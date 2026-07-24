//go:build enterprise

package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// cveFeedSourceRouter — роутер с CVE-скан + feed-source роутами и /capabilities.
func cveFeedSourceRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.CVERoutes(mgr)),
		api.WithAdminRoutes(api.CVEFeedSourceRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

type feedSourceResp struct {
	URL               string  `json:"url"`
	SyncIntervalHours int     `json:"sync_interval_hours"`
	Enabled           bool    `json:"enabled"`
	AutoScan          bool    `json:"auto_scan"`
	LastSyncedAt      *string `json:"last_synced_at"`
	LastStatus        string  `json:"last_status"`
}

const apiValidFeed = `[
  {"cve_id":"CVE-2024-9001","product":"apisrc-reader","version_constraint":"*","severity":"high"},
  {"cve_id":"CVE-2024-9002","product":"apisrc-writer","version_constraint":"<3.0.0","severity":"critical"}
]`

// PUT сохраняет конфиг источника, GET его отдаёт.
func TestCVEFeedSourcePutGet(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveFeedSourceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]any{
		"url": "https://feeds.example/cve.json", "sync_interval_hours": 12, "enabled": true, "auto_scan": false,
	})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/cve/feed-source", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /cve/feed-source = %d, body %s", w.Code, w.Body)
	}
	var put feedSourceResp
	json.NewDecoder(w.Body).Decode(&put)
	if put.URL != "https://feeds.example/cve.json" || put.SyncIntervalHours != 12 || !put.Enabled || put.AutoScan {
		t.Fatalf("PUT ответ неверен: %+v", put)
	}

	w = authedDo(t, rtr, http.MethodGet, "/api/v1/cve/feed-source", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /cve/feed-source = %d", w.Code)
	}
	var got feedSourceResp
	json.NewDecoder(w.Body).Decode(&got)
	if got.URL != "https://feeds.example/cve.json" || got.SyncIntervalHours != 12 || !got.Enabled {
		t.Fatalf("GET ответ неверен: %+v", got)
	}
}

// PUT с enabled=true и невалидным URL → 400.
func TestCVEFeedSourceBadURL(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveFeedSourceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]any{"url": "not a url", "enabled": true})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/cve/feed-source", body, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT невалидный url при enabled = %d, want 400", w.Code)
	}
}

// Форс-синк с httptest-источником, отдающим валидный фид → фид загружен, last_synced_at
// обновлён, last_status = ok; сводка отражает размер фида.
func TestCVEFeedSourceForceSync(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveFeedSourceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, apiValidFeed)
	}))
	defer srv.Close()

	// Настроить источник на httptest-сервер (auto_scan включён — синк ещё и пересканирует).
	body, _ := json.Marshal(map[string]any{"url": srv.URL, "sync_interval_hours": 24, "enabled": true, "auto_scan": true})
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/cve/feed-source", body, tok); w.Code != http.StatusOK {
		t.Fatalf("PUT /cve/feed-source = %d, body %s", w.Code, w.Body)
	}

	w := authedDo(t, rtr, http.MethodPost, "/api/v1/cve/feed-source/sync", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /cve/feed-source/sync = %d, body %s", w.Code, w.Body)
	}
	var synced feedSourceResp
	json.NewDecoder(w.Body).Decode(&synced)
	if synced.LastSyncedAt == nil {
		t.Fatal("last_synced_at не обновлён после форс-синка")
	}
	if len(synced.LastStatus) < 2 || synced.LastStatus[:2] != "ok" {
		t.Fatalf("last_status = %q, want ok...", synced.LastStatus)
	}

	// Фид действительно загружен — видно в сводке.
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/cve/summary", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /cve/summary = %d", w.Code)
	}
	var sum struct {
		FeedCount int `json:"feed_count"`
	}
	json.NewDecoder(w.Body).Decode(&sum)
	if sum.FeedCount != 2 {
		t.Fatalf("feed_count = %d, want 2 (фид синкнут)", sum.FeedCount)
	}
}

// Форс-синк недоступного источника: сервер не падает, 200 с last_status = error.
func TestCVEFeedSourceForceSyncUnreachable(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveFeedSourceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	// Закрытый сервер → connection refused на его URL.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	// enabled=false, чтобы PUT не валидировал URL, но форс-синк всё равно его использует.
	body, _ := json.Marshal(map[string]any{"url": url, "sync_interval_hours": 24, "enabled": false, "auto_scan": true})
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/cve/feed-source", body, tok); w.Code != http.StatusOK {
		t.Fatalf("PUT /cve/feed-source = %d", w.Code)
	}

	w := authedDo(t, rtr, http.MethodPost, "/api/v1/cve/feed-source/sync", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("POST sync недоступного источника = %d, want 200 (сервер не падает)", w.Code)
	}
	var synced feedSourceResp
	json.NewDecoder(w.Body).Decode(&synced)
	if len(synced.LastStatus) < 5 || synced.LastStatus[:5] != "error" {
		t.Fatalf("last_status = %q, want error...", synced.LastStatus)
	}
}

// Без активной лицензии на фичу — 402 на всех feed-source роутах.
func TestCVEFeedSourceRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := cveFeedSourceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/cve/feed-source"},
		{http.MethodPut, "/api/v1/cve/feed-source"},
		{http.MethodPost, "/api/v1/cve/feed-source/sync"},
	} {
		w := authedDo(t, rtr, tc.method, tc.path, []byte("{}"), tok)
		if w.Code != http.StatusPaymentRequired {
			t.Errorf("%s %s без лицензии = %d, want 402", tc.method, tc.path, w.Code)
		}
	}
}

// Feed-source роуты — только it_admin (viewer → 403).
func TestCVEFeedSourceRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveFeedSourceRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/cve/feed-source", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /cve/feed-source = %d, want 403", w.Code)
	}
}
