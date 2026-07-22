package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// findAppRow ищет строку app-usage по (app, title, url).
func findAppRow(rows []storage.AppUsageRow, app, title, url string) *storage.AppUsageRow {
	for i := range rows {
		if rows[i].AppName == app && rows[i].WindowTitle == title && rows[i].URL == url {
			return &rows[i]
		}
	}
	return nil
}

// TestCaptureURLs_DefaultOffAndToggle: флаг capture_urls по умолчанию ВЫКЛЮЧЕН и
// переключается по deviceID и читается по mTLS-серту (privacy: слежка за URL не
// включается сама собой). Зеркалит поведение capture_window_titles.
func TestCaptureURLs_DefaultOffAndToggle(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	fp := "fp-urls-" + uniq(t)
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, "urls-host", "urls-host", "192.0.2.11")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("GetDeviceIDByFingerprint: id=%q err=%v", id, err)
	}

	// Дефолт ВЫКЛЮЧЕН (privacy), устройство найдено.
	enabled, found, err := db.GetCaptureURLs(ctx, id)
	if err != nil {
		t.Fatalf("GetCaptureURLs: %v", err)
	}
	if !found {
		t.Fatal("устройство должно находиться")
	}
	if enabled {
		t.Fatal("capture_urls по умолчанию должен быть ВЫКЛЮЧЕН")
	}
	// По серту — тоже false.
	if v, err := db.GetCaptureURLsByFingerprint(ctx, fp); err != nil || v {
		t.Fatalf("GetCaptureURLsByFingerprint default = %v (err %v), want false", v, err)
	}

	// Включаем.
	ok, err := db.SetCaptureURLs(ctx, id, true)
	if err != nil || !ok {
		t.Fatalf("SetCaptureURLs(true): ok=%v err=%v", ok, err)
	}
	if enabled, _, _ := db.GetCaptureURLs(ctx, id); !enabled {
		t.Fatal("после включения GetCaptureURLs должен быть true")
	}
	if v, err := db.GetCaptureURLsByFingerprint(ctx, fp); err != nil || !v {
		t.Fatalf("GetCaptureURLsByFingerprint after enable = %v (err %v), want true", v, err)
	}

	// Неизвестное устройство — found=false, не ошибка.
	if ok, err := db.SetCaptureURLs(ctx, "00000000-0000-0000-0000-000000000000", true); err != nil || ok {
		t.Fatalf("SetCaptureURLs unknown: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	// Неизвестный серт → false, не ошибка.
	if v, err := db.GetCaptureURLsByFingerprint(ctx, "nope-"+uniq(t)); err != nil || v {
		t.Fatalf("GetCaptureURLsByFingerprint unknown = %v (err %v), want false", v, err)
	}
}

// TestAppUsage_URLPersistsAndServes: URL сохраняется и отдаётся, является ЧАСТЬЮ ключа
// (разные URL одного app+title — разные строки) и аккумулируется дельтами. Пустой URL
// (capture_urls выключен у агента) сохраняется как пустая строка — обратная совместимость.
func TestAppUsage_URLPersistsAndServes(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	fp := "fp-appurl-" + uniq(t)
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, "appurl-host", "appurl-host", "192.0.2.12")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("GetDeviceIDByFingerprint: id=%q err=%v", id, err)
	}

	day := time.Now().Format("2006-01-02")
	// Первая порция дельт.
	if err := db.UpsertAppUsage(ctx, id, []storage.AppUsageInput{
		{Day: day, AppName: "chrome.exe", WindowTitle: "Example", URL: "https://example.com", ForegroundSeconds: 30},
		{Day: day, AppName: "chrome.exe", WindowTitle: "Example", URL: "https://other.org", ForegroundSeconds: 15},
		{Day: day, AppName: "code.exe", WindowTitle: "", URL: "", ForegroundSeconds: 40},
	}); err != nil {
		t.Fatalf("UpsertAppUsage #1: %v", err)
	}
	// Вторая порция: та же (app,title,url) должна СУММИРОВАТЬСЯ (existing + delta).
	if err := db.UpsertAppUsage(ctx, id, []storage.AppUsageInput{
		{Day: day, AppName: "chrome.exe", WindowTitle: "Example", URL: "https://example.com", ForegroundSeconds: 20},
	}); err != nil {
		t.Fatalf("UpsertAppUsage #2: %v", err)
	}

	since := time.Now().AddDate(0, 0, -1)
	apps, _, err := db.GetAppUsage(ctx, id, since)
	if err != nil {
		t.Fatalf("GetAppUsage: %v", err)
	}

	// URL сохранился и просуммировался: 30 + 20 = 50.
	if r := findAppRow(apps, "chrome.exe", "Example", "https://example.com"); r == nil {
		t.Fatal("нет строки chrome/Example/example.com")
	} else if r.ForegroundSeconds != 50 {
		t.Errorf("example.com seconds = %d, want 50 (аккумуляция дельт)", r.ForegroundSeconds)
	}
	// Разный URL при том же app+title — ОТДЕЛЬНАЯ строка (URL — часть ключа).
	if r := findAppRow(apps, "chrome.exe", "Example", "https://other.org"); r == nil {
		t.Fatal("нет отдельной строки для other.org — URL должен быть частью ключа")
	} else if r.ForegroundSeconds != 15 {
		t.Errorf("other.org seconds = %d, want 15", r.ForegroundSeconds)
	}
	// Пустой URL сохраняется как '' (обратная совместимость с выключенным сбором).
	if r := findAppRow(apps, "code.exe", "", ""); r == nil {
		t.Fatal("нет строки code.exe с пустым URL")
	} else if r.ForegroundSeconds != 40 {
		t.Errorf("code.exe seconds = %d, want 40", r.ForegroundSeconds)
	}
}
