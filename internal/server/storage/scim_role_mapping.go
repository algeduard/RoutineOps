package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SCIM group→role mapping (миграция 056) — расширение SCIM-провижининга: роль юзера
// вычисляется из групп IdP, пришедших в SCIM User payload. Хранилище не за build-тегом
// (как scim.go): схема базовая, гейт по лицензии — на уровне хендлера (scim_role_mapping.go).

// SCIMRoleMapping — singleton-конфиг маппинга SCIM-групп на роли. AdminGroupValues — CSV
// значений групп (allowlist), дающих it_admin; DefaultRole — роль для всех прочих юзеров
// (least privilege). Отдаётся наружу как есть — секретов не содержит.
type SCIMRoleMapping struct {
	AdminGroupValues string    `json:"admin_group_values"`
	DefaultRole      string    `json:"default_role"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// AdminGroupSet разбирает CSV admin-групп в множество (trim, без пустых) для матчинга.
func (m SCIMRoleMapping) AdminGroupSet() map[string]bool {
	set := map[string]bool{}
	for _, v := range strings.Split(m.AdminGroupValues, ",") {
		if v = strings.TrimSpace(v); v != "" {
			set[v] = true
		}
	}
	return set
}

// EffectiveDefaultRole — дефолтная роль, гарантированно НЕ it_admin (fail-closed: it_admin
// достижим только явным совпадением admin-группы, никогда по умолчанию). Пустое/it_admin →
// viewer. Дублирует инвариант API-валидации как защита в глубину на случай прямой записи в БД.
func (m SCIMRoleMapping) EffectiveDefaultRole() string {
	if m.DefaultRole == "" || m.DefaultRole == "it_admin" {
		return "viewer"
	}
	return m.DefaultRole
}

// GetSCIMRoleMapping отдаёт конфиг маппинга. Дефолт (admin-групп нет, роль viewer) — если
// строки ещё нет: it_admin через SCIM по умолчанию не выдаётся никому.
func (db *DB) GetSCIMRoleMapping(ctx context.Context) (SCIMRoleMapping, error) {
	var m SCIMRoleMapping
	err := db.pool.QueryRow(ctx, `
		SELECT admin_group_values, default_role, updated_at
		FROM scim_role_mapping WHERE id = true`).
		Scan(&m.AdminGroupValues, &m.DefaultRole, &m.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SCIMRoleMapping{DefaultRole: "viewer"}, nil
		}
		return SCIMRoleMapping{}, err
	}
	return m, nil
}

// SetSCIMRoleMapping апсертит singleton-конфиг (updated_at → now()). Значения валидирует
// вызывающий (API): admin_group_values уже нормализован, default_role не it_admin.
func (db *DB) SetSCIMRoleMapping(ctx context.Context, adminGroupValues, defaultRole string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO scim_role_mapping (id, admin_group_values, default_role, updated_at)
		VALUES (true, $1, $2, now())
		ON CONFLICT (id) DO UPDATE SET
			admin_group_values = $1,
			default_role = $2,
			updated_at = now()`, adminGroupValues, defaultRole)
	return err
}

// SetSCIMUserRole меняет роль ТОЛЬКО SCIM-аккаунта (auth_source='scim') и ТОЛЬКО если она
// реально меняется. Гейт по auth_source — fail-safe: роль локального/SSO-аккаунта SCIM-канал
// не трогает, даже если по его id пришёл update (как UpdateSCIMUser не трогает role/пароль).
// changed=false: роль та же / не-SCIM / нет юзера / битый id (scimNotFound).
func (db *DB) SetSCIMUserRole(ctx context.Context, id, role string) (changed bool, err error) {
	ct, err := db.pool.Exec(ctx,
		`UPDATE users SET role = $2 WHERE id = $1 AND auth_source = 'scim' AND role <> $2`, id, role)
	if err != nil {
		if scimNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}
