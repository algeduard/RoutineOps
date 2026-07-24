package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// End-to-end проверка per-query scoping ЧЕРЕЗ middleware: jwtMiddleware резолвит tenant актора
// из его users-строки и кладёт scope в ctx (storage.WithTenantScope), поэтому GET /devices
// отдаёт админу НЕ-Default тенанта только его устройства. Провайдер (Default) видит всё.
func TestTenantScopeAPIIsolation(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	ctx := context.Background()

	slug := strings.ToLower(t.Name())
	tenantA, err := db.CreateTenant(ctx, "APIisoA-"+t.Name(), slug)
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// Токен админа (юзер заводится в Default), затем переносим его в tenant A. Middleware
	// резолвит tenant per-request из БД, поэтому тот же токен после переназначения уже скоуплен.
	tok := tokenForRole(t, rtr, db, "it_admin", "tsi_")
	u, err := db.GetUserByEmail(ctx, "tsi_"+t.Name()+"@test.com")
	if err != nil || u == nil {
		t.Fatalf("GetUserByEmail: %v (u=%v)", err, u)
	}
	if _, err := db.AssignUserTenant(ctx, u.ID, tenantA.ID); err != nil {
		t.Fatalf("AssignUserTenant: %v", err)
	}

	// Два active-устройства: одно в tenant A, одно остаётся в Default.
	devA, err := db.CreatePendingDevice(ctx, "apiA-"+t.Name(), "windows")
	if err != nil {
		t.Fatal(err)
	}
	devDefault, err := db.CreatePendingDevice(ctx, "apiDef-"+t.Name(), "windows")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{devA.ID, devDefault.ID} {
		if err := db.UpdateDeviceStatus(ctx, id, "active"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.AssignDeviceTenant(ctx, devA.ID, tenantA.ID); err != nil {
		t.Fatal(err)
	}

	// GET /devices под токеном админа tenant A → только его устройство.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/devices", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /devices: %d %s", w.Code, w.Body)
	}
	var got []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode devices: %v", err)
	}
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ID] = true
	}
	if !ids[devA.ID] {
		t.Fatal("админ tenant A должен видеть своё устройство в GET /devices")
	}
	if ids[devDefault.ID] {
		t.Fatal("админ tenant A НЕ должен видеть устройство Default-тенанта (утечка через middleware)")
	}
}

// Провайдер (админ в Default) видит устройства всех тенантов.
func TestTenantScopeAPIProviderSeesAll(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	ctx := context.Background()

	tenantA, err := db.CreateTenant(ctx, "APIprovA-"+t.Name(), strings.ToLower(t.Name()))
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// Админ остаётся в Default (tokenForRole заводит его в Default) → провайдер.
	tok := tokenForRole(t, rtr, db, "it_admin", "tsp_")

	devA, _ := db.CreatePendingDevice(ctx, "provA-"+t.Name(), "windows")
	devDefault, _ := db.CreatePendingDevice(ctx, "provDef-"+t.Name(), "windows")
	for _, id := range []string{devA.ID, devDefault.ID} {
		_ = db.UpdateDeviceStatus(ctx, id, "active")
	}
	if _, err := db.AssignDeviceTenant(ctx, devA.ID, tenantA.ID); err != nil {
		t.Fatal(err)
	}

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/devices", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /devices: %d %s", w.Code, w.Body)
	}
	var got []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ID] = true
	}
	if !ids[devA.ID] || !ids[devDefault.ID] {
		t.Fatalf("провайдер (Default) должен видеть устройства ОБОИХ тенантов: A=%v Default=%v", ids[devA.ID], ids[devDefault.ID])
	}
}

var _ = storage.DefaultTenantID // ensures storage import used even if refactored
