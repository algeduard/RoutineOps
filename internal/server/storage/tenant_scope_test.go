package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Полный per-query scoping (миграция 050 + tenant_scope.go): актор НЕ-Default тенанта видит
// ТОЛЬКО свои устройства/задачи/алерты; Default-тенант (провайдер, нескоуплено) видит всё.
// Проверяем ключевые чтения (ListDevices/GetDevice/ListAlerts) и мутацию (CreateTask).
func TestTenantScopeIsolation(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	tenantA, err := db.CreateTenant(ctx, "TenantA-"+uniq(t), "a"+uniq(t))
	if err != nil {
		t.Fatalf("CreateTenant A: %v", err)
	}
	tenantB, err := db.CreateTenant(ctx, "TenantB-"+uniq(t), "b"+uniq(t))
	if err != nil {
		t.Fatalf("CreateTenant B: %v", err)
	}

	devA := mustCreateActiveDevice(t, db, "hostA-"+uniq(t), "windows")
	devB := mustCreateActiveDevice(t, db, "hostB-"+uniq(t), "windows")
	if _, err := db.AssignDeviceTenant(ctx, devA.ID, tenantA.ID); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	if _, err := db.AssignDeviceTenant(ctx, devB.ID, tenantB.ID); err != nil {
		t.Fatalf("assign B: %v", err)
	}

	ctxA := storage.WithTenantScope(ctx, tenantA.ID)
	ctxB := storage.WithTenantScope(ctx, tenantB.ID)
	ctxProvider := storage.WithTenantScope(ctx, storage.DefaultTenantID) // = нескоуплено (провайдер)

	// ── ListDevices ──
	seenA := listDeviceIDs(t, db, ctxA)
	if !seenA[devA.ID] || seenA[devB.ID] {
		t.Fatalf("ListDevices tenant A: seesOwn=%v seesOther=%v (want true/false)", seenA[devA.ID], seenA[devB.ID])
	}
	seenB := listDeviceIDs(t, db, ctxB)
	if seenB[devA.ID] || !seenB[devB.ID] {
		t.Fatalf("ListDevices tenant B: seesOther=%v seesOwn=%v (want false/true)", seenB[devA.ID], seenB[devB.ID])
	}
	seenP := listDeviceIDs(t, db, ctxProvider)
	if !seenP[devA.ID] || !seenP[devB.ID] {
		t.Fatalf("ListDevices provider must see BOTH: A=%v B=%v", seenP[devA.ID], seenP[devB.ID])
	}

	// ── GetDevice кросс-тенант → not found ──
	if d, _, _ := db.GetDevice(ctxB, devA.ID); d != nil {
		t.Fatal("GetDevice: tenant B НЕ должен видеть устройство тенанта A")
	}
	if d, _, _ := db.GetDevice(ctxA, devA.ID); d == nil {
		t.Fatal("GetDevice: tenant A должен видеть своё устройство")
	}
	if d, _, _ := db.GetDevice(ctxProvider, devA.ID); d == nil {
		t.Fatal("GetDevice: провайдер должен видеть устройство тенанта A")
	}

	// ── CreateTask кросс-тенант → устройство «не найдено/не active» (задача не создаётся) ──
	if _, err := db.CreateTask(ctxB, devA.ID, "echo hi", "windows", "normal"); err == nil {
		t.Fatal("CreateTask: tenant B НЕ должен ставить задачу на устройство тенанта A")
	}
	if _, err := db.CreateTask(ctxA, devA.ID, "echo hi", "windows", "normal"); err != nil {
		t.Fatalf("CreateTask: tenant A должен ставить задачу на своё устройство: %v", err)
	}

	// ── Alerts ──
	if _, err := db.CreateAlert(ctx, devA.ID, "forbidden_software", `{"x":1}`, ""); err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}
	for _, a := range mustListAlerts(t, db, ctxB) {
		if a.DeviceID == devA.ID {
			t.Fatal("ListAlerts: tenant B видит алерт устройства тенанта A")
		}
	}
	foundOwn := false
	for _, a := range mustListAlerts(t, db, ctxA) {
		if a.DeviceID == devA.ID {
			foundOwn = true
		}
	}
	if !foundOwn {
		t.Fatal("ListAlerts: tenant A не видит свой алерт")
	}

	// ── Мутация by-id кросс-тенант: SetDeviceUpdateChannel не находит чужое устройство ──
	if found, _ := db.SetDeviceUpdateChannel(ctxB, devA.ID, "beta"); found {
		t.Fatal("SetDeviceUpdateChannel: tenant B не должен менять канал устройства тенанта A")
	}
	if found, _ := db.SetDeviceUpdateChannel(ctxA, devA.ID, "beta"); !found {
		t.Fatal("SetDeviceUpdateChannel: tenant A должен менять канал своего устройства")
	}

	// ── GetDeviceCN кросс-тенант → ErrNoRows (блокирует remote-desktop на чужое устройство) ──
	if _, err := db.GetDeviceCN(ctxB, devA.ID); err == nil {
		t.Fatal("GetDeviceCN: tenant B не должен резолвить CN устройства тенанта A (RD-сессия)")
	}
	if _, err := db.GetDeviceCN(ctxA, devA.ID); err != nil {
		t.Fatalf("GetDeviceCN: tenant A должен резолвить CN своего устройства: %v", err)
	}
	// Провайдер/нескоуплено (как worker доставки задач) → резолвит любое устройство.
	if _, err := db.GetDeviceCN(ctx, devA.ID); err != nil {
		t.Fatalf("GetDeviceCN: нескоупленный (worker) должен резолвить любое устройство: %v", err)
	}
}

func listDeviceIDs(t *testing.T, db *storage.DB, ctx context.Context) map[string]bool {
	t.Helper()
	ds, err := db.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	m := map[string]bool{}
	for _, d := range ds {
		m[d.ID] = true
	}
	return m
}

func mustListAlerts(t *testing.T, db *storage.DB, ctx context.Context) []storage.Alert {
	t.Helper()
	a, err := db.ListAlerts(ctx, "", 200)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	return a
}
