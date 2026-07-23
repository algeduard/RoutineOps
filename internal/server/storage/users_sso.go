package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// SSO/OIDC (миграция 045). Матчинг ТОЛЬКО по неизменяемой паре (issuer, subject), email —
// display-атрибут. Server-side state авторизационного флоу — sso_auth_flows.

const ssoFlowTTL = 10 * time.Minute

// ErrSSOEmailTaken — INSERT SSO-юзера уперся в UNIQUE по email (не в (issuer,subject)):
// email уже занят ДРУГИМ аккаунтом (гонка с параллельным созданием). Вызывающий трактует
// как email-коллизию (отказ авто-линка), а не как гонку той же (iss,sub).
var ErrSSOEmailTaken = errors.New("sso email already taken by another account")

// GetUserByOIDCIdentity ищет SSO-native юзера по стабильному (issuer, subject). nil = нет.
func (db *DB) GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (*User, error) {
	var u User
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, email, password_hash, role, created_at, auth_source, oidc_issuer, oidc_subject
		FROM users WHERE auth_source = 'oidc' AND oidc_issuer = $1 AND oidc_subject = $2
	`, issuer, subject).Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.AuthSource, &u.OIDCIssuer, &u.OIDCSubject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByEmailCI — case-insensitive поиск по email (для проверки коллизии при SSO-линке:
// email в БД мог быть заведён в другом регистре). nil = нет.
func (db *DB) GetUserByEmailCI(ctx context.Context, email string) (*User, error) {
	var u User
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, email, password_hash, role, created_at, auth_source, oidc_issuer, oidc_subject
		FROM users WHERE lower(email) = lower($1) LIMIT 1
	`, email).Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.AuthSource, &u.OIDCIssuer, &u.OIDCSubject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// CreateSSOUser создаёт SSO-native юзера (auth_source='oidc') с НЕиспользуемым паролем
// (bcrypt от crypto/rand — локальный вход по нему невозможен, плюс отдельный auth_source-гард).
// Гонку двух параллельных JIT ловит партиал-UNIQUE (oidc_issuer,oidc_subject): при конфликте
// возвращаем уже созданного конкурентом юзера (идемпотентно).
func (db *DB) CreateSSOUser(ctx context.Context, name, email, role, issuer, subject string) (*User, error) {
	rnd := make([]byte, 32)
	if _, err := rand.Read(rnd); err != nil {
		return nil, err
	}
	unusable, err := bcrypt.GenerateFromPassword(rnd, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	var u User
	err = db.pool.QueryRow(ctx, `
		INSERT INTO users (name, email, password_hash, role, auth_source, oidc_issuer, oidc_subject)
		VALUES ($1, $2, $3, $4, 'oidc', $5, $6)
		RETURNING id, name, email, password_hash, role, created_at, auth_source, oidc_issuer, oidc_subject
	`, name, email, string(unusable), role, issuer, subject).
		Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.AuthSource, &u.OIDCIssuer, &u.OIDCSubject)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "users_oidc_identity":
				// Гонка: конкурентный callback уже создал эту (issuer,subject) — берём его
				// строку. Проверяем ТОЛЬКО этот индекс: у users есть и UNIQUE по email
				// (мигр. 001), и его нарушение (тот же email, ДРУГАЯ iss/sub) сюда бы не
				// подошло — вернуло бы (nil,nil) и уронило вызывающего.
				existing, gerr := db.GetUserByOIDCIdentity(ctx, issuer, subject)
				if gerr != nil {
					return nil, gerr
				}
				if existing == nil {
					return nil, err // строки нет (аномалия) — не отдаём (nil,nil)
				}
				return existing, nil
			case "users_email_key":
				// email занят другим аккаунтом (гонка с параллельным созданием) → коллизия.
				return nil, ErrSSOEmailTaken
			default:
				return nil, err
			}
		}
		return nil, err
	}
	return &u, nil
}

// UpdateUserRole меняет роль (для пересчёта роли SSO-native юзера из claim). Только роль,
// пароль/token-epoch не трогает.
func (db *DB) UpdateUserRole(ctx context.Context, userID, role string) error {
	_, err := db.pool.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, userID, role)
	return err
}

// InsertSSOFlow сохраняет state/nonce/pkce_verifier авторизационного флоу и попутно чистит
// протухшие строки (opportunistic cleanup на каждом /login).
func (db *DB) InsertSSOFlow(ctx context.Context, state, nonce, verifier string) error {
	if _, err := db.pool.Exec(ctx,
		`DELETE FROM sso_auth_flows WHERE created_at < now() - ($1 * interval '1 second')`,
		ssoFlowTTL.Seconds()); err != nil {
		return err
	}
	_, err := db.pool.Exec(ctx,
		`INSERT INTO sso_auth_flows (state, nonce, pkce_verifier) VALUES ($1, $2, $3)`,
		state, nonce, verifier)
	return err
}

// ConsumeSSOFlow атомарно ЗАБИРАЕТ (SELECT+DELETE одним DELETE...RETURNING) строку флоу по
// state — валиден РОВНО одному конкурентному callback (single-use, детект replay/гонки).
// ok=false: state не найден / уже израсходован / протух (TTL).
func (db *DB) ConsumeSSOFlow(ctx context.Context, state string) (nonce, verifier string, ok bool, err error) {
	var createdAt time.Time
	err = db.pool.QueryRow(ctx,
		`DELETE FROM sso_auth_flows WHERE state = $1 RETURNING nonce, pkce_verifier, created_at`,
		state).Scan(&nonce, &verifier, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	if time.Since(createdAt) > ssoFlowTTL {
		// Строка уже удалена (single-use соблюдён), но протухла — отвергаем.
		return "", "", false, nil
	}
	return nonce, verifier, true, nil
}

// DeleteExpiredSSOFlows — периодическая чистка (cleanup-воркер main.go).
func (db *DB) DeleteExpiredSSOFlows(ctx context.Context) (int64, error) {
	tag, err := db.pool.Exec(ctx,
		`DELETE FROM sso_auth_flows WHERE created_at < now() - ($1 * interval '1 second')`,
		ssoFlowTTL.Seconds())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
