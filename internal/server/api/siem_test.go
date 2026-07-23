//go:build enterprise

package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func siemRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false, api.WithAdminRoutes(api.SIEMConfigRoutes(mgr)))
	return rtr, db
}

func TestSIEMConfigLicensedRoundTrip(t *testing.T) {
	mgr := licensedManager(t, nil) // вся редакция
	rtr, db := siemRouter(t, mgr)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]any{"enabled": true, "webhook_url": "https://siem.example/x", "hmac_secret": "topsecret"})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/siem/config", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, body %s", w.Code, w.Body)
	}

	w = authedDo(t, rtr, http.MethodGet, "/api/v1/siem/config", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	raw := w.Body.String()
	if strings.Contains(raw, "topsecret") {
		t.Fatalf("СЕКРЕТ утёк в GET: %s", raw)
	}
	var cfg struct {
		Enabled    bool   `json:"enabled"`
		WebhookURL string `json:"webhook_url"`
		HasSecret  bool   `json:"has_secret"`
	}
	json.NewDecoder(strings.NewReader(raw)).Decode(&cfg)
	if !cfg.Enabled || !cfg.HasSecret || cfg.WebhookURL != "https://siem.example/x" {
		t.Fatalf("статус конфига: %+v", cfg)
	}
}

func TestSIEMConfigRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := siemRouter(t, mgr)
	tok := authToken(t, rtr, db)
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/siem/config", nil, tok)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("без лицензии = %d, want 402", w.Code)
	}
}

func TestSIEMConfigRejectsInvalidURL(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := siemRouter(t, mgr)
	tok := authToken(t, rtr, db)
	body, _ := json.Marshal(map[string]any{"enabled": true, "webhook_url": "not-a-url", "hmac_secret": ""})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/siem/config", body, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("кривой URL при включении = %d, want 400", w.Code)
	}
}

func TestSIEMConfigRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := siemRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/siem/config", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d, want 403", w.Code)
	}
}
