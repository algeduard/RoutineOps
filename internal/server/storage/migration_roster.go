package storage

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// MigrationRosterRow — одна строка импортируемого ростера (то, что пришло из CSV).
type MigrationRosterRow struct {
	Hostname     string `json:"hostname"`
	SerialNumber string `json:"serial_number"`
	AssignedUser string `json:"assigned_user"`
	AssetTag     string `json:"asset_tag"`
	GroupHint    string `json:"group_hint"`
	Notes        string `json:"notes"`
}

// MigrationRosterEntry — строка ростера ВМЕСТЕ с результатом матча на чтении. Пустой
// MatchedDeviceID = ожидаемое устройство ещё не заехало в парк (не сматчилось ни по
// serial, ни по hostname).
type MigrationRosterEntry struct {
	ID           string    `json:"id"`
	BatchLabel   string    `json:"batch_label"`
	Hostname     string    `json:"hostname"`
	SerialNumber string    `json:"serial_number"`
	AssignedUser string    `json:"assigned_user"`
	AssetTag     string    `json:"asset_tag"`
	GroupHint    string    `json:"group_hint"`
	Notes        string    `json:"notes"`
	SourceMDM    string    `json:"source_mdm"`
	ImportedAt   time.Time `json:"imported_at"`
	ImportedBy   string    `json:"imported_by"`

	MatchedDeviceID string     `json:"matched_device_id"`
	MatchedStatus   string     `json:"matched_status"`
	MatchedLastSeen *time.Time `json:"matched_last_seen"`
}

// matchJoin — общий LATERAL для матча ростер→устройство. Сильный ключ — serial (админ
// сверяет его с железом; hostname агент называет о себе сам и может соврать), поэтому
// hostname включается ТОЛЬКО когда серийника в строке ростера нет. Среди подходящих
// устройств предпочитаем живое (active) и позже заэнролленное — на случай реенролла.
//
// lower(d.serial_number) БЕЗ COALESCE — намеренно: у устройства с NULL-серийником
// lower(NULL)=NULL, а NULL не равен непустому r.serial_number, так что оно и не сматчится;
// зато голое выражение совпадает с функциональным индексом idx_devices_serial_lower
// (миграция 037) — с COALESCE планировщик индекс бы не взял и делал seq-scan парка.
const matchJoin = `
	LEFT JOIN LATERAL (
	  SELECT id, status, last_seen_at FROM devices d
	  WHERE (r.serial_number <> '' AND lower(d.serial_number) = lower(r.serial_number))
	     OR (r.serial_number = '' AND r.hostname <> '' AND lower(d.hostname) = lower(r.hostname))
	  ORDER BY (d.status = 'active') DESC, d.enrolled_at DESC NULLS LAST
	  LIMIT 1
	) d ON true`

