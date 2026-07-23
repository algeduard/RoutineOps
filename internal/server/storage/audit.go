package storage

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// auditHMACKey — ключ подписи записей аудита (ROUTINEOPS_AUDIT_HMAC_KEY). Пусто = подпись
// выключена (tamper-evidence не активна). Ключ живёт в env деплоя, НЕ в БД: атакующий с
// доступом только к БД не может подделать подпись.
func auditHMACKey() string { return strings.TrimSpace(os.Getenv("ROUTINEOPS_AUDIT_HMAC_KEY")) }

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

// WriteAuditLog пишет запись аудита. Если задан ROUTINEOPS_AUDIT_HMAC_KEY — включает её в
// КЕЙД ХЕШ-ЦЕПОЧКУ: entry_hmac = HMAC(key, prev_hmac || canonical), где prev_hmac — голова
// цепочки (audit_chain.last_hash); затем голова двигается на новую строку. Запись
// сериализуется FOR UPDATE головы, поэтому порядок цепочки = порядок seq. Без ключа —
// обычная вставка (created_at = now(), tz-корректный instant), цепочка не ведётся.
//
// created_at в подписи берётся как UTC-стенка (AT TIME ZONE 'utc') — tz-НЕзависимо, так же
// как в VerifyAuditIntegrity; иначе на не-UTC/DST-сессии проверка ложно метила бы весь
// журнал. details — канонический ($6::jsonb)::text (детерминирован на записи и чтении).
// Разделитель E'\x1f' валиден в TEXT (в отличие от NUL); JSON экранирует контрол-символы.
func (db *DB) WriteAuditLog(ctx context.Context, userID, userEmail, action, targetType, targetID string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var uid *string
	if userID != "" {
		uid = &userID
	}
	key := auditHMACKey()
	if key == "" {
		_, err = db.pool.Exec(ctx, `
  INSERT INTO audit_log (user_id, user_email, action, target_type, target_id, details, created_at)
  VALUES ($1, $2, $3, $4, $5, $6::jsonb, now())
 `, uid, userEmail, action, targetType, targetID, string(raw))
		return err
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// FOR UPDATE головы сериализует подписанные записи: следующая цепляется на предыдущую,
	// seq (взятый ниже в INSERT) присваивается уже после блокировки → порядок совпадает.
	var prev string
	if err := tx.QueryRow(ctx, `SELECT last_hash FROM audit_chain WHERE id LIMIT 1 FOR UPDATE`).Scan(&prev); err != nil {
		return err
	}
	var newHash string
	var seq int64
	err = tx.QueryRow(ctx, `
  WITH t AS (SELECT now() AS ca)
  INSERT INTO audit_log (user_id, user_email, action, target_type, target_id, details, created_at, entry_hmac)
  SELECT $1, $2, $3, $4, $5, $6::jsonb, t.ca,
         encode(hmac($8 || E'\x1f' || $2 || E'\x1f' || $3 || E'\x1f' || $4 || E'\x1f' || $5
                     || E'\x1f' || ($6::jsonb)::text || E'\x1f'
                     || to_char(t.ca AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US'),
                     $7, 'sha256'), 'hex')
  FROM t
  RETURNING seq, entry_hmac
 `, uid, userEmail, action, targetType, targetID, string(raw), key, prev).Scan(&seq, &newHash)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE audit_chain SET last_hash = $1, last_seq = $2 WHERE id`, newHash, seq); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AuditIntegrity — результат проверки целостности журнала аудита (tamper-evidence).
type AuditIntegrity struct {
	Configured       bool  `json:"configured"`         // задан ли ключ подписи
	Checked          int   `json:"checked"`            // сколько подписанных строк проверено
	Tampered         bool  `json:"tampered"`           // цепочка нарушена (модификация/удаление/вставка/replay)
	FirstTamperedSeq int64 `json:"first_tampered_seq"` // seq первой битой строки (0 при усечении хвоста)
	TailTruncated    bool  `json:"tail_truncated"`     // хвост усечён (голова не сходится); ловит лишь наивное удаление — см. VerifyAuditIntegrity
}

// VerifyAuditIntegrity проверяет кейд хеш-цепочку. Для каждой подписанной строки (по seq)
// пересчитывает HMAC(key, prev_stored_hmac || canonical) и сравнивает с сохранённым:
// расхождение = модификация/удаление/вставка/replay на этой позиции (удалённая строка
// сдвигает prev у следующей, обнулённая — выпадает из цепочки → следующая не сходится).
// Самую раннюю СУЩЕСТВУЮЩУЮ строку проверить нельзя (её предшественник мог быть срезан
// ретеншеном) — она якорь (rn=1 пропускается). Отдельно сверяет last_hash головы с
// фактической последней строкой: не сойдётся → усечён хвост.
//
// Границы: модификация/удаление середины/вставка/replay требуют ПЕРЕсчёта звеньев цепочки,
// а ключ в env — их не подделать имея только БД. НО усечение хвоста ключа не требует:
// решительный атакующий удалит хвост И перепишет audit_chain.last_hash на hmac новой
// последней строки → TailTruncated НЕ сработает. Полная защита от усечения — внешний якорь
// (SIEM-экспорт вывозит строки до удаления). Голова ловит наивное/случайное усечение.
func (db *DB) VerifyAuditIntegrity(ctx context.Context, _ int) (AuditIntegrity, error) {
	key := auditHMACKey()
	if key == "" {
		return AuditIntegrity{Configured: false}, nil
	}
	r := AuditIntegrity{Configured: true}
	var firstBroken *int64
	var lastHashActual, lastHashStored *string
	err := db.pool.QueryRow(ctx, `
	  WITH w AS (
	    SELECT seq, entry_hmac,
	           row_number() OVER (ORDER BY seq) AS rn,
	           encode(hmac(COALESCE(lag(entry_hmac) OVER (ORDER BY seq), '') || E'\x1f'
	             || user_email || E'\x1f' || action || E'\x1f' || target_type || E'\x1f' || target_id
	             || E'\x1f' || COALESCE(details::text,'null') || E'\x1f'
	             || to_char(created_at AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US'),
	             $1, 'sha256'),'hex') AS expected
	    FROM audit_log WHERE entry_hmac <> ''
	  )
	  SELECT count(*),
	         min(seq) FILTER (WHERE rn > 1 AND entry_hmac <> expected),
	         (SELECT entry_hmac FROM w ORDER BY seq DESC LIMIT 1),
	         (SELECT last_hash FROM audit_chain WHERE id)
	  FROM w
	`, key).Scan(&r.Checked, &firstBroken, &lastHashActual, &lastHashStored)
	if err != nil {
		return AuditIntegrity{}, err
	}
	if firstBroken != nil {
		r.Tampered = true
		r.FirstTamperedSeq = *firstBroken
	}
	// Якорь-хвост: голова цепочки обязана указывать на фактическую последнюю строку.
	if r.Checked > 0 && lastHashStored != nil && (lastHashActual == nil || *lastHashStored != *lastHashActual) {
		r.Tampered = true
		r.TailTruncated = true
	}
	return r, nil
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
