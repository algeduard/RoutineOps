//go:build enterprise

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// CVERoutes монтирует /cve/* — сканирование инвентаря установленного ПО на известные
// уязвимости (CVE), enterprise-фича. Источник данных — уже собираемый инвентарь
// device_software; нового сбора от агента нет. Гейт по лицензии (mgr.Has → 402) в каждом
// хендлере. Ставится через WithAdminRoutes (it_admin): фид/скан — чувствительные операции,
// а находки — security-данные. В open-core роутов нет вовсе (404) — по коду UI отличает
// «недоступно в редакции» от сбоя.
//
// Формат фида (POST /cve/feed) — JSON-массив; деплойер заливает выгрузку из NVD/OSV:
//
//	[{"cve_id":"CVE-2023-1234","product":"Google Chrome","version_constraint":"<120.0.0",
//	  "severity":"high","cvss":8.1,"summary":"...","published_at":"2023-01-01T00:00:00Z"}]
//
// POST ЗАМЕНЯЕТ фид целиком (идемпотентная загрузка снапшота), DELETE очищает. Внешний
// сетевой фид на старте намеренно не тянем — деплойер сам управляет выгрузкой периодически.
// Тело ограничено 1 МБ (глобальный RequestSize-мидлвар) — для MVP достаточно; потоковую
// заливку крупных дампов оставляем на follow-up.
func CVERoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		// POST /cve/feed — залить/заменить фид уязвимостей.
		r.Post("/cve/feed", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			var entries []storage.CVEFeedEntry
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&entries); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			n, err := h.db.LoadCVEFeed(req.Context(), entries)
			if err != nil {
				slog.Error("load cve feed", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			h.audit(req.Context(), claims.UserID, claims.Email, "cve_feed_loaded", "cve", "",
				map[string]int{"count": n})
			writeJSON(w, http.StatusOK, map[string]int{"loaded": n})
		})

		// DELETE /cve/feed — очистить фид.
		r.Delete("/cve/feed", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			n, err := h.db.ClearCVEFeed(req.Context())
			if err != nil {
				slog.Error("clear cve feed", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			h.audit(req.Context(), claims.UserID, claims.Email, "cve_feed_cleared", "cve", "",
				map[string]int64{"count": n})
			writeJSON(w, http.StatusOK, map[string]int64{"cleared": n})
		})

		// POST /cve/scan — прогнать матчинг по всему парку, пересобрать находки.
		r.Post("/cve/scan", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			n, err := h.db.ScanCVE(req.Context())
			if err != nil {
				slog.Error("cve scan", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			h.audit(req.Context(), claims.UserID, claims.Email, "cve_scan_run", "cve", "",
				map[string]int{"findings": n})
			writeJSON(w, http.StatusOK, map[string]int{"findings": n})
		})

		// GET /cve/findings?device_id=&severity= — список находок.
		r.Get("/cve/findings", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			severity := strings.TrimSpace(req.URL.Query().Get("severity"))
			if severity != "" && !validCVESeverity(severity) {
				http.Error(w, "severity must be one of low|medium|high|critical", http.StatusBadRequest)
				return
			}
			deviceID := strings.TrimSpace(req.URL.Query().Get("device_id"))
			findings, err := h.db.ListCVEFindings(req.Context(), deviceID, severity)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if findings == nil {
				findings = []storage.CVEFinding{}
			}
			writeJSON(w, http.StatusOK, findings)
		})

		// GET /cve/summary — сводка по severity и по устройствам.
		r.Get("/cve/summary", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			s, err := h.db.CVESummaryData(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, s)
		})
	}
}

func validCVESeverity(s string) bool {
	switch s {
	case "low", "medium", "high", "critical":
		return true
	}
	return false
}
