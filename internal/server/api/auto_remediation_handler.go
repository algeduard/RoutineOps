//go:build enterprise

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// AutoRemediationRoutes монтирует /auto-remediation/* — настройку и лог авто-устранения
// запрещённого ПО (enterprise-фича FeatureAutoRemediation). Гейт по лицензии (mgr.Has → 402)
// в каждом хендлере. Ставится через WithAdminRoutes (it_admin): включение авто-удаления —
// деструктивное мутирующее действие. В open-core роутов нет вовсе (404). Само устранение
// делает фоновый ремедиатор (internal/server/remediation), переиспользуя путь удаления ПО.
func AutoRemediationRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		// GET /auto-remediation/config — текущая настройка (enabled / dry_run).
		r.Get("/auto-remediation/config", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureAutoRemediation) {
				http.Error(w, "auto-remediation requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			cfg, err := h.db.GetAutoRemediationConfig(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		})

		// PUT /auto-remediation/config — включить/выключить авто-устранение и режим dry_run.
		r.Put("/auto-remediation/config", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureAutoRemediation) {
				http.Error(w, "auto-remediation requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			var body struct {
				Enabled bool `json:"enabled"`
				DryRun  bool `json:"dry_run"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if err := h.db.SetAutoRemediationConfig(req.Context(), body.Enabled, body.DryRun); err != nil {
				slog.Error("set auto-remediation config", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			h.audit(req.Context(), claims.UserID, claims.Email, "set_auto_remediation_config", "auto_remediation", "",
				map[string]bool{"enabled": body.Enabled, "dry_run": body.DryRun})

			cfg, err := h.db.GetAutoRemediationConfig(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		})

		// GET /auto-remediation/log — история ремедиаций (что удалено / что удалили бы в dry_run).
		r.Get("/auto-remediation/log", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureAutoRemediation) {
				http.Error(w, "auto-remediation requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			entries, err := h.db.ListRemediationLog(req.Context(), 200)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if entries == nil {
				entries = []storage.RemediationLogEntry{}
			}
			writeJSON(w, http.StatusOK, entries)
		})
	}
}
