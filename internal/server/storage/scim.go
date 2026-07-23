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

// SCIM 2.0 provisioning (миграция 046). Хранилище не за build-тегом (как siem.go): схема
// базовая, гейт по лицензии — на уровне хендлера (scim_handler.go / scim_enterprise.go).

// ErrSCIMUserExists — POST /scim/v2/Users с уже занятым userName (email). Хендлер → 409.
var ErrSCIMUserExists = errors.New("scim user already exists")

// SCIMUser — представление строки users для SCIM 2.0 User. UserName = email (стабильный
// идентификатор входа), Formatted = users.name (отображаемое имя). GivenName/FamilyName —
// как прислал IdP. Active = is_active. AuthSource нужен хендлеру, чтобы НЕ трогать роль/пароль
// существующего локального админа при апдейте по SCIM (см. UpdateSCIMUser).
type SCIMUser struct {
	ID         string
	UserName   string
	GivenName  string
	FamilyName string
	Formatted  string
	Active     bool
	AuthSource string
	CreatedAt  time.Time
}

// scimUserCols — фиксированный порядок колонок для scanSCIMUser (SELECT/RETURNING).
const scimUserCols = `id, email, scim_given_name, scim_family_name, name, is_active, auth_source, created_at`

func scanSCIMUser(row pgx.Row) (*SCIMUser, error) {
	var u SCIMUser
	if err := row.Scan(&u.ID, &u.UserName, &u.GivenName, &u.FamilyName, &u.Formatted,
		&u.Active, &u.AuthSource, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// scimNotFound маппит «строки нет» И «id не UUID» (22P02) в (nil, nil): {id} из URL —
// произвольная строка, а users.id это uuid; несуществующий/битый id одинаково = 404.
func scimNotFound(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

// ── SCIM bearer-токен (singleton) ────────────────────────────────────────────

// GetSCIMTokenHash отдаёт sha256-хекс активного SCIM-токена. "" = SCIM выключен (токен не
// сгенерирован либо строки ещё нет).
func (db *DB) GetSCIMTokenHash(ctx context.Context) (string, error) {
	var h string
	err := db.pool.QueryRow(ctx, `SELECT token_hash FROM scim_config WHERE id = true`).Scan(&h)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return h, nil
}

// SetSCIMTokenHash апсертит singleton-хеш токена (ротация = перезапись, created_at → now()).
func (db *DB) SetSCIMTokenHash(ctx context.Context, hash string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO scim_config (id, token_hash, created_at) VALUES (true, $1, now())
		ON CONFLICT (id) DO UPDATE SET token_hash = $1, created_at = now()`, hash)
	return err
}

// SCIMEnabled — сгенерирован ли токен (для UI-статуса на странице управления SCIM).
func (db *DB) SCIMEnabled(ctx context.Context) (bool, error) {
	h, err := db.GetSCIMTokenHash(ctx)
	return h != "", err
}

// ── Провижининг пользователей ────────────────────────────────────────────────

// ListSCIMUsers — страница юзеров для SCIM ListResponse. filterUserName != "" → точный
// case-insensitive матч по email (?filter=userName eq "x"), иначе все юзеры (IdP так находит
// уже существующих SSO/локальных, чтобы управлять их active). startIndex 1-based (SCIM),
// count — размер страницы (0 = вернуть только totalResults). Возвращает (страница, totalResults).
func (db *DB) ListSCIMUsers(ctx context.Context, filterUserName string, startIndex, count int) ([]SCIMUser, int, error) {
	if startIndex < 1 {
		startIndex = 1
	}
	if count < 0 {
		count = 0
	}
	var total int
	if err := db.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE ($1 = '' OR lower(email) = lower($1))`,
		filterUserName).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.pool.Query(ctx, `
		SELECT `+scimUserCols+`
		FROM users
		WHERE ($1 = '' OR lower(email) = lower($1))
		ORDER BY created_at
		LIMIT $2 OFFSET $3`, filterUserName, count, startIndex-1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []SCIMUser
	for rows.Next() {
		u, serr := scanSCIMUser(rows)
		if serr != nil {
			return nil, 0, serr
		}
		out = append(out, *u)
	}
	return out, total, rows.Err()
}

// GetSCIMUserByID — по SCIM id (= users.id). nil = нет/битый id.
func (db *DB) GetSCIMUserByID(ctx context.Context, id string) (*SCIMUser, error) {
	u, err := scanSCIMUser(db.pool.QueryRow(ctx, `SELECT `+scimUserCols+` FROM users WHERE id = $1`, id))
	if err != nil {
		if scimNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// GetSCIMUserByEmail — case-insensitive поиск по userName (email). nil = нет. Используется при
// POST для явного 409 до INSERT (диагностика) и как обход гонки уникального индекса.
func (db *DB) GetSCIMUserByEmail(ctx context.Context, email string) (*SCIMUser, error) {
	u, err := scanSCIMUser(db.pool.QueryRow(ctx,
		`SELECT `+scimUserCols+` FROM users WHERE lower(email) = lower($1) LIMIT 1`, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// CreateSCIMUser провижинит нового юзера из SCIM: auth_source='scim', НЕиспользуемый пароль
// (bcrypt от crypto/rand — локальный вход невозможен, как у SSO). role обычно 'viewer'
// (least privilege). active управляет is_active. Дубль email (UNIQUE users_email_key) →
// ErrSCIMUserExists (хендлер отдаст 409 per SCIM).
func (db *DB) CreateSCIMUser(ctx context.Context, email, givenName, familyName, formatted, role string, active bool) (*SCIMUser, error) {
	rnd := make([]byte, 32)
	if _, err := rand.Read(rnd); err != nil {
		return nil, err
	}
	unusable, err := bcrypt.GenerateFromPassword(rnd, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u, err := scanSCIMUser(db.pool.QueryRow(ctx, `
		INSERT INTO users (name, email, password_hash, role, auth_source, is_active, scim_given_name, scim_family_name)
		VALUES ($1, $2, $3, $4, 'scim', $5, $6, $7)
		RETURNING `+scimUserCols,
		formatted, email, string(unusable), role, active, givenName, familyName))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrSCIMUserExists
		}
		return nil, err
	}
	return u, nil
}

// UpdateSCIMUser обновляет ТОЛЬКО SCIM-управляемые атрибуты (active + отображаемое имя).
// НИКОГДА не трогает role/password: если email принадлежит локальному админу, SCIM-канал не
// должен его повышать/понижать в правах или менять пароль — только тоггл active и имя (SCIM —
// доверенный канал провижининга от IdP, но не эскалации). nil = юзера нет.
func (db *DB) UpdateSCIMUser(ctx context.Context, id, givenName, familyName, formatted string, active bool) (*SCIMUser, error) {
	u, err := scanSCIMUser(db.pool.QueryRow(ctx, `
		UPDATE users
		SET is_active = $2, scim_given_name = $3, scim_family_name = $4, name = $5
		WHERE id = $1
		RETURNING `+scimUserCols, id, active, givenName, familyName, formatted))
	if err != nil {
		if scimNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// SetSCIMUserActive — точечная смена is_active (DELETE = деактивация, не хард-удаление; и
// PATCH active-only). role/пароль/имя не трогает. nil = юзера нет.
func (db *DB) SetSCIMUserActive(ctx context.Context, id string, active bool) (*SCIMUser, error) {
	u, err := scanSCIMUser(db.pool.QueryRow(ctx,
		`UPDATE users SET is_active = $2 WHERE id = $1 RETURNING `+scimUserCols, id, active))
	if err != nil {
		if scimNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// IsUserActive — активна ли учётка (для гейта в login: деактивированного по SCIM юзера не
// пускаем даже с верным паролем). false при отсутствии строки (юзер удалён).
func (db *DB) IsUserActive(ctx context.Context, userID string) (bool, error) {
	var active bool
	err := db.pool.QueryRow(ctx, `SELECT is_active FROM users WHERE id = $1`, userID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}
