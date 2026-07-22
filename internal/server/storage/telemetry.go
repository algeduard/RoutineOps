package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Телеметрия устройств: метрики ресурсов (time-series) и агрегаты активности
// приложений/времени за ПК. Методы принимают deviceID (uuid-строка), как остальное
// хранилище. Скоуп по устройству обеспечивает вызывающий (gateway — по mTLS-серту,
// REST — по URL-параметру после jwt).

// ── Метрики ресурсов ─────────────────────────────────────────────────────────

// ResourceSampleInput — один сэмпл ресурсов для вставки.
type ResourceSampleInput struct {
	Ts            time.Time
	CPUPercent    float64
	MemUsedBytes  int64
	MemTotalBytes int64
	DiskPercent   float64
	NetRxBps      int64
	NetTxBps      int64
}

// ResourceMetricRow — точка истории метрик (или последний сэмпл) для REST.
type ResourceMetricRow struct {
	Ts            time.Time `json:"ts"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemUsedBytes  int64     `json:"mem_used_bytes"`
	MemTotalBytes int64     `json:"mem_total_bytes"`
	DiskPercent   float64   `json:"disk_percent"`
	NetRxBps      int64     `json:"net_rx_bps"`
	NetTxBps      int64     `json:"net_tx_bps"`
}

// InsertResourceMetrics вставляет батч сэмплов за один round-trip (pgx.Batch).
// device_id передаётся строкой — Postgres assignment-кастит text→uuid (как всюду
// в хранилище). Пустой батч — no-op.
func (db *DB) InsertResourceMetrics(ctx context.Context, deviceID string, samples []ResourceSampleInput) error {
	if len(samples) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, s := range samples {
		batch.Queue(`
			INSERT INTO device_metrics
			  (device_id, ts, cpu_percent, mem_used_bytes, mem_total_bytes, disk_percent, net_rx_bps, net_tx_bps)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			deviceID, s.Ts, s.CPUPercent, s.MemUsedBytes, s.MemTotalBytes, s.DiskPercent, s.NetRxBps, s.NetTxBps)
	}
	br := db.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range samples {
		if _, err := br.Exec(); err != nil {
			return wrapFKViolation(err)
		}
	}
	return br.Close()
}

