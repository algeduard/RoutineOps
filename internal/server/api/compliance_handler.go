//go:build enterprise

package api

import (
	"encoding/csv"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// ComplianceRoutes монтирует GET /compliance/report — агрегированный отчёт соответствия
// (compliance-скор + набор CIS/SOC2-проверок по УЖЕ существующим данным: инвентарь,
// статусы устройств, MFA, политики, аудит). Enterprise-фича: гейт по лицензии
// (mgr.Has → 402). Read-only, но it_admin (WithAdminRoutes): compliance-отчёт — материал
// для аудита/безопасности. В open-core роута нет (404).
//
// ?format=csv отдаёт тот же отчёт CSV-файлом (для выгрузки во внешний аудит). Просмотр
// пишется в журнал (compliance_report_viewed), экспорт — отдельным действием
// (compliance_report_exported): кто и когда выгрузил отчёт наружу.
func ComplianceRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/compliance/report", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCompliance) {
				http.Error(w, "compliance reporting requires a license", http.StatusPaymentRequired)
				return
			}

			rep, err := h.db.ComplianceReport(req.Context())
			if err != nil {
				slog.Error("compliance report", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}

			userID, email, _ := Actor(req.Context())
			if req.URL.Query().Get("format") == "csv" {
				h.audit(req.Context(), userID, email, "compliance_report_exported", "compliance", "",
					map[string]any{"score": rep.Score, "checks": len(rep.Checks)})
				w.Header().Set("Content-Type", "text/csv; charset=utf-8")
				w.Header().Set("Content-Disposition", `attachment; filename="compliance-report.csv"`)
				w.WriteHeader(http.StatusOK)
				writeComplianceCSV(w, rep)
				return
			}

			h.audit(req.Context(), userID, email, "compliance_report_viewed", "compliance", "",
				map[string]any{"score": rep.Score})
			writeJSON(w, http.StatusOK, rep)
		})
	}
}

// writeComplianceCSV сериализует отчёт в CSV: строка на проверку + завершающая строка
// overall с общим скором (Passed=score, Total=100) — так весь отчёт, включая скор,
// читается одним парсером без отдельной шапки.
func writeComplianceCSV(w io.Writer, rep storage.ComplianceReport) {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "title", "category", "status", "passed", "total", "detail"})
	for _, c := range rep.Checks {
		_ = cw.Write([]string{c.ID, c.Title, c.Category, c.Status, strconv.Itoa(c.Passed), strconv.Itoa(c.Total), c.Detail})
	}
	_ = cw.Write([]string{"overall", "Overall compliance score", "summary", "", strconv.Itoa(rep.Score), "100", "Weighted compliance score (0-100)"})
	cw.Flush()
}
