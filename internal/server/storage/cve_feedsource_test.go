package storage_test

import (
	"context"
	"testing"
)

// cve_feed_source — singleton: чистим перед проверкой дефолта, чтобы предыдущие тесты пакета
// (общая БД) не оставили строку и не сломали ожидание «строки ещё нет».
func TestCVEFeedSource(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := db.Pool().Exec(ctx, `TRUNCATE cve_feed_source`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Дефолт при отсутствии строки: интервал 24ч, auto_scan включён, сам источник выключен.
	cfg, err := db.GetCVEFeedSource(ctx)
	if err != nil {
		t.Fatalf("GetCVEFeedSource (default): %v", err)
	}
	if cfg.Enabled || cfg.URL != "" || cfg.SyncIntervalHours != 24 || !cfg.AutoScan || cfg.LastSyncedAt != nil {
		t.Fatalf("дефолт неверен: %+v", cfg)
	}

	// Сохранение и обратное чтение.
	url := "https://feeds.example/cve.json"
	if err := db.SetCVEFeedSource(ctx, url, 6, true, false); err != nil {
		t.Fatalf("SetCVEFeedSource: %v", err)
	}
	cfg, err = db.GetCVEFeedSource(ctx)
	if err != nil {
		t.Fatalf("GetCVEFeedSource: %v", err)
	}
	if cfg.URL != url || cfg.SyncIntervalHours != 6 || !cfg.Enabled || cfg.AutoScan {
		t.Fatalf("после Set: %+v", cfg)
	}
	if cfg.LastSyncedAt != nil {
		t.Fatalf("Set не должен трогать last_synced_at: %+v", cfg.LastSyncedAt)
	}

	// Фиксация итога синка: last_synced_at проставляется, last_status сохраняется, конфиг цел.
	if err := db.MarkCVEFeedSourceSynced(ctx, "ok: загружено записей 3"); err != nil {
		t.Fatalf("MarkCVEFeedSourceSynced: %v", err)
	}
	cfg, err = db.GetCVEFeedSource(ctx)
	if err != nil {
		t.Fatalf("GetCVEFeedSource (after mark): %v", err)
	}
	if cfg.LastSyncedAt == nil {
		t.Fatalf("last_synced_at должен быть проставлен после Mark")
	}
	if cfg.LastStatus != "ok: загружено записей 3" {
		t.Fatalf("last_status = %q", cfg.LastStatus)
	}
	// Mark НЕ должен обнулять ранее сохранённый конфиг (url/interval/enabled/auto_scan).
	if cfg.URL != url || cfg.SyncIntervalHours != 6 || !cfg.Enabled || cfg.AutoScan {
		t.Fatalf("Mark затёр конфиг: %+v", cfg)
	}

	// Апдейт статуса на ошибку двигает last_synced_at вперёд (время попытки, не только успеха).
	prev := *cfg.LastSyncedAt
	if err := db.MarkCVEFeedSourceSynced(ctx, "error: источник вернул 503"); err != nil {
		t.Fatalf("MarkCVEFeedSourceSynced (error): %v", err)
	}
	cfg, _ = db.GetCVEFeedSource(ctx)
	if cfg.LastStatus != "error: источник вернул 503" {
		t.Fatalf("last_status (error) = %q", cfg.LastStatus)
	}
	if cfg.LastSyncedAt.Before(prev) {
		t.Fatalf("last_synced_at ушёл назад: %v < %v", cfg.LastSyncedAt, prev)
	}
}
