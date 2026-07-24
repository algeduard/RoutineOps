package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DefaultTenantID — фиксированный id строки "Default" (см. migrations/050). В неё бэкфилятся
// все существующие сущности и попадают новые (DEFAULT колонки tenant_id). Неудаляема.
const DefaultTenantID = "00000000-0000-0000-0000-000000000001"

// Ошибки управления тенантами. Маппятся API-хендлером на HTTP-коды (409/404).
var (
	// ErrTenantExists — name или slug уже заняты (unique-нарушение 23505).
	ErrTenantExists = errors.New("tenant with this name or slug already exists")
	// ErrTenantNotFound — тенанта с таким id нет.
	ErrTenantNotFound = errors.New("tenant not found")
	// ErrTenantIsDefault — попытка удалить default-тенант (он неудаляем).
	ErrTenantIsDefault = errors.New("the default tenant cannot be deleted")
	// ErrTenantNotEmpty — попытка удалить тенант, к которому ещё привязаны устройства/юзеры;
	// сначала их надо переназначить (иначе они бы осиротели через ON DELETE SET NULL).
	ErrTenantNotEmpty = errors.New("tenant still has devices or users assigned")
)

// Tenant — арендатор. DeviceCount/UserCount заполняются только в ListTenants (счётчики
// привязанных сущностей); в остальных путях 0. IsDefault=true у неудаляемого default-тенанта.
type Tenant struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	CreatedAt   time.Time `json:"created_at"`
	IsDefault   bool      `json:"is_default"`
	DeviceCount int       `json:"device_count"`
	UserCount   int       `json:"user_count"`
}

// CreateTenant заводит тенант. Конфликт name/slug (23505) → ErrTenantExists (API отдаёт 409).
func (db *DB) CreateTenant(ctx context.Context, name, slug string) (*Tenant, error) {
	var tnt Tenant
	err := db.pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug) VALUES ($1, $2)
		RETURNING id, name, slug, created_at
	`, name, slug).Scan(&tnt.ID, &tnt.Name, &tnt.Slug, &tnt.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrTenantExists
		}
		return nil, err
	}
	tnt.IsDefault = tnt.ID == DefaultTenantID
	return &tnt, nil
}

// ListTenants отдаёт все тенанты со счётчиками привязанных устройств и пользователей.
// Default идёт первым, дальше по имени. Счётчики — коррелированными подзапросами (парк
// невелик; при росте — заменить на GROUP BY с LEFT JOIN).
func (db *DB) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT t.id, t.name, t.slug, t.created_at,
		       (SELECT count(*) FROM devices d WHERE d.tenant_id = t.id),
		       (SELECT count(*) FROM users u WHERE u.tenant_id = t.id)
		FROM tenants t
		ORDER BY (t.id = $1) DESC, t.name
	`, DefaultTenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tenant
	for rows.Next() {
		var tnt Tenant
		if err := rows.Scan(&tnt.ID, &tnt.Name, &tnt.Slug, &tnt.CreatedAt, &tnt.DeviceCount, &tnt.UserCount); err != nil {
			return nil, err
		}
		tnt.IsDefault = tnt.ID == DefaultTenantID
		out = append(out, tnt)
	}
	return out, rows.Err()
}

// GetTenant отдаёт тенант по id или (nil, nil), если его нет. Хендлер зовёт его перед
// назначением, чтобы отдать 404 на несуществующий тенант, а не ловить FK-нарушение.
func (db *DB) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	var tnt Tenant
	err := db.pool.QueryRow(ctx,
		`SELECT id, name, slug, created_at FROM tenants WHERE id = $1`, id).
		Scan(&tnt.ID, &tnt.Name, &tnt.Slug, &tnt.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	tnt.IsDefault = tnt.ID == DefaultTenantID
	return &tnt, nil
}

// RenameTenant меняет отображаемое имя (slug неизменяем — это машинный ключ). Конфликт имени
// (23505) → ErrTenantExists; строки нет → ErrTenantNotFound. Default переименовать МОЖНО
// (меняется только name; slug='default' и фиксированный id остаются).
func (db *DB) RenameTenant(ctx context.Context, id, name string) error {
	tag, err := db.pool.Exec(ctx, `UPDATE tenants SET name = $2 WHERE id = $1`, id, name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrTenantExists
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTenantNotFound
	}
	return nil
}

// DeleteTenant удаляет тенант. Default неудаляем (ErrTenantIsDefault). Непустой (есть
// привязанные устройства/юзеры) не удаляется (ErrTenantNotEmpty): проверка пустоты и само
// удаление — одним условным DELETE (атомарно, без TOCTOU-гонки с параллельным назначением).
// Строки нет → ErrTenantNotFound.
func (db *DB) DeleteTenant(ctx context.Context, id string) error {
	if id == DefaultTenantID {
		return ErrTenantIsDefault
	}
	tag, err := db.pool.Exec(ctx, `
		DELETE FROM tenants
		WHERE id = $1
		  AND NOT EXISTS (SELECT 1 FROM devices WHERE tenant_id = $1)
		  AND NOT EXISTS (SELECT 1 FROM users   WHERE tenant_id = $1)
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	// Ничего не удалено: либо тенанта нет, либо он непуст — различаем отдельным запросом.
	var exists bool
	if err := db.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrTenantNotFound
	}
	return ErrTenantNotEmpty
}

// GetUserTenantID возвращает tenant_id пользователя (для установки per-request scope в
// jwtMiddleware). Осиротевший (tenant удалён → ON DELETE SET NULL) или отсутствующий tenant_id
// трактуем как DefaultTenantID: провайдер-скоуп безопаснее «пустого» (не отрежет актора от его
// собственных данных из-за рассинхрона). Юзера нет → ErrTenantNotFound (middleware уже отбил бы
// такой токен по epoch-проверке, но перестрахуемся).
func (db *DB) GetUserTenantID(ctx context.Context, userID string) (string, error) {
	var tid *string
	err := db.pool.QueryRow(ctx, `SELECT tenant_id FROM users WHERE id = $1`, userID).Scan(&tid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrTenantNotFound
		}
		return "", err
	}
	if tid == nil || *tid == "" {
		return DefaultTenantID, nil
	}
	return *tid, nil
}

// AssignDeviceTenant привязывает устройство к тенанту (found=false, если устройства нет → 404).
// tenantID должен существовать — хендлер проверяет это заранее (GetTenant), поэтому FK тут
// не нарушится в штатном потоке.
func (db *DB) AssignDeviceTenant(ctx context.Context, deviceID, tenantID string) (found bool, err error) {
	tag, err := db.pool.Exec(ctx, `UPDATE devices SET tenant_id = $2 WHERE id = $1`, deviceID, tenantID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// AssignUserTenant привязывает пользователя к тенанту (found=false, если юзера нет). См.
// AssignDeviceTenant про гарантию существования tenantID.
func (db *DB) AssignUserTenant(ctx context.Context, userID, tenantID string) (found bool, err error) {
	tag, err := db.pool.Exec(ctx, `UPDATE users SET tenant_id = $2 WHERE id = $1`, userID, tenantID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
