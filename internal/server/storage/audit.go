package storage

import (
	"context"
	"encoding/json"
	"time"
)

type AuditEntry struct {
	ID         string          `json:"id"`
	Seq        int64           `json:"seq,omitempty"` // монотонный курсор (миграция 042); 0 в UI-списке, >0 в SIEM-экспорте
	UserID     *string         `json:"user_id"`
	UserEmail  string          `json:"user_email"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (db *DB) WriteAuditLog(ctx context.Context, userID, userEmail, action, targetType, targetID string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var uid *string
	if userID != "" {
		uid = &userID
	}
	_, err = db.pool.Exec(ctx, `
  INSERT INTO audit_log (user_id, user_email, action, target_type, target_id, details)
  VALUES ($1, $2, $3, $4, $5, $6)
 `, uid, userEmail, action, targetType, targetID, string(raw))
	return err
}

func (db *DB) ListAuditLog(ctx context.Context, action string, limit int) ([]AuditEntry, error) {
	// Верхний clamp: без него `?limit=2000000000` от viewer'а материализует весь
	// (многолетний) audit_log в память → OOM. Тот же паттерн, что в ListScriptResultsByPolicy.
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var query string
	var args []any
	if action != "" {
		query = `SELECT id, user_id, user_email, action, target_type, target_id,
                  COALESCE(details::text, 'null'), created_at
           FROM audit_log WHERE action = $1
           ORDER BY created_at DESC LIMIT $2`
		args = []any{action, limit}
	} else {
		query = `SELECT id, user_id, user_email, action, target_type, target_id,
                  COALESCE(details::text, 'null'), created_at
           FROM audit_log ORDER BY created_at DESC LIMIT $1`
		args = []any{limit}
	}
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var detailsRaw string
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserEmail, &e.Action,
			&e.TargetType, &e.TargetID, &detailsRaw, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Details = json.RawMessage(detailsRaw)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
