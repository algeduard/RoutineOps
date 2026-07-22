//go:build enterprise

package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// licenseRouter строит роутер с СМОНТИРОВАННЫМ /license (как enterprise-оверлей), чтобы
// проверить HTTP-контракт активации. Open-core NewRouter опций не передаёт → 404 (см.
// license_absent_test.go), здесь передаём.
func licenseRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false, api.WithAdminRoutes(api.LicenseRoutes(mgr)))
	return rtr, db
}

func TestLicenseApplyStatusDeactivate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	salt, hash, _ := license.HashPassword("pw")
	blob, err := license.Issue(license.Claims{
		Licensee:  "Test Co",
		Edition:   "enterprise",
		NotBefore: time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
		PwSalt:    salt,
		PwHash:    hash,
	}, priv)
	if err != nil {
		t.Fatal(err)
	}

	mgr := license.NewManager(pub, 0, filepath.Join(t.TempDir(), "lic.blob"))
	rtr, db := licenseRouter(t, mgr)
	tok := authToken(t, rtr, db)

	// Старт: роут смонтирован (200, НЕ 404), но лицензии нет → configured=false.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/license", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /license = %d, want 200 (enterprise монтирует роут)", w.Code)
	}
	var st license.Status
	json.NewDecoder(w.Body).Decode(&st)
	if st.Configured {
		t.Fatalf("должно стартовать без лицензии: %+v", st)
	}

	// Активация.
	body, _ := json.Marshal(map[string]string{"license": blob, "activation_password": "pw"})
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/license", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("apply = %d, body %s", w.Code, w.Body)
	}
	json.NewDecoder(w.Body).Decode(&st)
	if !st.Configured || !st.Valid || st.Licensee != "Test Co" {
		t.Fatalf("после активации: %+v", st)
	}
	// Персист лёг на диск → без persist_warning.
	if st.PersistWarning != "" {
		t.Errorf("неожиданный persist_warning: %q", st.PersistWarning)
	}

	// Неверный пароль → 400, текущая лицензия НЕ сбрасывается.
	bad, _ := json.Marshal(map[string]string{"license": blob, "activation_password": "nope"})
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/license", bad, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong password = %d, want 400", w.Code)
	}
	if !mgr.Status().Configured {
		t.Fatalf("отклонённая активация не должна сбрасывать текущую лицензию")
	}

	// Деактивация (пустой license) → Free.
	empty, _ := json.Marshal(map[string]string{"license": "", "activation_password": ""})
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/license", empty, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("deactivate = %d", w.Code)
	}
	json.NewDecoder(w.Body).Decode(&st)
	if st.Configured {
		t.Fatalf("после деактивации всё ещё configured: %+v", st)
	}
}

// Битый/чужой ключ → 400 и текущая лицензия цела.
func TestLicenseApplyRejectsBadSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	blob, _ := license.Issue(license.Claims{Edition: "enterprise"}, otherPriv) // подписан ЧУЖИМ ключом

	mgr := license.NewManager(pub, 0, "")
	rtr, db := licenseRouter(t, mgr)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]string{"license": blob, "activation_password": ""})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/license", body, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("чужой ключ = %d, want 400", w.Code)
	}
}

// /license — только it_admin (viewer → 403).
func TestLicenseRequiresAdmin(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "")
	rtr, db := licenseRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/license", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /license = %d, want 403", w.Code)
	}
}
