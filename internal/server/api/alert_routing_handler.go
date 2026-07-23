//go:build enterprise

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// AlertRoutingRoutes монтирует CRUD правил маршрутизации алертов (enterprise-фича
// FeatureAlertRouting). Гейт по лицензии (mgr.Has → 402). Ставится через WithAdminRoutes
// (it_admin): правила определяют, куда уходят security-уведомления. В open-core роутов нет
// (404). Саму доставку по правилам делает фоновый маршрутизатор (internal/server/alertrouting).
func AlertRoutingRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/alert-routing-rules", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureAlertRouting) {
				http.Error(w, "alert routing requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			rules, err := h.db.ListAlertRoutingRules(req.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if rules == nil {
				rules = []storage.AlertRoutingRule{}
			}
			writeJSON(w, http.StatusOK, rules)
		})

		r.Post("/alert-routing-rules", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureAlertRouting) {
				http.Error(w, "alert routing requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			var body struct {
				MinSeverity          string `json:"min_severity"`
				Channel              string `json:"channel"`
				Target               string `json:"target"`
				Enabled              *bool  `json:"enabled"`
				EscalateAfterMinutes int    `json:"escalate_after_minutes"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			body.MinSeverity = strings.TrimSpace(body.MinSeverity)
			if body.MinSeverity == "" {
				body.MinSeverity = storage.SeverityWarning
			}
			if !storage.ValidAlertSeverity(body.MinSeverity) {
				http.Error(w, "min_severity must be info, warning or critical", http.StatusBadRequest)
				return
			}
			body.Channel = strings.TrimSpace(body.Channel)
			if !storage.ValidAlertChannel(body.Channel) {
				http.Error(w, "channel must be telegram or webhook", http.StatusBadRequest)
				return
			}
			body.Target = strings.TrimSpace(body.Target)
			if body.Target == "" {
				http.Error(w, "target is required", http.StatusBadRequest)
				return
			}
			// Валидируем target под канал: webhook — валидный http(s)-URL (внутренние адреса
			// разрешены намеренно, как в SIEM: приёмник часто в закрытом контуре); telegram —
			// целочисленный chat_id (у групп он отрицательный), иначе доставка молча падала бы.
			switch body.Channel {
			case storage.AlertChannelWebhook:
				u, err := url.Parse(body.Target)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					http.Error(w, "target must be a valid http(s) URL for a webhook channel", http.StatusBadRequest)
					return
				}
			case storage.AlertChannelTelegram:
				if _, err := strconv.ParseInt(body.Target, 10, 64); err != nil {
					http.Error(w, "target must be a numeric Telegram chat_id", http.StatusBadRequest)
					return
				}
			}
			if body.EscalateAfterMinutes < 0 {
				body.EscalateAfterMinutes = 0
			}
			// Верхний кап минут: защита от абсурдных значений (переполнение интервала не
			// грозит, но осмысленная эскалация укладывается в неделю).
			if body.EscalateAfterMinutes > 10080 {
				body.EscalateAfterMinutes = 10080
			}
			enabled := true
			if body.Enabled != nil {
				enabled = *body.Enabled
			}

			rule, err := h.db.CreateAlertRoutingRule(req.Context(), body.MinSeverity, body.Channel, body.Target, enabled, body.EscalateAfterMinutes)
			if err != nil {
				slog.Error("create alert routing rule", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			// target вебхука нередко несёт токен (user:pass@ или ?token=), а /audit-log
			// читают ВСЕ роли (в т.ч. viewer) — кладём без кредов. chat_id telegram не секрет.
			h.audit(req.Context(), claims.UserID, claims.Email, "alert_rule_created", "alert_routing_rule", rule.ID,
				map[string]any{
					"min_severity":           rule.MinSeverity,
					"channel":                rule.Channel,
					"target":                 auditTarget(rule.Channel, rule.Target),
					"enabled":                rule.Enabled,
					"escalate_after_minutes": rule.EscalateAfterMinutes,
				})
			writeJSON(w, http.StatusOK, rule)
		})

		r.Delete("/alert-routing-rules/{id}", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureAlertRouting) {
				http.Error(w, "alert routing requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			id := chi.URLParam(req, "id")
			found, err := h.db.DeleteAlertRoutingRule(req.Context(), id)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !found {
				http.Error(w, "rule not found", http.StatusNotFound)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)
			h.audit(req.Context(), claims.UserID, claims.Email, "alert_rule_deleted", "alert_routing_rule", id, nil)
			writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		})
	}
}

// auditTarget готовит target к записи в аудит: у вебхука режет креды из URL (redactWebhookURL),
// chat_id telegram оставляет как есть (не секрет).
func auditTarget(channel, target string) string {
	if channel == storage.AlertChannelWebhook {
		return redactWebhookURL(target)
	}
	return target
}
