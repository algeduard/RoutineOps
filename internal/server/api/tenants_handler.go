//go:build enterprise

package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// slugRe — машинный идентификатор тенанта: lowercase [a-z0-9], разделённый одиночными
// дефисами (без ведущего/замыкающего дефиса и без двойных). Валиден напр. "acme", "acme-ru".
var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TenantsRoutes монтирует управление тенантами (enterprise-фича «мультитенантность», MVP:
// модель тенантов + назначение сущностей). Гейт по лицензии в КАЖДОМ хендлере: без активной
// лицензии на фичу — 402, БД не трогаем. Ставится через WithAdminRoutes (it_admin) — это
// организационная структура парка. В open-core роутов нет вовсе (404).
//
// ⚠ SCOPE: это ФУНДАМЕНТ. ПОЛНАЯ изоляция данных (scoping каждого запроса к
// devices/users/tasks/... по tenant_id текущего актора) — большой cross-cutting рефактор и
// он FOLLOW-UP; здесь не делается. Сейчас: CRUD тенантов + назначение устройств/юзеров.
func TenantsRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/tenants", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureMultitenancy) {
				http.Error(w, "multitenancy requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			tenants, err := h.db.ListTenants(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if tenants == nil {
				tenants = []storage.Tenant{}
			}
			writeJSON(w, http.StatusOK, tenants)
		})

		r.Post("/tenants", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureMultitenancy) {
				http.Error(w, "multitenancy requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			var body struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			name := strings.TrimSpace(body.Name)
			slug := strings.ToLower(strings.TrimSpace(body.Slug))
			if name == "" {
				http.Error(w, "name is required", http.StatusBadRequest)
				return
			}
			if !slugRe.MatchString(slug) || len(slug) > 63 {
				http.Error(w, "slug must be lowercase [a-z0-9-] (no leading/trailing/double dash), up to 63 chars", http.StatusBadRequest)
				return
			}
			tnt, err := h.db.CreateTenant(req.Context(), name, slug)
			if err != nil {
				if errors.Is(err, storage.ErrTenantExists) {
					http.Error(w, "a tenant with this name or slug already exists", http.StatusConflict)
					return
				}
				slog.Error("create tenant", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			uid, email, _ := Actor(req.Context())
			h.audit(req.Context(), uid, email, "tenant_created", "tenant", tnt.ID,
				map[string]string{"name": tnt.Name, "slug": tnt.Slug})
			writeJSON(w, http.StatusCreated, tnt)
		})

		r.Patch("/tenants/{id}", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureMultitenancy) {
				http.Error(w, "multitenancy requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			id := chi.URLParam(req, "id")
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			name := strings.TrimSpace(body.Name)
			if name == "" {
				http.Error(w, "name is required", http.StatusBadRequest)
				return
			}
			switch err := h.db.RenameTenant(req.Context(), id, name); {
			case err == nil:
				// ok
			case errors.Is(err, storage.ErrTenantNotFound):
				http.Error(w, "tenant not found", http.StatusNotFound)
				return
			case errors.Is(err, storage.ErrTenantExists):
				http.Error(w, "a tenant with this name already exists", http.StatusConflict)
				return
			default:
				slog.Error("rename tenant", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			uid, email, _ := Actor(req.Context())
			h.audit(req.Context(), uid, email, "tenant_renamed", "tenant", id,
				map[string]string{"name": name})
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

		r.Delete("/tenants/{id}", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureMultitenancy) {
				http.Error(w, "multitenancy requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			id := chi.URLParam(req, "id")
			switch err := h.db.DeleteTenant(req.Context(), id); {
			case err == nil:
				// ok
			case errors.Is(err, storage.ErrTenantIsDefault):
				http.Error(w, "the default tenant cannot be deleted", http.StatusConflict)
				return
			case errors.Is(err, storage.ErrTenantNotEmpty):
				http.Error(w, "tenant still has devices or users; reassign them first", http.StatusConflict)
				return
			case errors.Is(err, storage.ErrTenantNotFound):
				http.Error(w, "tenant not found", http.StatusNotFound)
				return
			default:
				slog.Error("delete tenant", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			uid, email, _ := Actor(req.Context())
			h.audit(req.Context(), uid, email, "tenant_deleted", "tenant", id, nil)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

		// POST /tenants/{id}/assign — назначить устройства и/или пользователей тенанту одним
		// вызовом. Назначения идемпотентны; несуществующие id просто не попадают в счётчик
		// (found=false), поэтому частичный вход не роняет запрос.
		r.Post("/tenants/{id}/assign", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureMultitenancy) {
				http.Error(w, "multitenancy requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			id := chi.URLParam(req, "id")
			var body struct {
				DeviceIDs []string `json:"device_ids"`
				UserIDs   []string `json:"user_ids"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			// Тенант должен существовать: проверяем заранее, чтобы отдать 404, а не ловить
			// FK-нарушение на UPDATE (и не логировать его как 500).
			tnt, err := h.db.GetTenant(req.Context(), id)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if tnt == nil {
				http.Error(w, "tenant not found", http.StatusNotFound)
				return
			}
			var devicesAssigned, usersAssigned int
			for _, did := range body.DeviceIDs {
				did = strings.TrimSpace(did)
				if did == "" {
					continue
				}
				found, err := h.db.AssignDeviceTenant(req.Context(), did, id)
				if err != nil {
					slog.Error("assign device tenant", "device_id", did, "err", err)
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				if found {
					devicesAssigned++
				}
			}
			for _, uidToAssign := range body.UserIDs {
				uidToAssign = strings.TrimSpace(uidToAssign)
				if uidToAssign == "" {
					continue
				}
				found, err := h.db.AssignUserTenant(req.Context(), uidToAssign, id)
				if err != nil {
					slog.Error("assign user tenant", "user_id", uidToAssign, "err", err)
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				if found {
					usersAssigned++
				}
			}
			actorID, email, _ := Actor(req.Context())
			h.audit(req.Context(), actorID, email, "tenant_assigned", "tenant", id,
				map[string]int{"devices": devicesAssigned, "users": usersAssigned})
			writeJSON(w, http.StatusOK, map[string]int{
				"devices_assigned": devicesAssigned,
				"users_assigned":   usersAssigned,
			})
		})
	}
}
