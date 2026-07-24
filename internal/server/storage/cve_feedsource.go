package storage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CVEFeedSource — конфиг внешнего источника CVE-фида (singleton, таблица cve_feed_source из
// миграции 055). URL указывает на выгрузку в том же JSON-формате, что принимает POST /cve/feed
// (см. CVEFeedEntry). Фоновый синкер (internal/server/cvesync) по расписанию скачивает фид,
// ЗАМЕНЯЕТ его (LoadCVEFeed) и, если AutoScan, пересобирает находки (ScanCVE).
//
// LastSyncedAt — время последней ПОПЫТКИ синка (успех ИЛИ ошибка): так расписание двигается
// даже когда источник недоступен, и синкер не молотит один и тот же битый URL каждый тик.
// LastStatus описывает исход ('ok: ...' / 'error: ...').
type CVEFeedSource struct {
	URL               string     `json:"url"`
	SyncIntervalHours int        `json:"sync_interval_hours"`
	Enabled           bool       `json:"enabled"`
	AutoScan          bool       `json:"auto_scan"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
	LastStatus        string     `json:"last_status"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// GetCVEFeedSource отдаёт конфиг источника. Если строки ещё нет — вменяемые дефолты
// (интервал 24ч, auto_scan включён, сам источник выключен), а не нулевой интервал.
func (db *DB) GetCVEFeedSource(ctx context.Context) (CVEFeedSource, error) {
	var c CVEFeedSource
	err := db.pool.QueryRow(ctx, `
		SELECT url, sync_interval_hours, enabled, auto_scan, last_synced_at, last_status, updated_at
		FROM cve_feed_source WHERE id = true`).
		Scan(&c.URL, &c.SyncIntervalHours, &c.Enabled, &c.AutoScan, &c.LastSyncedAt, &c.LastStatus, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return CVEFeedSource{SyncIntervalHours: 24, AutoScan: true}, nil
		}
		return CVEFeedSource{}, err
	}
	return c, nil
}

// SetCVEFeedSource апсертит singleton-конфиг источника. Статус синка (last_synced_at/last_status)
// НЕ трогает — им владеет MarkCVEFeedSourceSynced (иначе правка URL сбрасывала бы историю синка).
func (db *DB) SetCVEFeedSource(ctx context.Context, url string, syncIntervalHours int, enabled, autoScan bool) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO cve_feed_source (id, url, sync_interval_hours, enabled, auto_scan, updated_at)
		VALUES (true, $1, $2, $3, $4, now())
		ON CONFLICT (id) DO UPDATE SET
			url = $1,
			sync_interval_hours = $2,
			enabled = $3,
			auto_scan = $4,
			updated_at = now()`,
		url, syncIntervalHours, enabled, autoScan)
	return err
}

// MarkCVEFeedSourceSynced фиксирует итог попытки синка: last_synced_at = now() (время попытки,
// не только успеха — см. CVEFeedSource) и last_status. Апсерт, чтобы форс-синк по ещё не
// сохранённому конфигу тоже оставил след, а не потерялся в UPDATE без строки.
func (db *DB) MarkCVEFeedSourceSynced(ctx context.Context, status string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO cve_feed_source (id, last_synced_at, last_status)
		VALUES (true, now(), $1)
		ON CONFLICT (id) DO UPDATE SET
			last_synced_at = now(),
			last_status = $1`,
		status)
	return err
}
