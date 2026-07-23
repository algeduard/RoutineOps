//go:build enterprise

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/go-chi/chi/v5"
)

// SCIMRoutes монтирует АДМИН-ручки управления SCIM-токеном (за лицензией FeatureSCIM):
//
//	GET  /scim/config — статус (включён ли SCIM) + base URL для настройки IdP.
//	POST /scim/token  — сгенерировать/ротировать bearer-токен (показывается ОДИН раз).
//
// Ставится через WithAdminRoutes (it_admin): выпуск bearer-токена, открывающего провижининг-
// канал (создание/деактивация юзеров), — чувствительное админ-действие, как применение лицензии
// или настройка SIEM. САМИ SCIM 2.0 эндпоинты (/scim/v2/*) — ПУБЛИЧНЫ (свой bearer, не JWT) и
// живут в scim_enterprise.go (WithSCIM). В open-core этих ручек нет (404).
func SCIMRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/scim/config", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureSCIM) {
				http.Error(w, "SCIM requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			enabled, err := h.db.SCIMEnabled(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"enabled":  enabled,
				"base_url": h.publicWebURL + "/scim/v2",
			})
		})

		r.Post("/scim/token", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureSCIM) {
				http.Error(w, "SCIM requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			// 32 случайных байта hex (256 бит). Храним только sha256-хеш; сравнение при запросе —
			// constant-time (см. scim_enterprise.go authOK). Сам токен показывается ОДИН раз.
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			token := hex.EncodeToString(raw)
			sum := sha256.Sum256([]byte(token))
			if err := h.db.SetSCIMTokenHash(req.Context(), hex.EncodeToString(sum[:])); err != nil {
				slog.Error("set scim token", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			userID, email, _ := Actor(req.Context())
			// Токен в аудит НЕ пишем (секрет) — только факт ротации.
			h.audit(req.Context(), userID, email, "scim_token_rotated", "scim", "", nil)
			writeJSON(w, http.StatusOK, map[string]any{
				"token":    token,
				"base_url": h.publicWebURL + "/scim/v2",
			})
		})
	}
}
