//go:build enterprise

// Package alertrouting доставляет алерты по правилам маршрутизации (enterprise-фича
// FeatureAlertRouting). Фоновый цикл поллит новые алерты по durable-курсору (routed_at),
// сопоставляет их severity с порогом каждого включённого правила (rank(severity) >=
// rank(min_severity)) и доставляет в канал правила (telegram-чат или webhook). Доставка
// best-effort: ошибка канала логируется, но не мешает пометить алерт обработанным и не
// роняет создание алерта. Отдельно тот же цикл эскалирует НЕпринятые critical-алерты старше
// порога escalate_after_minutes (повторная доставка с анти-спамом по last_escalated_at).
// Гейт по лицензии — снаружи (licensed()), чтобы пакет не зависел от internal/license.
package alertrouting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

const (
	tickInterval = 20 * time.Second // как часто проверять новые/просроченные алерты
	batchSize    = 200
	httpTimeout  = 10 * time.Second
	// safetyLagSeconds — маршрутизируем только алерты старше этого лага. Как и у SIEM-курсора,
	// закрывает гонку «строка вставлена, но ещё не видна»: короткий INSERT алерта коммитится
	// за миллисекунды, поэтому пары секунд достаточно, чтобы курсор не перепрыгнул невидимую
	// строку и не оставил её вечно необработанной.
	safetyLagSeconds = 3
)

// TelegramSendFunc доставляет текст в конкретный telegram-чат (nil = бот не сконфигурён).
// Совпадает по сигнатуре с (*notifier.Bot).SendToChat — main.go передаёт его напрямую.
type TelegramSendFunc func(ctx context.Context, chatID, text string) error

// Router — фоновый маршрутизатор алертов. licensed() гейтит по лицензии (пустой тик, если
// FeatureAlertRouting не активна), чтобы пакет не зависел от internal/license.
type Router struct {
	db           *storage.DB
	licensed     func() bool
	telegramSend TelegramSendFunc
	logger       *slog.Logger
	client       *http.Client
	// lagSeconds — safety-lag выборки новых алертов (см. safetyLagSeconds). Поле, а не
	// константа, чтобы тест мог обнулить лаг и обработать только что созданный алерт.
	lagSeconds int
}

func NewRouter(db *storage.DB, licensed func() bool, telegramSend TelegramSendFunc, logger *slog.Logger) *Router {
	return &Router{
		db:           db,
		licensed:     licensed,
		telegramSend: telegramSend,
		logger:       logger,
		client:       &http.Client{Timeout: httpTimeout},
		lagSeconds:   safetyLagSeconds,
	}
}

// Run крутит цикл маршрутизации до завершения процесса (как прочие фоновые циклы cmd/server).
func (r *Router) Run() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for range ticker.C {
		r.tick()
	}
}

