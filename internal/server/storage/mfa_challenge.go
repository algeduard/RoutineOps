package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Промежуточный challenge между шагом-1 (пароль) и шагом-2 (TOTP) логина. Хранится
// sha256-хешем токена; сам токен отдаётся клиенту в теле ответа шага-1. См. миграцию 044.

// CreateMFAChallenge вставляет challenge (по хешу) с TTL и попутно чистит истёкшие/
// израсходованные строки этого же юзера (opportunistic cleanup — держит таблицу маленькой
// без отдельного прохода).
func (db *DB) CreateMFAChallenge(ctx context.Context, userID, tokenHash string, ttl time.Duration) error {
	if _, err := db.pool.Exec(ctx,
		`DELETE FROM mfa_challenges WHERE user_id = $1 AND (expires_at < now() OR consumed_at IS NOT NULL)`,
		userID); err != nil {
		return err
	}
	_, err := db.pool.Exec(ctx, `
		INSERT INTO mfa_challenges (user_id, token_hash, expires_at)
		VALUES ($1, $2, now() + ($3 * interval '1 second'))
	`, userID, tokenHash, ttl.Seconds())
	return err
}

// LookupMFAChallenge возвращает user_id действительного challenge (не израсходован, не
// истёк). ok=false — не найден/использован/просрочен. Только чтение: расход — отдельным
// атомарным MarkMFAChallengeConsumed уже ПОСЛЕ успешной проверки кода.
func (db *DB) LookupMFAChallenge(ctx context.Context, tokenHash string) (userID string, ok bool, err error) {
	err = db.pool.QueryRow(ctx, `
		SELECT user_id FROM mfa_challenges
		WHERE token_hash = $1 AND consumed_at IS NULL AND now() < expires_at
	`, tokenHash).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return userID, true, nil
}

// MarkMFAChallengeConsumed атомарно помечает challenge израсходованным (одноразовость).
// ok=false — гонка/уже израсходован/истёк: вызывающий отвергает вход.
func (db *DB) MarkMFAChallengeConsumed(ctx context.Context, tokenHash string) (userID string, ok bool, err error) {
	err = db.pool.QueryRow(ctx, `
		UPDATE mfa_challenges SET consumed_at = now()
		WHERE token_hash = $1 AND consumed_at IS NULL AND now() < expires_at
		RETURNING user_id
	`, tokenHash).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return userID, true, nil
}

// DeleteExpiredMFAChallenges чистит истёкшие/израсходованные challenge (периодический
// проход из cleanup-воркера main.go).
func (db *DB) DeleteExpiredMFAChallenges(ctx context.Context) (int64, error) {
	tag, err := db.pool.Exec(ctx,
		`DELETE FROM mfa_challenges WHERE expires_at < now() OR consumed_at IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