// GetResourceMetrics возвращает историю метрик с момента since, ДАУНСЭМПЛЕННУЮ в
// корзины по bucketSeconds (усреднение внутри корзины). Даунсэмплинг на стороне БД
// держит ответ компактным для SVG-графика независимо от частоты сэмплирования
// (24ч по 15с — 5760 сырых точек). Корзинирование по epoch (floor(epoch/bucket)*
// bucket) не зависит от версии Postgres (без date_bin).
func (db *DB) GetResourceMetrics(ctx context.Context, deviceID string, since time.Time, bucketSeconds int) ([]ResourceMetricRow, error) {
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	rows, err := db.pool.Query(ctx, `
		SELECT to_timestamp(floor(extract(epoch FROM ts) / $2) * $2) AS bucket,
		       avg(cpu_percent)              AS cpu,
		       avg(mem_used_bytes)::bigint   AS mem_used,
		       max(mem_total_bytes)          AS mem_total,
		       avg(disk_percent)             AS disk,
		       avg(net_rx_bps)::bigint       AS rx,
		       avg(net_tx_bps)::bigint       AS tx
		FROM device_metrics
		WHERE device_id = $1 AND ts >= $3
		GROUP BY bucket
		ORDER BY bucket`, deviceID, bucketSeconds, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ResourceMetricRow
	for rows.Next() {
		var r ResourceMetricRow
		var memTotal *int64
		if err := rows.Scan(&r.Ts, &r.CPUPercent, &r.MemUsedBytes, &memTotal, &r.DiskPercent, &r.NetRxBps, &r.NetTxBps); err != nil {
			return nil, err
		}
		if memTotal != nil {
			r.MemTotalBytes = *memTotal
		}
		r.CPUPercent = round2(r.CPUPercent)
		r.DiskPercent = round2(r.DiskPercent)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetLatestResourceMetric возвращает самый свежий сэмпл (для живого значения в
// карточке). nil, если метрик ещё нет.
func (db *DB) GetLatestResourceMetric(ctx context.Context, deviceID string) (*ResourceMetricRow, error) {
	var r ResourceMetricRow
	err := db.pool.QueryRow(ctx, `
		SELECT ts,
		       COALESCE(cpu_percent, 0), COALESCE(mem_used_bytes, 0), COALESCE(mem_total_bytes, 0),
		       COALESCE(disk_percent, 0), COALESCE(net_rx_bps, 0), COALESCE(net_tx_bps, 0)
		FROM device_metrics
		WHERE device_id = $1
		ORDER BY ts DESC
		LIMIT 1`, deviceID).
		Scan(&r.Ts, &r.CPUPercent, &r.MemUsedBytes, &r.MemTotalBytes, &r.DiskPercent, &r.NetRxBps, &r.NetTxBps)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// ── App-usage (аналитика активности приложений) ──────────────────────────────

// AppUsageInput / DailyActivityInput — ДЕЛЬТЫ от агента (аккумулируются в БД).
type AppUsageInput struct {
	Day               string // ISO "2006-01-02"
	AppName           string
	WindowTitle       string // "" когда capture_window_titles выключен
	ForegroundSeconds int64
}
type DailyActivityInput struct {
	Day           string
	ActiveSeconds int64
	IdleSeconds   int64
}

// AppUsageRow / DailyActivityRow — агрегаты для REST-отчёта.
type AppUsageRow struct {
	Day               string `json:"day"`
	AppName           string `json:"app_name"`
	WindowTitle       string `json:"window_title"`
	ForegroundSeconds int64  `json:"foreground_seconds"`
}
type DailyActivityRow struct {
	Day           string `json:"day"`
	ActiveSeconds int64  `json:"active_seconds"`
	IdleSeconds   int64  `json:"idle_seconds"`
}

const dayLayout = "2006-01-02"

// UpsertAppUsage аккумулирует дельты использования приложений: existing + delta.
// Некорректный формат day или пустое имя приложения — запись пропускается (не валит
// весь батч). Отрицательные/нулевые дельты игнорируются.
func (db *DB) UpsertAppUsage(ctx context.Context, deviceID string, entries []AppUsageInput) error {
	batch := &pgx.Batch{}
	queued := 0
	for _, e := range entries {
		day, err := time.Parse(dayLayout, e.Day)
		if err != nil || e.AppName == "" || e.ForegroundSeconds <= 0 {
			continue
		}
		batch.Queue(`
			INSERT INTO device_app_usage (device_id, day, app_name, window_title, foreground_seconds, updated_at)
			VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT (device_id, day, app_name, window_title)
			DO UPDATE SET foreground_seconds = device_app_usage.foreground_seconds + EXCLUDED.foreground_seconds,
			              updated_at = now()`,
			deviceID, day, e.AppName, e.WindowTitle, e.ForegroundSeconds)
		queued++
	}
	if queued == 0 {
		return nil
	}
	br := db.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < queued; i++ {
		if _, err := br.Exec(); err != nil {
			return wrapFKViolation(err)
		}
	}
	return br.Close()
}

// UpsertDailyActivity аккумулирует дельты активного/простойного времени за день.
func (db *DB) UpsertDailyActivity(ctx context.Context, deviceID string, days []DailyActivityInput) error {
	batch := &pgx.Batch{}
	queued := 0
	for _, d := range days {
		day, err := time.Parse(dayLayout, d.Day)
		if err != nil || (d.ActiveSeconds <= 0 && d.IdleSeconds <= 0) {
			continue
		}
		batch.Queue(`
			INSERT INTO device_activity_daily (device_id, day, active_seconds, idle_seconds, updated_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (device_id, day)
			DO UPDATE SET active_seconds = device_activity_daily.active_seconds + EXCLUDED.active_seconds,
			              idle_seconds   = device_activity_daily.idle_seconds   + EXCLUDED.idle_seconds,
			              updated_at = now()`,
			deviceID, day, d.ActiveSeconds, d.IdleSeconds)
		queued++
	}
	if queued == 0 {
		return nil
	}
	br := db.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < queued; i++ {
		if _, err := br.Exec(); err != nil {
			return wrapFKViolation(err)
		}
	}
	return br.Close()
}

// GetAppUsage возвращает использование приложений (топ по времени) и активность по
// дням с даты since (включительно).
func (db *DB) GetAppUsage(ctx context.Context, deviceID string, since time.Time) ([]AppUsageRow, []DailyActivityRow, error) {
	appRows, err := db.pool.Query(ctx, `
		SELECT day, app_name, window_title, foreground_seconds
		FROM device_app_usage
		WHERE device_id = $1 AND day >= $2::date
		ORDER BY foreground_seconds DESC, app_name`, deviceID, since)
	if err != nil {
		return nil, nil, err
	}
	defer appRows.Close()
	var apps []AppUsageRow
	for appRows.Next() {
		var a AppUsageRow
		var day time.Time
		if err := appRows.Scan(&day, &a.AppName, &a.WindowTitle, &a.ForegroundSeconds); err != nil {
			return nil, nil, err
		}
		a.Day = day.Format(dayLayout)
		apps = append(apps, a)
	}
	if err := appRows.Err(); err != nil {
		return nil, nil, err
	}

	dayRows, err := db.pool.Query(ctx, `
		SELECT day, active_seconds, idle_seconds
		FROM device_activity_daily
		WHERE device_id = $1 AND day >= $2::date
		ORDER BY day`, deviceID, since)
	if err != nil {
		return nil, nil, err
	}
	defer dayRows.Close()
	var days []DailyActivityRow
	for dayRows.Next() {
		var d DailyActivityRow
		var day time.Time
		if err := dayRows.Scan(&day, &d.ActiveSeconds, &d.IdleSeconds); err != nil {
			return nil, nil, err
		}
		d.Day = day.Format(dayLayout)
		days = append(days, d)
	}
	return apps, days, dayRows.Err()
}

// ── Privacy-тумблер сбора аналитики приложений ───────────────────────────────

// GetAppUsageEnabledByFingerprint отдаёт флаг сбора для устройства по mTLS-серту
// (для gateway.FetchTelemetryConfig и гейта ReportAppUsage). Неизвестный серт →
// false (сбор выключен), а не ошибка.
func (db *DB) GetAppUsageEnabledByFingerprint(ctx context.Context, fingerprint string) (bool, error) {
	var enabled bool
	err := db.pool.QueryRow(ctx,
		`SELECT app_usage_enabled FROM devices WHERE certificate_fingerprint = $1`, fingerprint).
		Scan(&enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return enabled, nil
}

// GetAppUsageEnabled отдаёт флаг сбора по deviceID (для REST GET конфига).
// found=false, если устройства нет.
func (db *DB) GetAppUsageEnabled(ctx context.Context, deviceID string) (enabled, found bool, err error) {
	err = db.pool.QueryRow(ctx,
		`SELECT app_usage_enabled FROM devices WHERE id = $1`, deviceID).Scan(&enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, nil
		}
		return false, false, err
	}
	return enabled, true, nil
}

// SetAppUsageEnabled переключает сбор для устройства. found=false, если устройства
// нет (0 обновлённых строк).
func (db *DB) SetAppUsageEnabled(ctx context.Context, deviceID string, enabled bool) (found bool, err error) {
	tag, err := db.pool.Exec(ctx,
		`UPDATE devices SET app_usage_enabled = $2 WHERE id = $1`, deviceID, enabled)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ── Privacy-тумблер сбора заголовков окон (отдельный, строже app_usage) ───────

// GetCaptureWindowTitlesByFingerprint отдаёт флаг сбора заголовков окон по
// mTLS-серту (FetchTelemetryConfig + серверный гейт ReportAppUsage). Неизвестный
// серт → false.
func (db *DB) GetCaptureWindowTitlesByFingerprint(ctx context.Context, fingerprint string) (bool, error) {
	var enabled bool
	err := db.pool.QueryRow(ctx,
		`SELECT capture_window_titles FROM devices WHERE certificate_fingerprint = $1`, fingerprint).
		Scan(&enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return enabled, nil
}

// GetCaptureWindowTitles отдаёт флаг по deviceID (REST GET конфига). found=false,
// если устройства нет.
func (db *DB) GetCaptureWindowTitles(ctx context.Context, deviceID string) (enabled, found bool, err error) {
	err = db.pool.QueryRow(ctx,
		`SELECT capture_window_titles FROM devices WHERE id = $1`, deviceID).Scan(&enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, nil
		}
		return false, false, err
	}
	return enabled, true, nil
}

// SetCaptureWindowTitles переключает сбор заголовков окон. found=false, если
// устройства нет.
func (db *DB) SetCaptureWindowTitles(ctx context.Context, deviceID string, enabled bool) (found bool, err error) {
	tag, err := db.pool.Exec(ctx,
		`UPDATE devices SET capture_window_titles = $2 WHERE id = $1`, deviceID, enabled)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
