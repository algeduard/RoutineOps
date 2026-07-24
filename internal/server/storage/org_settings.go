package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Организационные настройки (миграция 054). Пока единственная — политика принуждения MFA.
// Таблица org_settings — singleton (одна строка, id BOOLEAN PK CHECK(id)).

// GetMFARequiredRole читает политику принуждения MFA: пусто (выкл) | 'it_admin' | 'all'.
// Отсутствие строки трактуется как пусто (выкл): это безопасный дефолт, при котором гейт
// никого не блокирует (в штатной инсталляции строка создаётся миграцией).
func (db *DB) GetMFARequiredRole(ctx context.Context) (string, error) {
	var role string
	err := db.pool.QueryRow(ctx,
		`SELECT mfa_required_role FROM org_settings WHERE id = true`).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// SetMFARequiredRole сохраняет политику принуждения MFA. UPSERT (а не голый UPDATE), чтобы
// самовосстановить singleton, если строка почему-то отсутствует (не оставить настройку без
// записи). Валидация значения — на уровне API.
func (db *DB) SetMFARequiredRole(ctx context.Context, role string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO org_settings (id, mfa_required_role, updated_at)
		VALUES (true, $1, now())
		ON CONFLICT (id) DO UPDATE SET mfa_required_role = EXCLUDED.mfa_required_role, updated_at = now()
	`, role)
	return err
}
