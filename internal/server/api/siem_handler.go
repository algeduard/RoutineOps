//go:build enterprise

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/go-chi/chi/v5"
)

// SIEMConfigRoutes монтирует GET/POST /siem/config — настройку форвардинга аудита в SIEM
// (enterprise-фича). Гейт по лицензии (mgr.Has → 402). Сам форвардинг делает фоновый
// экспортёр (internal/server/siem). Ставится через WithAdminRoutes (it_admin). В open-core
// роута нет (404). Секрет наружу не отдаётся (GET прячет hmac_secret, показывает has_secret).
func SIEMConfigRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/siem/config", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureSIEMExport) {
				http.Error(w, "SIEM export requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			cfg, err := h.db.GetSIEMExportConfig(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		})

		r.Post("/siem/config", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureSIEMExport) {
				http.Error(w, "SIEM export requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			var body struct {
				Enabled    bool   `json:"enabled"`
				WebhookURL string `json:"webhook_url"`
				HMACSecret string `json:"hmac_secret"` // пусто = оставить прежний секрет
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			body.WebhookURL = strings.TrimSpace(body.WebhookURL)
			// При включении требуем валидный http(s)-URL: экспортёр иначе тихо ничего не
			// слал бы. Внутренние адреса РАЗРЕШЕНЫ намеренно — SIEM часто в закрытом контуре.
			if body.Enabled {
				u, err := url.Parse(body.WebhookURL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					http.Error(w, "webhook_url must be a valid http(s) URL when enabled", http.StatusBadRequest)
					return
				}
			}
			if err := h.db.SetSIEMExportConfig(req.Context(), body.Enabled, body.WebhookURL, body.HMACSecret); err != nil {
				slog.Error("set siem config", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			// webhook_url редактируем: /audit-log читают ВСЕ роли (в т.ч. viewer), а SIEM-URL
			// нередко несёт токен (user:pass@ или ?token=) — в аудит кладём без кредов.
			h.audit(req.Context(), claims.UserID, claims.Email, "set_siem_config", "siem", "",
				map[string]any{"enabled": body.Enabled, "webhook_url": redactWebhookURL(body.WebhookURL), "secret_changed": body.HMACSecret != ""})

			cfg, err := h.db.GetSIEMExportConfig(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		})
	}
}

// redactWebhookURL убирает из URL встроенные креды (userinfo user:pass@ и query-токены)
// перед записью в аудит: /audit-log доступен всем ролям (в т.ч. viewer), а SIEM-вебхуки
// нередко несут токен в URL. Остаётся scheme://host/path — достаточно для аудита.
func redactWebhookURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
