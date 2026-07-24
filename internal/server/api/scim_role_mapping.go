//go:build enterprise

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// SCIM group→role mapping (расширение SCIM-провижининга, миграция 056). Раньше все SCIM-юзеры
// получали жёстко роль viewer; теперь роль вычисляется из групп IdP в SCIM User payload по
// настраиваемому маппингу (admin-группы → it_admin через allowlist, иначе default_role).
// Применение — на create/update юзера (scim_enterprise.go); настройка — админ-ручки ниже.

// scimRoleMappingMaxLen — потолок длины нормализованного CSV admin-групп: защита от абсурдно
// большого конфига (значения групп короткие; тысячи символов — явная ошибка/абьюз).
const scimRoleMappingMaxLen = 8192

// scimGroupRef — запись группы в SCIM User payload. Многие IdP (Okta, Azure AD) шлют членство
// в группах прямо в User-ресурсе: value — id/имя группы, display — отображаемое имя. Для
// матчинга против allowlist берём ОБА (IdP различаются в том, что кладут в allowlist).
type scimGroupRef struct {
	Value   string `json:"value"`
	Display string `json:"display"`
}

// groupCandidates собирает из групп payload все непустые кандидаты (value и display) для
// сверки с allowlist admin-групп.
func groupCandidates(groups []scimGroupRef) []string {
	out := make([]string, 0, len(groups)*2)
	for _, g := range groups {
		if v := strings.TrimSpace(g.Value); v != "" {
			out = append(out, v)
		}
		if d := strings.TrimSpace(g.Display); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// scimRoleFromGroups: любая группа ∈ adminSet → it_admin, иначе defaultRole. Fail-closed —
// it_admin ТОЛЬКО явным совпадением; пустой adminSet делает it_admin недостижимым (как SSO
// roleFromClaim). Аналог инварианта «it_admin по умолчанию НИКОГДА».
func scimRoleFromGroups(groups []scimGroupRef, adminSet map[string]bool, defaultRole string) string {
	for _, cand := range groupCandidates(groups) {
		if adminSet[cand] {
			return "it_admin"
		}
	}
	return defaultRole
}

// scimRole вычисляет роль из групп payload и конфига маппинга. present=false, если поле groups
// в payload отсутствует (nil): вызывающий решает — create берёт default, update НЕ трогает
// текущую роль (fail-closed: нет groups → не понижаем молча, как SSO при отсутствии claim).
// Ошибку загрузки конфига трактуем fail-closed: пустой маппинг → default viewer, it_admin
// недостижим.
func (p *scimProvider) scimRole(ctx context.Context, groups *[]scimGroupRef) (role string, present bool) {
	m, err := p.db.GetSCIMRoleMapping(ctx)
	if err != nil {
		slog.Error("scim role mapping load", "err", err)
		m = storage.SCIMRoleMapping{}
	}
	def := m.EffectiveDefaultRole()
	if groups == nil {
		return def, false
	}
	return scimRoleFromGroups(*groups, m.AdminGroupSet(), def), true
}

// reconcileRole пересчитывает роль SCIM-юзера из групп payload на update — так отзыв admin-
// группы в IdP приводит к downgrade. НЕ трогает роль не-SCIM аккаунтов (auth_source != 'scim':
// SCIM их роль не создаёт и не меняет) и НЕ понижает при отсутствии groups (present=false,
// fail-closed). Смену роли аудитит существующим событием scim_user_updated.
func (p *scimProvider) reconcileRole(ctx context.Context, u *storage.SCIMUser, groups *[]scimGroupRef) {
	if u.AuthSource != "scim" {
		return
	}
	role, present := p.scimRole(ctx, groups)
	if !present {
		return
	}
	changed, err := p.db.SetSCIMUserRole(ctx, u.ID, role)
	if err != nil {
		slog.Error("scim set user role", "err", err, "user", u.ID)
		return
	}
	if changed {
		p.audit(ctx, "scim_user_updated", u.ID, map[string]any{"email": u.UserName, "role": role})
	}
}

// SCIMRoleMappingRoutes монтирует GET/PUT /scim/role-mapping — настройку маппинга SCIM-групп
// на роли (enterprise-фича FeatureSCIM). Гейт по лицензии (mgr.Has → 402). Ставится через
// WithAdminRoutes (it_admin): маппинг решает, кто получит it_admin через IdP, — чувствительное
// админ-действие. В open-core роутов нет (404). Применение маппинга — в scim_enterprise.go.
func SCIMRoleMappingRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/scim/role-mapping", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureSCIM) {
				http.Error(w, "SCIM requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			m, err := h.db.GetSCIMRoleMapping(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, m)
		})

		r.Put("/scim/role-mapping", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureSCIM) {
				http.Error(w, "SCIM requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			var body struct {
				AdminGroupValues string `json:"admin_group_values"`
				DefaultRole      string `json:"default_role"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 32*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			// Нормализуем CSV admin-групп: trim записей, отбрасываем пустые, дедуп. Пустой
			// список = it_admin через SCIM не выдаётся никому (fail-closed, allowlist).
			admin := normalizeCSV(body.AdminGroupValues)
			if len(admin) > scimRoleMappingMaxLen {
				http.Error(w, "admin_group_values is too long", http.StatusBadRequest)
				return
			}
			// default_role — роль для юзеров БЕЗ admin-группы. it_admin ЗАПРЕЩЁН: эскалация
			// только явной группой (инвариант «it_admin по умолчанию НИКОГДА»). Пусто → viewer.
			defRole := strings.TrimSpace(body.DefaultRole)
			if defRole == "" {
				defRole = scimDefaultRole
			}
			if defRole == "it_admin" {
				http.Error(w, "default_role cannot be it_admin (grant it_admin only via an admin group)", http.StatusBadRequest)
				return
			}
			if !validRoles[defRole] {
				http.Error(w, "default_role must be a valid role", http.StatusBadRequest)
				return
			}
			if err := h.db.SetSCIMRoleMapping(req.Context(), admin, defRole); err != nil {
				slog.Error("set scim role mapping", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			userID, email, _ := Actor(req.Context())
			h.audit(req.Context(), userID, email, "scim_role_mapping_changed", "scim", "",
				map[string]any{"admin_group_values": admin, "default_role": defRole})
			m, err := h.db.GetSCIMRoleMapping(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, m)
		})
	}
}

// normalizeCSV тримит записи CSV, отбрасывает пустые и дедуплицирует, сохраняя порядок.
func normalizeCSV(raw string) string {
	seen := map[string]bool{}
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return strings.Join(out, ",")
}
