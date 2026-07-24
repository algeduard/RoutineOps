package storage

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// AdminSoftwareDelta — дельта инвентаря ПО за сессию админ-прав (для REST-ответа).
type AdminSoftwareDelta struct {
	Added   []SoftwareItem `json:"added"`
	Removed []SoftwareItem `json:"removed"`
}

// SaveAdminSoftwareDelta сохраняет дельту ПО за сессию админ-прав, привязанную к
// заявке. Идемпотентно (ON CONFLICT DO NOTHING) — повторная доставка REVOKED-отчёта
// (at-least-once outbox) не дублирует строки. Пустые added/removed — no-op.
func (db *DB) SaveAdminSoftwareDelta(ctx context.Context, requestID string, added, removed []SoftwareItem) error {
	batch := &pgx.Batch{}
	const q = `INSERT INTO admin_access_software_delta (request_id, change_type, software_name, version)
	           VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`
	queue := func(changeType string, items []SoftwareItem) {
		for _, s := range items {
			if s.Name == "" {
				continue
			}
			batch.Queue(q, requestID, changeType, s.Name, s.Version)
		}
	}
	queue("added", added)
	queue("removed", removed)
	if batch.Len() == 0 {
		return nil
	}
	br := db.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return wrapFKViolation(err)
		}
	}
	return br.Close()
}

// GetAdminSoftwareDelta отдаёт дельту ПО заявки (added/removed), отсортированную по
// имени. Пустая дельта — нулевые срезы (JSON []).
func (db *DB) GetAdminSoftwareDelta(ctx context.Context, requestID string) (AdminSoftwareDelta, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT change_type, software_name, version
		 FROM admin_access_software_delta
		 WHERE request_id = $1
		   AND EXISTS (SELECT 1 FROM admin_access_requests ar JOIN devices d ON d.id = ar.device_id
		                 WHERE ar.id = $1 AND ($2::uuid IS NULL OR d.tenant_id = $2::uuid))
		 ORDER BY software_name`, requestID, scopeParam(ctx))
	if err != nil {
		return AdminSoftwareDelta{}, err
	}
	defer rows.Close()

	d := AdminSoftwareDelta{Added: []SoftwareItem{}, Removed: []SoftwareItem{}}
	for rows.Next() {
		var changeType string
		var s SoftwareItem
		if err := rows.Scan(&changeType, &s.Name, &s.Version); err != nil {
			return AdminSoftwareDelta{}, err
		}
		if changeType == "added" {
			d.Added = append(d.Added, s)
		} else {
			d.Removed = append(d.Removed, s)
		}
	}
	return d, rows.Err()
}
