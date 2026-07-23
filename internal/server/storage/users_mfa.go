package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// MFA-состояние пользователя (миграция 044). userID везде — string, как в остальном
// postgres.go (pgx кастует к uuid). Шифрование/расшифровка секрета — в mfa_crypto.go,
// эти методы работают уже с зашифрованным blob.

// GetUserMFA читает MFA-поля юзера. secretEnc == nil, если секрета нет (NULL).
func (db *DB) GetUserMFA(ctx context.Context, userID string) (enabled bool, secretEnc []byte, lastStep int64, confirmedAt *time.Time, err error) {
	err = db.pool.QueryRow(ctx, `
		SELECT totp_enabled, totp_secret_enc, totp_last_step, totp_confirmed_at
		FROM users WHERE id = $1
	`, userID).Scan(&enabled, &secretEnc, &lastStep, &confirmedAt)
	return enabled, secretEnc, lastStep, confirmedAt, err
}

// SetUserTOTPPending записывает НЕподтверждённый секрет (enrollment начат, но кодом ещё не
// подтверждён): enabled остаётся false, last_step сбрасывается. MFA на логине не требуется,
// пока enabled=false, поэтому висящий pending-секрет ни на что не влияет.
func (db *DB) SetUserTOTPPending(ctx context.Context, userID string, secretEnc []byte) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE users
		SET totp_secret_enc = $2, totp_enabled = false, totp_confirmed_at = NULL, totp_last_step = 0
		WHERE id = $1
	`, userID, secretEnc)
	return err
}

// ConfirmUserTOTP включает MFA после подтверждения секрета кодом: enabled=true,
// confirmed_at=now(), last_step=matchedCounter (сразу закрывает replay первого кода) и
// в той же транзакции заменяет набор recovery-кодов на переданные хеши.
func (db *DB) ConfirmUserTOTP(ctx context.Context, userID string, matchedCounter int64, codeHashes []string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET totp_enabled = true, totp_confirmed_at = now(), totp_last_step = $2
		WHERE id = $1
	`, userID, matchedCounter); err != nil {
		return err
	}
	if err := replaceRecoveryCodesTx(ctx, tx, userID, codeHashes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DisableUserTOTP полностью снимает MFA: обнуляет все totp_*-поля и удаляет recovery-коды.
// Используется и для самостоятельного отключения, и для admin-reset (крипто ничего не
// проверяет → работает даже без enc-ключа).
func (db *DB) DisableUserTOTP(ctx context.Context, userID string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET totp_secret_enc = NULL, totp_enabled = false, totp_confirmed_at = NULL, totp_last_step = 0
		WHERE id = $1
	`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdvanceTOTPLastStep атомарно двигает last_step на matchedCounter, НО только если тот
// строго больше текущего (CAS). Возвращает advanced=false, если counter уже использован
// (replay): вызывающий обязан отвергнуть вход. Это авторитетный replay-guard — read-check-
// write в хендлере гонку не закрывает, а этот UPDATE ... WHERE закрывает.
func (db *DB) AdvanceTOTPLastStep(ctx context.Context, userID string, counter int64) (advanced bool, err error) {
	tag, err := db.pool.Exec(ctx, `
		UPDATE users SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2
	`, userID, counter)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReplaceRecoveryCodes заменяет весь набор recovery-кодов юзера (регенерация): удаляет
// старые, вставляет новые хеши. Атомарно.
func (db *DB) ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := replaceRecoveryCodesTx(ctx, tx, userID, codeHashes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func replaceRecoveryCodesTx(ctx context.Context, tx pgx.Tx, userID string, codeHashes []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
			return err
		}
	}
	return nil
}

// ConsumeRecoveryCode ищет recovery-код по хешу В ПРЕДЕЛАХ user_id и УДАЛЯЕТ его (одноразово,
// атомарно). ok=false — код не найден/уже использован.
func (db *DB) ConsumeRecoveryCode(ctx context.Context, userID, codeHash string) (ok bool, err error) {
	tag, err := db.pool.Exec(ctx,
		`DELETE FROM mfa_recovery_codes WHERE user_id = $1 AND code_hash = $2`, userID, codeHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RecoveryCodeExists — есть ли у юзера непогашенный recovery-код с таким хешем. Проверка
// БЕЗ расхода: расход отдельным ConsumeRecoveryCode уже ПОСЛЕ атомарного клейма challenge,
// чтобы проигравший гонку/просроченный запрос не сжигал одноразовый код зря.
func (db *DB) RecoveryCodeExists(ctx context.Context, userID, codeHash string) (bool, error) {
	var one int
	err := db.pool.QueryRow(ctx,
		`SELECT 1 FROM mfa_recovery_codes WHERE user_id = $1 AND code_hash = $2`, userID, codeHash).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CountRecoveryCodes — сколько неиспользованных recovery-кодов осталось у юзера (для UI).
func (db *DB) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx,
		`SELECT count(*) FROM mfa_recovery_codes WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// CountEnabledMFAUsers — сколько юзеров с включённой MFA (для стартового warn в main.go,
// если enc-ключ не задан).
func (db *DB) CountEnabledMFAUsers(ctx context.Context) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE totp_enabled`).Scan(&n)
	return n, err
}
