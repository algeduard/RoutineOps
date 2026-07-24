//go:build enterprise

package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func autoRemediationRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.AutoRemediationRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

// GET/PUT конфига под активной лицензией: дефолт выключен, PUT включает dry_run.
func TestAutoRemediationConfigGetPut(t *testing.T) {
	mgr := licensedManager(t, nil) // пустой список фич = вся редакция
	rtr, db := autoRemediationRouter(t, mgr)
	tok := authToken(t, rtr, db)

	// Дефолт — выключено.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/auto-remediation/config", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET config = %d, body %s", w.Code, w.Body)
	}
	var cfg storage.AutoRemediationConfig
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.Enabled || cfg.DryRun {
		t.Fatalf("дефолт должен быть выключен: %+v", cfg)
	}

	// PUT: включаем dry_run.
	body, _ := json.Marshal(map[string]bool{"enabled": true, "dry_run": true})
	w = authedDo(t, rtr, http.MethodPut, "/api/v1/auto-remediation/config", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT config = %d, body %s", w.Code, w.Body)
	}
	json.NewDecoder(w.Body).Decode(&cfg)
	if !cfg.Enabled || !cfg.DryRun {
		t.Fatalf("после PUT ожидали enabled+dry_run: %+v", cfg)
	}

	// Перечитываем — настройка персистентна.
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/auto-remediation/config", nil, tok)
	json.NewDecoder(w.Body).Decode(&cfg)
	if !cfg.Enabled || !cfg.DryRun {
		t.Fatalf("после перечитывания: %+v", cfg)
	}
}

// Лог доступен под лицензией (пустой, но валидный JSON-массив).
func TestAutoRemediationLog(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := autoRemediationRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/auto-remediation/log", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET log = %d, body %s", w.Code, w.Body)
	}
	var entries []storage.RemediationLogEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("лог должен быть JSON-массивом: %v", err)
	}
}

// Без активной лицензии — 402 на все ручки, БД не трогаем.
func TestAutoRemediationRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := autoRemediationRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/auto-remediation/config", nil, tok)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("GET без лицензии = %d, want 402", w.Code)
	}
	body, _ := json.Marshal(map[string]bool{"enabled": true})
	w = authedDo(t, rtr, http.MethodPut, "/api/v1/auto-remediation/config", body, tok)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("PUT без лицензии = %d, want 402", w.Code)
	}
}

// Настройка авто-устранения — только it_admin (viewer → 403).
func TestAutoRemediationRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := autoRemediationRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	body, _ := json.Marshal(map[string]bool{"enabled": true})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/auto-remediation/config", body, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT = %d, want 403", w.Code)
	}
}

// /capabilities отражает активную лицензию на авто-устранение.
func TestCapabilitiesReflectsAutoRemediation(t *testing.T) {
	mgr := licensedManager(t, []string{license.FeatureAutoRemediation})
	rtr, db := autoRemediationRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d", w.Code)
	}
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeatureAutoRemediation] {
		t.Fatalf("ожидали auto_remediation=true, got %+v", caps)
	}
}
