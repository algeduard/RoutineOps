//go:build enterprise

package api

import (
	"net/http"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/go-chi/chi/v5"
)

// AuditIntegrityRoutes монтирует GET /audit-log/verify — проверку целостности журнала
// аудита (tamper-evidence, enterprise-фича). Гейт по лицензии (mgr.Has → 402). Read-only,
// но it_admin (WithAdminRoutes): статус целостности — админ/безопасность. В open-core роута
// нет (404). Сама подпись записей — базовая (пишется всегда при ROUTINEOPS_AUDIT_HMAC_KEY),
// платная лишь верификация/отчёт.
func AuditIntegrityRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/audit-log/verify", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureAuditIntegrity) {
				http.Error(w, "audit integrity verification requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			res, err := h.db.VerifyAuditIntegrity(req.Context(), 10000)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, res)
		})
	}
}