func (r *Router) tick() {
	if !r.licensed() {
		return // лицензия не покрывает маршрутизацию — молча ничего не доставляем
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rules, err := r.db.ListAlertRoutingRules(ctx)
	if err != nil {
		r.logger.Error("alert routing: чтение правил", "err", err)
		return
	}
	enabled := enabledRules(rules)

	r.routeNew(ctx, enabled)
	r.escalate(ctx, enabled)
}

// routeNew доставляет свежие (ещё не обработанные) алерты по подходящим правилам и помечает
// их обработанными. Пометку ставим ДАЖЕ если правил нет или доставка не удалась — best-effort:
// иначе один непринятый канал держал бы алерт в очереди вечно и спамил бы повторами.
func (r *Router) routeNew(ctx context.Context, enabled []storage.AlertRoutingRule) {
	alerts, err := r.db.ListUnroutedAlerts(ctx, r.lagSeconds, batchSize)
	if err != nil {
		r.logger.Error("alert routing: выборка новых алертов", "err", err)
		return
	}
	for _, a := range alerts {
		for _, rule := range enabled {
			if storage.AlertSeverityRank(a.Severity) >= storage.AlertSeverityRank(rule.MinSeverity) {
				r.deliver(ctx, rule, a, false)
			}
		}
		if err := r.db.MarkAlertRouted(ctx, a.ID); err != nil {
			r.logger.Error("alert routing: пометка обработанным", "alert_id", a.ID, "err", err)
		}
	}
}

// escalate повторно доставляет НЕпринятые critical-алерты старше порога escalate_after_minutes
// у правил с эскалацией. Анти-спам: между повторами по одному алерту должно пройти не меньше
// порога (last_escalated_at). Помечаем алерт эскалированным, только если хоть одно правило
// реально сработало в этот тик.
func (r *Router) escalate(ctx context.Context, enabled []storage.AlertRoutingRule) {
	var escRules []storage.AlertRoutingRule
	for _, rule := range enabled {
		if rule.EscalateAfterMinutes > 0 {
			escRules = append(escRules, rule)
		}
	}
	if len(escRules) == 0 {
		return
	}
	alerts, err := r.db.ListEscalatableAlerts(ctx, batchSize)
	if err != nil {
		r.logger.Error("alert routing: выборка кандидатов эскалации", "err", err)
		return
	}
	now := time.Now()
	// ОГРАНИЧЕНИЕ (осознанное, MVP): анти-спам считается по ЕДИНОЙ на алерт колонке
	// last_escalated_at, а порог — у каждого правила свой. Если на один алерт матчатся
	// НЕСКОЛЬКО правил эскалации с РАЗНЫМИ escalate_after_minutes, быстрое правило на каждом
	// тике сбрасывает last_escalated_at, и медленное может не дозреть до своего порога. Для
	// типового одного правила эскалации всё корректно. Правильный фикс — per-(alert,rule)
	// состояние (таблица) — follow-up; здесь не делаем ради простоты MVP.
	for _, a := range alerts {
		escalated := false
		for _, rule := range escRules {
			if storage.AlertSeverityRank(a.Severity) < storage.AlertSeverityRank(rule.MinSeverity) {
				continue
			}
			threshold := time.Duration(rule.EscalateAfterMinutes) * time.Minute
			if now.Sub(a.CreatedAt) < threshold {
				continue // ещё не дозрел до порога этого правила
			}
			if a.LastEscalatedAt != nil && now.Sub(*a.LastEscalatedAt) < threshold {
				continue // недавно уже эскалировали — не спамим
			}
			r.deliver(ctx, rule, a, true)
			escalated = true
		}
		if escalated {
			if err := r.db.MarkAlertEscalated(ctx, a.ID); err != nil {
				r.logger.Error("alert routing: пометка эскалации", "alert_id", a.ID, "err", err)
			}
		}
	}
}

// deliver отправляет один алерт в канал правила. Best-effort: любая ошибка логируется, но
// не прерывает обработку остальных алертов/правил.
func (r *Router) deliver(ctx context.Context, rule storage.AlertRoutingRule, a storage.RoutableAlert, escalation bool) {
	switch rule.Channel {
	case storage.AlertChannelTelegram:
		if r.telegramSend == nil {
			r.logger.Warn("alert routing: telegram-канал недоступен (бот не сконфигурён)", "rule_id", rule.ID)
			return
		}
		if err := r.telegramSend(ctx, rule.Target, formatTelegram(a, escalation)); err != nil {
			r.logger.Warn("alert routing: доставка в telegram не удалась", "rule_id", rule.ID, "alert_id", a.ID, "err", err)
		}
	case storage.AlertChannelWebhook:
		if err := r.postWebhook(ctx, rule.Target, a, escalation); err != nil {
			// url логируем БЕЗ кредов: target вебхука мог нести токен в URL.
			r.logger.Warn("alert routing: доставка на webhook не удалась", "rule_id", rule.ID, "alert_id", a.ID, "url", redactURL(rule.Target), "err", err)
		}
	}
}

// postWebhook шлёт алерт JSON'ом на webhook. Успех — только 2xx. Без HMAC-подписи (в отличие
// от SIEM-экспорта): маршрутизация — уведомление, а не durable-форвардинг аудита.
func (r *Router) postWebhook(ctx context.Context, target string, a storage.RoutableAlert, escalation bool) error {
	body, err := json.Marshal(map[string]any{
		"alert": map[string]any{
			"id":              a.ID,
			"device_id":       a.DeviceID,
			"device_hostname": a.DeviceHostname,
			"alert_type":      a.AlertType,
			"severity":        a.Severity,
			"details":         a.Details,
			"created_at":      a.CreatedAt,
		},
		"escalation": escalation,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RoutineOps-Alert-Routing")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook вернул %d", resp.StatusCode)
	}
	return nil
}

// enabledRules отбирает включённые правила.
func enabledRules(rules []storage.AlertRoutingRule) []storage.AlertRoutingRule {
	out := rules[:0:0]
	for _, r := range rules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

// formatTelegram формирует текст уведомления для telegram (HTML parse_mode).
func formatTelegram(a storage.RoutableAlert, escalation bool) string {
	head := "🚨 <b>Алерт</b>"
	if escalation {
		head = "⏫ <b>Эскалация алерта</b>"
	}
	host := a.DeviceHostname
	if host == "" {
		host = a.DeviceID
	}
	// Экранируем интерполируемые поля: host/AlertType/Details приходят от устройства
	// (SecurityEvent) и без escape ломали бы parse_mode=HTML (Telegram 400 → тихая потеря
	// алерта) или инъектировали разметку/фишинг-ссылку в уведомление оператора.
	return fmt.Sprintf("%s [%s]\nТип: %s\nУстройство: <code>%s</code>\nДетали: %s",
		head, html.EscapeString(strings.ToUpper(a.Severity)), html.EscapeString(a.AlertType),
		html.EscapeString(host), html.EscapeString(a.Details))
}

// redactURL убирает креды из URL (userinfo и query) перед логированием: target вебхука мог
// нести токен в user:pass@ или ?token=. По образцу redactWebhookURL SIEM-экспорта.
func redactURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
