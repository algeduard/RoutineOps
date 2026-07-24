//go:build enterprise

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/cvesync"
	"github.com/go-chi/chi/v5"
)

// CVEFeedSourceRoutes монтирует /cve/feed-source* — настройку внешнего источника CVE-фида и
// его форс-синхронизацию (расширение CVE-сканирования, enterprise-фича FeatureCVEScan). Вместо
// ручной заливки POST /cve/feed деплойер указывает URL выгрузки (тот же JSON-формат), интервал
// и тумблер авто-синка; фоновый синкер (internal/server/cvesync) тянет фид по расписанию.
// Гейт по лицензии (mgr.Has → 402) в каждом хендлере. Ставится через WithAdminRoutes (it_admin):
// URL источника и запуск синка — чувствительные операции. В open-core роутов нет (404).
func CVEFeedSourceRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	// Разумные границы интервала: не чаще раза в час (анти-DoS на источник) и не реже раза в
	// месяц (иначе «авто-синк» превращается в почти-ручной).
	const (
		minIntervalHours = 1
		maxIntervalHours = 24 * 30
	)
	return func(h *Handler, r chi.Router) {
		// GET /cve/feed-source — текущий конфиг источника + статус последнего синка.
		r.Get("/cve/feed-source", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			cfg, err := h.db.GetCVEFeedSource(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		})

		// PUT /cve/feed-source — сохранить конфиг источника.
		r.Put("/cve/feed-source", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			var body struct {
				URL               string `json:"url"`
				SyncIntervalHours int    `json:"sync_interval_hours"`
				Enabled           bool   `json:"enabled"`
				AutoScan          bool   `json:"auto_scan"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			body.URL = strings.TrimSpace(body.URL)
			// При включении требуем валидный http(s)-URL: иначе фоновый синкер тихо ничего не
			// тянул бы. Внутренние адреса РАЗРЕШЕНЫ намеренно — фид-прокси часто в закрытом контуре.
			if body.Enabled {
				u, err := url.Parse(body.URL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					http.Error(w, "url must be a valid http(s) URL when enabled", http.StatusBadRequest)
					return
				}
			}
			if body.SyncIntervalHours < minIntervalHours {
				body.SyncIntervalHours = minIntervalHours
			}
			if body.SyncIntervalHours > maxIntervalHours {
				body.SyncIntervalHours = maxIntervalHours
			}
			if err := h.db.SetCVEFeedSource(req.Context(), body.URL, body.SyncIntervalHours, body.Enabled, body.AutoScan); err != nil {
				slog.Error("set cve feed source", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			// URL кладём в аудит без кредов (redactWebhookURL): фид-URL может нести токен, а
			// /audit-log читают все роли.
			h.audit(req.Context(), claims.UserID, claims.Email, "cve_feed_source_set", "cve", "",
				map[string]any{"enabled": body.Enabled, "url": redactWebhookURL(body.URL),
					"interval_hours": body.SyncIntervalHours, "auto_scan": body.AutoScan})

			cfg, err := h.db.GetCVEFeedSource(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		})

		// POST /cve/feed-source/sync — форсировать синк сейчас (даже при выключенном авто-синке).
		// Недоступный источник НЕ ошибка запроса: возвращаем 200 с конфигом, где last_status
		// начинается с 'error:' — UI покажет причину без ложного «сбой сервера».
		r.Post("/cve/feed-source/sync", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureCVEScan) {
				http.Error(w, "CVE scanning requires a license", http.StatusPaymentRequired)
				return
			}
			loaded, status, err := cvesync.Sync(req.Context(), h.db, nil)
			if err != nil {
				// err != nil — только реальный сбой БД (см. cvesync.Sync).
				slog.Error("cve feed source sync", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			h.audit(req.Context(), claims.UserID, claims.Email, "cve_feed_synced", "cve", "",
				map[string]any{"count": loaded, "status": status})

			cfg, err := h.db.GetCVEFeedSource(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		})
	}
}
