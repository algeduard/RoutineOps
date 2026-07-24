package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Уровни критичности алертов. Порядок задаётся AlertSeverityRank: правило срабатывает на
// алерт, у которого rank(severity) >= rank(min_severity).
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Каналы доставки правила маршрутизации.
const (
	AlertChannelTelegram = "telegram"
	AlertChannelWebhook  = "webhook"
)

// ValidAlertSeverity сообщает, входит ли s в допустимый набор уровней критичности.
func ValidAlertSeverity(s string) bool {
	return s == SeverityInfo || s == SeverityWarning || s == SeverityCritical
}

// ValidAlertChannel сообщает, входит ли c в допустимый набор каналов доставки.
func ValidAlertChannel(c string) bool {
	return c == AlertChannelTelegram || c == AlertChannelWebhook
}

// AlertSeverityRank — числовой ранг критичности для сравнения severity >= min_severity.
// Неизвестное значение трактуем как warning (как DEFAULT колонки severity) — чтобы кривая
// строка не проваливалась в info и не переставала матчиться warning-правилами.
func AlertSeverityRank(sev string) int {
	switch sev {
	case SeverityCritical:
		return 2
	case SeverityInfo:
		return 0
	default: // warning и всё неизвестное
		return 1
	}
}

// AlertRoutingRule — правило маршрутизации алерта (миграция 048, enterprise-фича).
type AlertRoutingRule struct {
	ID                   string    `json:"id"`
	MinSeverity          string    `json:"min_severity"`
	Channel              string    `json:"channel"`
	Target               string    `json:"target"`
	Enabled              bool      `json:"enabled"`
	EscalateAfterMinutes int       `json:"escalate_after_minutes"`
	CreatedAt            time.Time `json:"created_at"`
}

// RoutableAlert — срез алерта, нужный маршрутизатору для доставки/эскалации (без полей UI).
type RoutableAlert struct {
	ID              string
	DeviceID        string
	DeviceHostname  string
	AlertType       string
	Details         string
	Severity        string
	CreatedAt       time.Time
	LastEscalatedAt *time.Time
}

