//go:build enterprise

package cvesync

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/Floodww/RoutineOps/internal/server/testutil"
)

var sharedDSN string

func TestMain(m *testing.M) {
	dsn, cleanup := testutil.NewDSNWithCleanup()
	sharedDSN = dsn
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Connect(context.Background(), sharedDSN)
	if err != nil {
		t.Fatalf("storage.Connect: %v", err)
	}
	t.Cleanup(db.Close)
	// Singleton-конфиг и фид общие в temp-БД — чистим, чтобы тесты не текли друг в друга.
	if _, err := db.Pool().Exec(context.Background(), `TRUNCATE cve_feed_source, cve_feed`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// validFeed отдаёт валидный JSON-массив записей фида (тот же формат, что POST /cve/feed).
const validFeed = `[
  {"cve_id":"CVE-2024-0001","product":"Acme Reader","version_constraint":"<2.0.0","severity":"high","cvss":7.5,"summary":"test"},
  {"cve_id":"CVE-2024-0002","product":"Acme Writer","version_constraint":"*","severity":"critical"}
]`

// Ядро расширения: включённый источник + подошедший срок → фид скачан с httptest-сервера,
// загружен, last_synced_at проставлен, last_status = ok.
func TestTickSyncsFeed(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, validFeed)
	}))
	defer srv.Close()

	if err := db.SetCVEFeedSource(ctx, srv.URL, 24, true, false); err != nil {
		t.Fatalf("SetCVEFeedSource: %v", err)
	}

	s := NewSyncer(db, func() bool { return true }, discardLogger())
	s.tick()

	if hits != 1 {
		t.Fatalf("источник запрошен %d раз, want 1", hits)
	}
	if n, _ := db.CVEFeedCount(ctx); n != 2 {
		t.Fatalf("в фиде %d записей, want 2", n)
	}
	cfg, _ := db.GetCVEFeedSource(ctx)
	if cfg.LastSyncedAt == nil {
		t.Fatal("last_synced_at не проставлен после синка")
	}
	if len(cfg.LastStatus) < 2 || cfg.LastStatus[:2] != "ok" {
		t.Fatalf("last_status = %q, want ok...", cfg.LastStatus)
	}
}

// Без лицензии тик пустой — источник не запрашивается, фид не грузится.
func TestTickUnlicensedNoop(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, validFeed)
	}))
	defer srv.Close()

	if err := db.SetCVEFeedSource(ctx, srv.URL, 24, true, false); err != nil {
		t.Fatalf("SetCVEFeedSource: %v", err)
	}
	NewSyncer(db, func() bool { return false }, discardLogger()).tick()

	if hit {
		t.Fatal("без лицензии источник не должен запрашиваться")
	}
	if n, _ := db.CVEFeedCount(ctx); n != 0 {
		t.Fatalf("без лицензии фид пуст, got %d", n)
	}
}

// Расписание: только что синканный источник (last_synced_at свежий, интервал большой) не
// синкается повторно на следующем тике.
func TestTickRespectsSchedule(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = io.WriteString(w, validFeed)
	}))
	defer srv.Close()

	if err := db.SetCVEFeedSource(ctx, srv.URL, 24, true, false); err != nil {
		t.Fatalf("SetCVEFeedSource: %v", err)
	}
	s := NewSyncer(db, func() bool { return true }, discardLogger())
	s.tick() // первый синк
	s.tick() // сразу второй — срок не подошёл (интервал 24ч)
	if hits != 1 {
		t.Fatalf("источник запрошен %d раз, want 1 (расписание не соблюдено)", hits)
	}
}

// Недоступный источник: сервер не падает, last_status = error, фид не тронут; Sync возвращает
// err == nil (сбой источника ≠ отказ сервера).
func TestSyncUnreachableSource(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	// Сервер, который сразу закрыт → connection refused на его URL.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	if err := db.SetCVEFeedSource(ctx, url, 24, true, true); err != nil {
		t.Fatalf("SetCVEFeedSource: %v", err)
	}
	loaded, status, err := Sync(ctx, db, &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Sync недоступного источника вернул err (должно быть nil): %v", err)
	}
	if loaded != 0 {
		t.Fatalf("loaded = %d, want 0", loaded)
	}
	if len(status) < 5 || status[:5] != "error" {
		t.Fatalf("status = %q, want error...", status)
	}
	cfg, _ := db.GetCVEFeedSource(ctx)
	if cfg.LastSyncedAt == nil || len(cfg.LastStatus) < 5 || cfg.LastStatus[:5] != "error" {
		t.Fatalf("last_status после сбоя = %q (synced=%v)", cfg.LastStatus, cfg.LastSyncedAt)
	}
	if n, _ := db.CVEFeedCount(ctx); n != 0 {
		t.Fatalf("битый источник не должен менять фид, got %d", n)
	}
}

// due: никогда не синкали → true; свежий синк при большом интервале → false; давний → true.
func TestDue(t *testing.T) {
	now := time.Now()
	if !due(storage.CVEFeedSource{SyncIntervalHours: 24}, now) {
		t.Fatal("никогда не синканный источник должен быть due")
	}
	fresh := now.Add(-time.Hour)
	if due(storage.CVEFeedSource{SyncIntervalHours: 24, LastSyncedAt: &fresh}, now) {
		t.Fatal("синканный час назад при интервале 24ч не должен быть due")
	}
	old := now.Add(-25 * time.Hour)
	if !due(storage.CVEFeedSource{SyncIntervalHours: 24, LastSyncedAt: &old}, now) {
		t.Fatal("синканный 25ч назад при интервале 24ч должен быть due")
	}
}
