//go:build enterprise

package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// licensedManager — Manager с уже применённой валидной лицензией (features: nil = вся
// редакция). Для проверки, что фича включается при активной лицензии.
func licensedManager(t *testing.T, features []string) *license.Manager {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	blob, err := license.Issue(license.Claims{
		Edition:   "enterprise",
		Features:  features,
		NotBefore: time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, priv)
	if err != nil {
		t.Fatal(err)
	}
	mgr := license.NewManager(pub, 0, "")
	if _, err := mgr.Apply(blob, ""); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func softwareRemovalRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.SoftwareRemovalRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

func activeDeviceID(t *testing.T, db *storage.DB, host string) string {
	t.Helper()
	d, err := db.CreatePendingDevice(context.Background(), host, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDeviceStatus(context.Background(), d.ID, "active"); err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestSoftwareRemovalLicensedCreatesTask(t *testing.T) {
	mgr := licensedManager(t, nil) // пустой список фич = вся редакция
	rtr, db := softwareRemovalRouter(t, mgr)
	tok := authToken(t, rtr, db)
	dev := activeDeviceID(t, db, "sw-"+t.Name())

	body, _ := json.Marshal(map[string]string{"name": "Foxit Reader", "version": "12.0"})
	w := authedDo(t, rtr, http.MethodPost, fmt.Sprintf("/api/v1/devices/%s/software/remove", dev), body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("licensed removal = %d, body %s", w.Code, w.Body)
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.TaskID == "" {
		t.Fatal("нет task_id в ответе")
	}
	task, err := db.GetTask(context.Background(), resp.TaskID)
	if err != nil || task == nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.TaskType != "remove_software" || task.SoftwareName != "Foxit Reader" || task.SoftwareVersion != "12.0" {
		t.Fatalf("задача создана неверно: %+v", task)
	}
}

// Без активной лицензии на фичу — 402, БД не трогаем.
func TestSoftwareRemovalRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := softwareRemovalRouter(t, mgr)
	tok := authToken(t, rtr, db)
	dev := activeDeviceID(t, db, "sw-"+t.Name())

	body, _ := json.Marshal(map[string]string{"name": "Foxit"})
	w := authedDo(t, rtr, http.MethodPost, fmt.Sprintf("/api/v1/devices/%s/software/remove", dev), body, tok)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("без лицензии = %d, want 402", w.Code)
	}
}

// Неактивное устройство → 409 (задача повисла бы pending).
func TestSoftwareRemovalNonActive(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := softwareRemovalRouter(t, mgr)
	tok := authToken(t, rtr, db)
	d, _ := db.CreatePendingDevice(context.Background(), "pend-"+t.Name(), "windows") // остаётся pending

	body, _ := json.Marshal(map[string]string{"name": "Foxit"})
	w := authedDo(t, rtr, http.MethodPost, fmt.Sprintf("/api/v1/devices/%s/software/remove", d.ID), body, tok)
	if w.Code != http.StatusConflict {
		t.Fatalf("неактивное устройство = %d, want 409", w.Code)
	}
}

// Удаление ПО — только it_admin (viewer → 403).
func TestSoftwareRemovalRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := softwareRemovalRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	dev := activeDeviceID(t, db, "sw-"+t.Name())

	body, _ := json.Marshal(map[string]string{"name": "Foxit"})
	w := authedDo(t, rtr, http.MethodPost, fmt.Sprintf("/api/v1/devices/%s/software/remove", dev), body, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d, want 403", w.Code)
	}
}

// /capabilities отражает активную лицензию.
func TestCapabilitiesReflectsLicense(t *testing.T) {
	mgr := licensedManager(t, []string{license.FeatureSoftwareRemoval})
	rtr, db := softwareRemovalRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d", w.Code)
	}
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeatureSoftwareRemoval] {
		t.Fatalf("ожидали software_removal=true, got %+v", caps)
	}
}
