package storage

import (
	"context"
	"time"
)

// Экспортируемые отчёты (enterprise-фича FeatureReports) агрегируют УЖЕ существующие
// данные — нового сбора от агента нет и миграции не требуется. Каждый отчёт стримит
// строки построчно через emit-колбэк: экспорт большого парка/журнала не материализует
// весь набор в память (в отличие от Scan-в-слайс), а сразу пишется в CSV/HTML вызывающим.
// emit возвращает ошибку записи вниз по стеку — она прерывает обход rows.
//
// Строки уже стрингифицированы на стороне SQL (COALESCE от NULL, to_char для меток
// времени в UTC-инстанте) — так формат отчёта не зависит от tz процесса, а вызывающему
// (reports_handler) остаётся только квотация CSV/экранирование HTML. Метки времени —
// ISO-8601 UTC (…Z), совпадает с конвенцией audit/SIEM-экспорта.

// StreamDeviceReport — парк устройств: статус, ОС, шифрование, владелец, последняя
// активность. owner_email через LEFT JOIN users (устройство без владельца → пустая строка).
func (db *DB) StreamDeviceReport(ctx context.Context, emit func([]string) error) error {
	rows, err := db.pool.Query(ctx, `
		SELECT d.hostname, d.os, COALESCE(d.os_version, ''), d.status,
		       COALESCE(d.disk_encryption, ''),
		       COALESCE(u.email, ''),
		       COALESCE(to_char(d.last_seen_at AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
		       COALESCE(d.agent_version, ''),
		       COALESCE(d.serial_number, '')
		FROM devices d
		LEFT JOIN users u ON u.id = d.owner_id
		ORDER BY lower(d.hostname)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hostname, os, osVersion, status, enc, owner, lastSeen, agentVersion, serial string
		if err := rows.Scan(&hostname, &os, &osVersion, &status, &enc, &owner, &lastSeen, &agentVersion, &serial); err != nil {
			return err
		}
		if err := emit([]string{hostname, os, osVersion, status, enc, owner, lastSeen, agentVersion, serial}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// StreamSoftwareInventoryReport — сводка по ПО в парке: (имя, версия) и на скольких
// устройствах установлено. Агрегат в SQL (GROUP BY), а не разворот всего инвентаря —
// компактный, пригодный для аудита лицензий/распространённости софта отчёт.
func (db *DB) StreamSoftwareInventoryReport(ctx context.Context, emit func([]string) error) error {
	rows, err := db.pool.Query(ctx, `
		SELECT software_name, COALESCE(version, ''), count(DISTINCT device_id)::text
		FROM device_software
		GROUP BY software_name, version
		ORDER BY count(DISTINCT device_id) DESC, lower(software_name), version`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, version, deviceCount string
		if err := rows.Scan(&name, &version, &deviceCount); err != nil {
			return err
		}
		if err := emit([]string{name, version, deviceCount}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// StreamAuditReport — журнал аудита за полуинтервал [from, to). Границы задаёт вызывающий
// (reports_handler разбирает ?from=&to=); отсутствующая граница подставляется сентинелом
// (0001-01-01 / 9999-12-31), поэтому фильтр всегда корректный диапазон. Порядок — по
// времени возрастания (хронология события за событием).
func (db *DB) StreamAuditReport(ctx context.Context, from, to time.Time, emit func([]string) error) error {
	rows, err := db.pool.Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       user_email, action, target_type, target_id,
		       COALESCE(details::text, '')
		FROM audit_log
		WHERE created_at >= $1 AND created_at < $2
		ORDER BY created_at`, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ts, email, action, targetType, targetID, details string
		if err := rows.Scan(&ts, &email, &action, &targetType, &targetID, &details); err != nil {
			return err
		}
		if err := emit([]string{ts, email, action, targetType, targetID, details}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// StreamAlertsReport — журнал алертов: устройство, тип, критичность, детали, когда создан
// и принят (пусто = ещё не принят). LEFT JOIN devices — алерт живёт даже если устройство
// уже удалено (каскад его снёс бы, но на всякий случай не роняем строку из-за NULL).
func (db *DB) StreamAlertsReport(ctx context.Context, emit func([]string) error) error {
	rows, err := db.pool.Query(ctx, `
		SELECT COALESCE(d.hostname, ''), a.alert_type, a.severity, COALESCE(a.details, ''),
		       to_char(a.created_at AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       COALESCE(to_char(a.acknowledged_at AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
		FROM alerts a
		LEFT JOIN devices d ON d.id = a.device_id
		ORDER BY a.created_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hostname, alertType, severity, details, createdAt, ackAt string
		if err := rows.Scan(&hostname, &alertType, &severity, &details, &createdAt, &ackAt); err != nil {
			return err
		}
		if err := emit([]string{hostname, alertType, severity, details, createdAt, ackAt}); err != nil {
			return err
		}
	}
	return rows.Err()
}
