package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// SIEMExportConfig — конфиг форвардинга аудита в SIEM (singleton). HMACSecret и LastSentSeq
// наружу не отдаём (json:"-"): секрет — тайна, курсор — внутренняя механика.
type SIEMExportConfig struct {
	Enabled     bool      `json:"enabled"`
	WebhookURL  string    `json:"webhook_url"`
	HMACSecret  string    `json:"-"`
	LastSentSeq int64     `json:"-"`
	UpdatedAt   time.Time `json:"updated_at"`
	// HasSecret — задан ли секрет (для UI: показать «секрет установлен», не раскрывая его).
	HasSecret bool `json:"has_secret"`
}

// GetSIEMExportConfig отдаёт конфиг. Пустой (Enabled=false) — если строки ещё нет.
func (db *DB) GetSIEMExportConfig(ctx context.Context) (SIEMExportConfig, error) {
	var c SIEMExportConfig
	err := db.pool.QueryRow(ctx, `
		SELECT enabled, webhook_url, hmac_secret, last_sent_seq, updated_at
		FROM siem_export_config WHERE id = 1`).
		Scan(&c.Enabled, &c.WebhookURL, &c.HMACSecret, &c.LastSentSeq, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return SIEMExportConfig{}, nil
		}
		return SIEMExportConfig{}, err
	}
	c.HasSecret = c.HMACSecret != ""
	return c, nil
}

// SetSIEMExportConfig апсертит singleton-конфиг. Пустой secret означает «оставить прежний»
// (админ меняет URL, не вводя секрет повторно). При ВКЛючении, если курсор ещё 0, он
// инициализируется текущим максимумом seq — форвардим только новые события, не историю.
func (db *DB) SetSIEMExportConfig(ctx context.Context, enabled bool, webhookURL, secret string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO siem_export_config (id, enabled, webhook_url, hmac_secret, updated_at)
		VALUES (1, $1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE SET
			enabled = $1,
			webhook_url = $2,
			hmac_secret = CASE WHEN $3 = '' THEN siem_export_config.hmac_secret ELSE $3 END,
			updated_at = now()
	`, enabled, webhookURL, secret); err != nil {
		return err
	}
	if enabled {
		// Инициализация курсора «с текущего момента» ровно один раз (пока он 0).
		if _, err := tx.Exec(ctx, `
			UPDATE siem_export_config
			SET last_sent_seq = (SELECT COALESCE(MAX(seq), 0) FROM audit_log)
			WHERE id = 1 AND last_sent_seq = 0`); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ListAuditSince отдаёт события с seq > afterSeq по возрастанию, НО не моложе minAgeSeconds
// (safety-lag), не больше limit. Основа durable-курсора экспортёра.
//
// ⚠ Зачем лаг: seq (IDENTITY) присваивается на INSERT (nextval), а видимым ряд становится
// на COMMIT — при конкуренции ряд с МЕНЬШИМ seq может закоммититься ПОЗЖЕ ряда с бо́льшим.
// Курсор, прыгнув на больший seq (advance-only), навсегда пропустил бы меньший ещё-не-
// видимый ряд → тихая потеря аудита (анти-паттерн «sequence как CDC-курсор»). Аудит пишется
// короткими autocommit-INSERT'ами (WriteAuditLog), поэтому окно seq→commit — миллисекунды;
// экспортируя лишь ряды старше нескольких секунд, гарантируем, что все конкурентные ряды с
// меньшим seq уже закоммичены и видимы. minAgeSeconds=0 отключает лаг (для тестов).
func (db *DB) ListAuditSince(ctx context.Context, afterSeq int64, minAgeSeconds, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.pool.Query(ctx, `
		SELECT seq, id, user_id, user_email, action, target_type, target_id,
		       COALESCE(details::text, 'null'), created_at
		FROM audit_log
		WHERE seq > $1 AND created_at <= now() - ($2 * interval '1 second')
		ORDER BY seq ASC LIMIT $3`, afterSeq, minAgeSeconds, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var detailsRaw string
		if err := rows.Scan(&e.Seq, &e.ID, &e.UserID, &e.UserEmail, &e.Action,
			&e.TargetType, &e.TargetID, &detailsRaw, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Details = json.RawMessage(detailsRaw)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AdvanceSIEMCursor двигает курсор вперёд (только вперёд — guard last_sent_seq < $1 не
// откатит его при гонке/повторе).
func (db *DB) AdvanceSIEMCursor(ctx context.Context, seq int64) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE siem_export_config SET last_sent_seq = $1 WHERE id = 1 AND last_sent_seq < $1`, seq)
	return err
}
