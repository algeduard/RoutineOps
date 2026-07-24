package storage

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// SAML 2.0 SSO (миграция 058). Идентичность SAML-юзеров хранится в тех же колонках, что и OIDC
// (миграция 045): auth_source='saml', oidc_issuer=IdP EntityID, oidc_subject=NameID. Матчинг
// ТОЛЬКО по неизменяемой паре (issuer, subject); email — display-атрибут. Тонкие обёртки над
// теми же паттернами, что GetUserByOIDCIdentity/CreateSSOUser, но с auth_source='saml' и
// собственным партиал-UNIQUE users_saml_identity.

// GetUserBySAMLIdentity ищет SAML-native юзера по стабильному (issuer=EntityID, subject=NameID).
// nil = нет.
func (db *DB) GetUserBySAMLIdentity(ctx context.Context, issuer, subject string) (*User, error) {
	var u User
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, email, password_hash, role, created_at, auth_source, oidc_issuer, oidc_subject
		FROM users WHERE auth_source = 'saml' AND oidc_issuer = $1 AND oidc_subject = $2
	`, issuer, subject).Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.AuthSource, &u.OIDCIssuer, &u.OIDCSubject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// CreateSAMLUser создаёт SAML-native юзера (auth_source='saml') с НЕиспользуемым паролем
// (bcrypt от crypto/rand — локальный вход по нему невозможен, плюс auth_source-гард в login).
// Гонку двух параллельных JIT ловит партиал-UNIQUE users_saml_identity (issuer,subject):
// при конфликте возвращаем уже созданного конкурентом юзера (идемпотентно). Коллизия по
// глобальному UNIQUE email (другой аккаунт) → ErrSSOEmailTaken (переиспользуем из OIDC-пути:
// вызывающий трактует как отказ авто-линка).
func (db *DB) CreateSAMLUser(ctx context.Context, name, email, role, issuer, subject string) (*User, error) {
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
		VALUES ($1, $2, $3, $4, 'saml', $5, $6)
		RETURNING id, name, email, password_hash, role, created_at, auth_source, oidc_issuer, oidc_subject
	`, name, email, string(unusable), role, issuer, subject).
		Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.AuthSource, &u.OIDCIssuer, &u.OIDCSubject)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "users_saml_identity":
				// Гонка: конкурентный ACS уже создал эту (issuer,subject) — берём его строку.
				existing, gerr := db.GetUserBySAMLIdentity(ctx, issuer, subject)
				if gerr != nil {
					return nil, gerr
				}
				if existing == nil {
					return nil, err // строки нет (аномалия) — не отдаём (nil,nil)
				}
				return existing, nil
			case "users_email_key":
				// email занят ДРУГИМ аккаунтом (гонка с параллельным созданием) → коллизия.
				return nil, ErrSSOEmailTaken
			default:
				return nil, err
			}
		}
		return nil, err
	}
	return &u, nil
}
