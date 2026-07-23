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

func tenantsRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.TenantsRoutes(mgr)))
	return rtr, db
}

func tenantByID(list []storage.Tenant, id string) *storage.Tenant {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// Создание тенанта, назначение устройства и корректность счётчиков.
func TestTenantCreateAssignAndCounts(t *testing.T) {
	mgr := licensedManager(t, nil) // вся редакция
	rtr, db := tenantsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	slug := "acme-" + uniqSuffix()
	body, _ := json.Marshal(map[string]string{"name": "Acme " + t.Name(), "slug": slug})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/tenants", body, tok)
	if w.Code != http.StatusCreated {
		t.Fatalf("create tenant = %d, body %s", w.Code, w.Body)
	}
	var created storage.Tenant
	json.NewDecoder(w.Body).Decode(&created)
	if created.ID == "" || created.IsDefault || created.Slug != slug {
		t.Fatalf("создан неверно: %+v", created)
	}

	dev := activeDeviceID(t, db, "tn-"+t.Name())
	ab, _ := json.Marshal(map[string][]string{"device_ids": {dev}})
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/tenants/"+created.ID+"/assign", ab, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("assign = %d, body %s", w.Code, w.Body)
	}
	var res struct {
		DevicesAssigned int `json:"devices_assigned"`
		UsersAssigned   int `json:"users_assigned"`
	}
	json.NewDecoder(w.Body).Decode(&res)
	if res.DevicesAssigned != 1 {
		t.Fatalf("devices_assigned = %d, want 1", res.DevicesAssigned)
	}

	w = authedDo(t, rtr, http.MethodGet, "/api/v1/tenants", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var list []storage.Tenant
	json.NewDecoder(w.Body).Decode(&list)
	mine := tenantByID(list, created.ID)
	if mine == nil || mine.DeviceCount != 1 {
		t.Fatalf("счётчик устройств тенанта: %+v", mine)
	}
}

// Назначение устройства в непустой тенант, затем попытка его удалить → 409.
func TestTenantNonEmptyNotDeletable(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := tenantsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]string{"name": "Full " + t.Name(), "slug": "full-" + uniqSuffix()})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/tenants", body, tok)
	var created storage.Tenant
	json.NewDecoder(w.Body).Decode(&created)

	dev := activeDeviceID(t, db, "tn-full-"+t.Name())
	ab, _ := json.Marshal(map[string][]string{"device_ids": {dev}})
	authedDo(t, rtr, http.MethodPost, "/api/v1/tenants/"+created.ID+"/assign", ab, tok)

	w = authedDo(t, rtr, http.MethodDelete, "/api/v1/tenants/"+created.ID, nil, tok)
	if w.Code != http.StatusConflict {
		t.Fatalf("удаление непустого = %d, want 409", w.Code)
	}
}

// Default-тенант неудаляем → 409.
func TestTenantDefaultNotDeletable(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := tenantsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodDelete, "/api/v1/tenants/"+storage.DefaultTenantID, nil, tok)
	if w.Code != http.StatusConflict {
		t.Fatalf("удаление default = %d, want 409", w.Code)
	}
}

// Невалидный slug → 400.
func TestTenantInvalidSlug(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := tenantsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]string{"name": "Bad " + t.Name(), "slug": "Not Valid!"})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/tenants", body, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("кривой slug = %d, want 400", w.Code)
	}
}

// Без активной лицензии на фичу — 402, БД не трогаем.
func TestTenantRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := tenantsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/tenants", nil, tok)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("без лицензии = %d, want 402", w.Code)
	}
}

// Управление тенантами — только it_admin (viewer → 403).
func TestTenantRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := tenantsRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/tenants", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d, want 403", w.Code)
	}
}