// ListAlertRoutingRules отдаёт все правила маршрутизации (новые первыми).
func (db *DB) ListAlertRoutingRules(ctx context.Context) ([]AlertRoutingRule, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, min_severity, channel, target, enabled, escalate_after_minutes, created_at
		FROM alert_routing_rules
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertRoutingRule
	for rows.Next() {
		var r AlertRoutingRule
		if err := rows.Scan(&r.ID, &r.MinSeverity, &r.Channel, &r.Target, &r.Enabled,
			&r.EscalateAfterMinutes, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateAlertRoutingRule вставляет правило и возвращает созданную строку.
func (db *DB) CreateAlertRoutingRule(ctx context.Context, minSeverity, channel, target string, enabled bool, escalateAfterMinutes int) (AlertRoutingRule, error) {
	var r AlertRoutingRule
	err := db.pool.QueryRow(ctx, `
		INSERT INTO alert_routing_rules (min_severity, channel, target, enabled, escalate_after_minutes)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, min_severity, channel, target, enabled, escalate_after_minutes, created_at`,
		minSeverity, channel, target, enabled, escalateAfterMinutes).
		Scan(&r.ID, &r.MinSeverity, &r.Channel, &r.Target, &r.Enabled, &r.EscalateAfterMinutes, &r.CreatedAt)
	return r, err
}

// DeleteAlertRoutingRule удаляет правило по id. found=false — строки не было.
func (db *DB) DeleteAlertRoutingRule(ctx context.Context, id string) (bool, error) {
	tag, err := db.pool.Exec(ctx, `DELETE FROM alert_routing_rules WHERE id = $1::uuid`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListUnroutedAlerts отдаёт ещё не обработанные маршрутизатором алерты (routed_at IS NULL),
// НЕ моложе minAgeSeconds (safety-lag против гонки INSERT→visible, по образцу SIEM-курсора),
// самые старые первыми. Основа durable-курсора маршрутизации.
func (db *DB) ListUnroutedAlerts(ctx context.Context, minAgeSeconds, limit int) ([]RoutableAlert, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.pool.Query(ctx, `
		SELECT a.id, a.device_id, COALESCE(d.hostname, ''), a.alert_type, a.details, a.severity, a.created_at, a.last_escalated_at
		FROM alerts a
		LEFT JOIN devices d ON d.id = a.device_id
		WHERE a.routed_at IS NULL
		  AND a.created_at <= now() - ($1 * interval '1 second')
		ORDER BY a.created_at ASC
		LIMIT $2`, minAgeSeconds, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRoutableAlerts(rows)
}

// MarkAlertRouted помечает алерт обработанным маршрутизатором (routed_at = now()). Идемпотентно
// (guard routed_at IS NULL): повторный вызов в гонке не сдвигает уже проставленное время.
func (db *DB) MarkAlertRouted(ctx context.Context, alertID string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE alerts SET routed_at = now() WHERE id = $1::uuid AND routed_at IS NULL`, alertID)
	return err
}

// ListEscalatableAlerts отдаёт кандидатов на эскалацию: НЕпринятые critical-алерты, уже
// прошедшие первичную маршрутизацию (routed_at IS NOT NULL). Порог по времени и анти-спам
// (last_escalated_at) проверяет вызывающий по escalate_after_minutes каждого правила.
func (db *DB) ListEscalatableAlerts(ctx context.Context, limit int) ([]RoutableAlert, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.pool.Query(ctx, `
		SELECT a.id, a.device_id, COALESCE(d.hostname, ''), a.alert_type, a.details, a.severity, a.created_at, a.last_escalated_at
		FROM alerts a
		LEFT JOIN devices d ON d.id = a.device_id
		WHERE a.acknowledged_at IS NULL
		  AND a.routed_at IS NOT NULL
		  AND a.severity = 'critical'
		ORDER BY a.created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRoutableAlerts(rows)
}

// MarkAlertEscalated фиксирует время повторной доставки (анти-спам эскалации). Оставлена
// для совместимости; per-rule анти-спам теперь ведёт TryEscalateRule (таблица
// alert_rule_escalations), а не общая колонка alerts.last_escalated_at.
func (db *DB) MarkAlertEscalated(ctx context.Context, alertID string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE alerts SET last_escalated_at = now() WHERE id = $1::uuid`, alertID)
	return err
}

// TryEscalateRule атомарно «проверяет-и-отмечает» per-(alert, rule) анти-спам эскалации.
// Один upsert: при первой эскалации пары (alertID, ruleID) вставляет строку, при повторе
// обновляет last_escalated_at ТОЛЬКО если с прошлой эскалации прошло не меньше
// thresholdSeconds. Возвращает true, если эскалировать нужно СЕЙЧАС (строка вставлена или
// обновлена — RETURNING отдал строку), false — если анти-спам ещё держит (прошлая эскалация
// свежее порога: DO UPDATE ... WHERE не сматчил, RETURNING пуст).
//
// Порог у каждого правила свой, поэтому состояние ведётся per-(alert, rule): быстрое правило
// больше не сбрасывает окно медленного (была общая колонка last_escalated_at — медленный
// канал тихо терялся). Атомарность upsert'а даёт корректность при гонке тиков/узлов: ON
// CONFLICT сериализует конкурентные вставки по первичному ключу, и ровно один вызов получит
// строку из RETURNING.
func (db *DB) TryEscalateRule(ctx context.Context, alertID, ruleID string, thresholdSeconds int) (bool, error) {
	var returned string
	err := db.pool.QueryRow(ctx, `
		INSERT INTO alert_rule_escalations (alert_id, rule_id)
		VALUES ($1::uuid, $2::uuid)
		ON CONFLICT (alert_id, rule_id) DO UPDATE
		  SET last_escalated_at = now()
		  WHERE alert_rule_escalations.last_escalated_at < now() - ($3 * interval '1 second')
		RETURNING alert_id`, alertID, ruleID, thresholdSeconds).Scan(&returned)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // анти-спам: прошлая эскалация этого правила ещё свежее порога
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// rowScanner — минимальный интерфейс pgx.Rows, нужный scanRoutableAlerts.
type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanRoutableAlerts(rows rowScanner) ([]RoutableAlert, error) {
	var out []RoutableAlert
	for rows.Next() {
		var a RoutableAlert
		if err := rows.Scan(&a.ID, &a.DeviceID, &a.DeviceHostname, &a.AlertType,
			&a.Details, &a.Severity, &a.CreatedAt, &a.LastEscalatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