// ImportMigrationRoster заливает партию ожидаемых устройств. Идемпотентно
// (ON CONFLICT DO NOTHING по уникальному индексу identity) — повторная заливка того же
// CSV не задваивает строки. Строки, где и hostname, и serial пусты, отбрасываются: такую
// машину в парке всё равно не сматчить. Возвращает число реально вставленных строк.
func (db *DB) ImportMigrationRoster(ctx context.Context, batchLabel, sourceMDM, importedBy string, rows []MigrationRosterRow) (int, error) {
	batchLabel = strings.TrimSpace(batchLabel)
	sourceMDM = strings.TrimSpace(sourceMDM)

	batch := &pgx.Batch{}
	const q = `INSERT INTO device_migration_roster
	    (batch_label, hostname, serial_number, assigned_user, asset_tag, group_hint, notes, source_mdm, imported_by)
	    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`
	for _, row := range rows {
		hostname := strings.TrimSpace(row.Hostname)
		serial := strings.TrimSpace(row.SerialNumber)
		if hostname == "" && serial == "" {
			continue // нечего матчить — не заводим мусорную строку
		}
		batch.Queue(q, batchLabel, hostname, serial,
			strings.TrimSpace(row.AssignedUser), strings.TrimSpace(row.AssetTag),
			strings.TrimSpace(row.GroupHint), strings.TrimSpace(row.Notes), sourceMDM, importedBy)
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	br := db.pool.SendBatch(ctx, batch)
	defer br.Close()
	inserted := 0
	for i := 0; i < batch.Len(); i++ {
		ct, err := br.Exec()
		if err != nil {
			return inserted, err
		}
		inserted += int(ct.RowsAffected()) // 0 при конфликте (уже была), 1 при вставке
	}
	return inserted, br.Close()
}

// ListMigrationRoster отдаёт весь ростер с матчем на чтении. Ещё не приехавшие
// (MatchedDeviceID == "") идут первыми — это и есть список «кого добить»: пока их нет,
// миграция не завершена.
func (db *DB) ListMigrationRoster(ctx context.Context) ([]MigrationRosterEntry, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT r.id, r.batch_label, r.hostname, r.serial_number, r.assigned_user, r.asset_tag,
		       r.group_hint, r.notes, r.source_mdm, r.imported_at, r.imported_by,
		       COALESCE(d.id::text, ''), COALESCE(d.status, ''), d.last_seen_at
		FROM device_migration_roster r`+matchJoin+`
		ORDER BY (d.id IS NOT NULL), lower(r.hostname), lower(r.serial_number)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MigrationRosterEntry{}
	for rows.Next() {
		var e MigrationRosterEntry
		if err := rows.Scan(&e.ID, &e.BatchLabel, &e.Hostname, &e.SerialNumber, &e.AssignedUser,
			&e.AssetTag, &e.GroupHint, &e.Notes, &e.SourceMDM, &e.ImportedAt, &e.ImportedBy,
			&e.MatchedDeviceID, &e.MatchedStatus, &e.MatchedLastSeen); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MigrationRosterForDevice ищет строку ростера, соответствующую конкретному устройству
// (обратный матч для карточки устройства). nil, nil — устройство не из импортированного
// парка. Матч тот же: сильный ключ serial, запасной hostname.
//
// ⚠ hostname не уникален среди устройств (identity парка — сертификат, не hostname). Если
// в парке несколько машин с одним hostname, а строка ростера безсерийная, она сматчится
// к КАЖДОЙ из них (обе карточки покажут «импортировано из MDM»), тогда как в списке
// ListMigrationRoster та же строка привязана к ОДНОЙ (LIMIT 1). Расхождение справочное и
// присуще безсерийному матчу; для сильного (serial) ключа его нет.
func (db *DB) MigrationRosterForDevice(ctx context.Context, deviceID string) (*MigrationRosterEntry, error) {
	var e MigrationRosterEntry
	err := db.pool.QueryRow(ctx, `
		SELECT r.id, r.batch_label, r.hostname, r.serial_number, r.assigned_user, r.asset_tag,
		       r.group_hint, r.notes, r.source_mdm, r.imported_at, r.imported_by
		FROM device_migration_roster r
		JOIN devices d ON d.id = $1
		WHERE (r.serial_number <> '' AND lower(r.serial_number) = lower(d.serial_number))
		   OR (r.serial_number = '' AND r.hostname <> '' AND lower(r.hostname) = lower(d.hostname))
		ORDER BY (r.serial_number <> '') DESC, r.imported_at DESC
		LIMIT 1`, deviceID).Scan(&e.ID, &e.BatchLabel, &e.Hostname, &e.SerialNumber, &e.AssignedUser,
		&e.AssetTag, &e.GroupHint, &e.Notes, &e.SourceMDM, &e.ImportedAt, &e.ImportedBy)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	e.MatchedDeviceID = deviceID // раз строку нашли по этому устройству — оно и есть матч
	return &e, nil
}

// DeleteMigrationRoster чистит ростер. all == true сносит ВЕСЬ ростер; иначе удаляется одна
// партия по точному batchLabel (в т.ч. безымянная — пустая строка). Возвращает число
// удалённых строк. Разведение all и batchLabel намеренное: раньше пустой batchLabel
// означал «снести всё», из-за чего безымянную партию нельзя было удалить прицельно, а
// случайный ?batch= сносил весь ростер.
func (db *DB) DeleteMigrationRoster(ctx context.Context, batchLabel string, all bool) (int64, error) {
	q := `DELETE FROM device_migration_roster WHERE batch_label = $1`
	args := []any{strings.TrimSpace(batchLabel)}
	if all {
		q = `DELETE FROM device_migration_roster`
		args = nil
	}
	ct, err := db.pool.Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
