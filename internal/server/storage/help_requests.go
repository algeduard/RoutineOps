package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ====== Обращения за помощью (help requests) ======
// «Сообщить о проблеме» в трее агента: текст + опциональный скриншот (JPEG).
// Создаёт gateway по SubmitHelpRequest, читает/закрывает веб через REST.

// HelpRequestRow — строка списка обращений. Скриншот в списке НЕ отдаём
// (bytea до 2МБ на строку раздул бы ответ), только флаг наличия — сам JPEG
// веб забирает отдельной ручкой GET /help-requests/{id}/screenshot.
type HelpRequestRow struct {
	ID             string     `json:"id"`
	DeviceID       string     `json:"device_id"`
	DeviceHostname string     `json:"device_hostname"`
	Reporter       string     `json:"reporter"`
	Message        string     `json:"message"`
	HasScreenshot  bool       `json:"has_screenshot"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	ReceivedAt     time.Time  `json:"received_at"`
	ClosedBy       *string    `json:"closed_by"`
	ClosedByEmail  string     `json:"closed_by_email"`
	ClosedAt       *time.Time `json:"closed_at"`
}

func (db *DB) CreateHelpRequest(ctx context.Context, deviceID, reporter, message string, screenshot []byte, createdAt time.Time) (string, error) {
	// NULLIF на пустой скриншот: NULL в колонке = «без скриншота», а не пустой blob.
	var id string
	err := db.pool.QueryRow(ctx, `
		INSERT INTO help_requests (device_id, reporter, message, screenshot, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, deviceID, reporter, message, nilIfEmpty(screenshot), createdAt).Scan(&id)
	return id, err
}

func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// LastHelpRequestAt — момент доставки последнего обращения устройства (для
// кулдауна в gateway). Нулевое время = обращений ещё не было.
func (db *DB) LastHelpRequestAt(ctx context.Context, deviceID string) (time.Time, error) {
	var t time.Time
	err := db.pool.QueryRow(ctx,
		`SELECT received_at FROM help_requests WHERE device_id = $1 ORDER BY received_at DESC LIMIT 1`,
		deviceID).Scan(&t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return t, nil
}

func (db *DB) ListHelpRequests(ctx context.Context, deviceID, statusFilter string) ([]HelpRequestRow, error) {
	// device_id::text — чтобы кривой UUID из query-string дал пустой список, а не
	// 22P02 → 500 (приём как у GetScript).
	rows, err := db.pool.Query(ctx, `
		SELECT r.id, r.device_id, COALESCE(d.hostname, ''), COALESCE(r.reporter, ''), r.message,
		       r.screenshot IS NOT NULL, r.status, r.created_at, r.received_at,
		       r.closed_by::text, COALESCE(u.email, ''), r.closed_at
		FROM help_requests r
		LEFT JOIN devices d ON d.id = r.device_id
		LEFT JOIN users u ON u.id = r.closed_by
		WHERE ($1 = '' OR r.device_id::text = $1)
		  AND ($2 = '' OR r.status = $2)
		  AND ($3::uuid IS NULL OR d.tenant_id = $3::uuid)   -- tenant-scope по устройству обращения
		ORDER BY r.received_at DESC
		LIMIT 200
	`, deviceID, statusFilter, scopeParam(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []HelpRequestRow
	for rows.Next() {
		var r HelpRequestRow
		if err := rows.Scan(&r.ID, &r.DeviceID, &r.DeviceHostname, &r.Reporter, &r.Message,
			&r.HasScreenshot, &r.Status, &r.CreatedAt, &r.ReceivedAt,
			&r.ClosedBy, &r.ClosedByEmail, &r.ClosedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetHelpRequestScreenshot возвращает (nil, nil), если обращения нет или оно без
// скриншота — обработчик отдаёт 404, не 500.
func (db *DB) GetHelpRequestScreenshot(ctx context.Context, id string) ([]byte, error) {
	var img []byte
	err := db.pool.QueryRow(ctx,
		`SELECT screenshot FROM help_requests WHERE id::text = $1
		   AND EXISTS (SELECT 1 FROM devices d WHERE d.id = help_requests.device_id
		                 AND ($2::uuid IS NULL OR d.tenant_id = $2::uuid))`,
		id, scopeParam(ctx)).Scan(&img)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return img, nil
}

// ErrHelpRequestNotFound — обращения нет или статус уже такой (UPDATE затронул 0 строк).
var ErrHelpRequestNotFound = errors.New("help request not found")

// SetHelpRequestStatus закрывает/переоткрывает обращение. При закрытии фиксирует
// кто и когда; при переоткрытии — очищает (обращение снова «новое»).
func (db *DB) SetHelpRequestStatus(ctx context.Context, id, status, userID string) error {
	var q string
	args := []any{id}
	// tenant-scope через устройство обращения (EXISTS-гард в WHERE). Номер параметра зависит
	// от ветки: 'closed' добавляет userID ($2), поэтому tenant = $3; 'new' → tenant = $2.
	switch status {
	case "closed":
		q = `UPDATE help_requests SET status = 'closed', closed_by = $2, closed_at = now()
		     WHERE id::text = $1 AND status <> 'closed'
		       AND EXISTS (SELECT 1 FROM devices d WHERE d.id = help_requests.device_id
		                     AND ($3::uuid IS NULL OR d.tenant_id = $3::uuid))`
		args = append(args, userID, scopeParam(ctx))
	case "new":
		q = `UPDATE help_requests SET status = 'new', closed_by = NULL, closed_at = NULL
		     WHERE id::text = $1 AND status <> 'new'
		       AND EXISTS (SELECT 1 FROM devices d WHERE d.id = help_requests.device_id
		                     AND ($2::uuid IS NULL OR d.tenant_id = $2::uuid))`
		args = append(args, scopeParam(ctx))
	default:
		return errors.New("status must be 'new' or 'closed'")
	}
	tag, err := db.pool.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrHelpRequestNotFound
	}
	return nil
}
