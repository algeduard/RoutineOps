//go:build enterprise

package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func reportsRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.ReportsRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

// reportsSeedDevice — активное устройство с одним пакетом ПО (через инвентарь).
func reportsSeedDevice(t *testing.T, db *storage.DB, host, swName, swVersion string) string {
	t.Helper()
	ctx := context.Background()
	fp := "repfp-" + host
	if err := db.UpsertDeviceHeartbeat(ctx, storage.HeartbeatData{
		CertFingerprint: fp, DeviceID: host, CertCN: host, IPAddress: "192.0.2.40",
	}); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	if err := db.UpsertInventory(ctx, storage.InventoryData{
		CertFingerprint: fp, Hostname: host, OS: "Windows 11", OSVersion: "23H2",
		Software: []storage.SoftwareItem{{Name: swName, Version: swVersion}},
	}); err != nil {
		t.Fatalf("UpsertInventory: %v", err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("GetDeviceIDByFingerprint: id=%q err=%v", id, err)
	}
	return id
}

// CSV-экспорт device-отчёта: text/csv, корректная шапка + строка с устройством.
func TestReportsDevicesCSV(t *testing.T) {
	mgr := licensedManager(t, nil) // вся редакция
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)
	host := "repdev-" + t.Name()
	reportsSeedDevice(t, db, host, "SomePkg", "1.0.0")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/devices?format=csv", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("devices csv = %d, body %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, want text/csv", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "devices-report.csv") {
		t.Fatalf("Content-Disposition = %q, want attachment filename", cd)
	}

	recs, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(recs) < 2 {
		t.Fatalf("csv has %d records, want header + >=1 row", len(recs))
	}
	wantHeader := []string{"hostname", "os", "os_version", "status", "disk_encryption", "owner_email", "last_seen", "agent_version", "serial_number"}
	if strings.Join(recs[0], ",") != strings.Join(wantHeader, ",") {
		t.Fatalf("header = %v, want %v", recs[0], wantHeader)
	}
	found := false
	for _, r := range recs[1:] {
		if r[0] == host {
			found = true
		}
	}
	if !found {
		t.Fatalf("device %q not in csv rows", host)
	}
}

// CSV-инъекция нейтрализована: имя ПО, начинающееся с '=', в выводе НЕ начинается с '='.
func TestReportsInventoryCSVInjectionNeutralized(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	evil := "=1+2+cmd|' /C calc'!A0"
	reportsSeedDevice(t, db, "repinj-"+t.Name(), evil, "9.9")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/inventory?format=csv", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("inventory csv = %d, body %s", w.Code, w.Body)
	}

	recs, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	var cell string
	for _, r := range recs[1:] {
		if strings.Contains(r[0], "1+2+cmd") {
			cell = r[0]
		}
	}
	if cell == "" {
		t.Fatalf("injected software row not found in csv: %q", w.Body.String())
	}
	// Ключевая проверка: ячейка не начинается с формула-триггера '='.
	if strings.HasPrefix(cell, "=") {
		t.Fatalf("cell still starts with '=' (injection not neutralized): %q", cell)
	}
	if !strings.HasPrefix(cell, "'=") {
		t.Fatalf("cell not apostrophe-prefixed: %q", cell)
	}
}

// Период-фильтр audit-отчёта: широкое окно содержит запись, окно в прошлом — нет.
func TestReportsAuditPeriodFilter(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	action := "reportapi_action_" + t.Name()
	if err := db.WriteAuditLog(context.Background(), "", "rep-audit@test.com", action, "report", "z", nil); err != nil {
		t.Fatalf("WriteAuditLog: %v", err)
	}

	bodyContainsAction := func(query string) bool {
		w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/audit?format=csv"+query, nil, tok)
		if w.Code != http.StatusOK {
			t.Fatalf("audit csv%s = %d, body %s", query, w.Code, w.Body)
		}
		return strings.Contains(w.Body.String(), action)
	}

	// Без границ — запись есть.
	if !bodyContainsAction("") {
		t.Fatal("audit report without period should contain the entry")
	}
	// Окно в прошлом (to = давняя дата) — записи нет.
	if bodyContainsAction("&from=2000-01-01&to=2000-01-02") {
		t.Fatal("past window should NOT contain today's entry")
	}
	// Окно, включающее сегодня (широкие границы) — запись есть.
	if !bodyContainsAction("&from=2000-01-01&to=2999-01-01") {
		t.Fatal("wide window should contain the entry")
	}
}

// PDF-формат отдаёт печатный HTML (inline text/html), а не файл-вложение.
func TestReportsPDFReturnsPrintableHTML(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)
	host := "reppdf-" + t.Name()
	reportsSeedDevice(t, db, host, "PdfPkg", "1.0")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/devices?format=pdf", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("devices pdf = %d, body %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<table") || !strings.Contains(body, "Device fleet report") {
		t.Fatalf("html report missing table/title: %.120q", body)
	}
	if !strings.Contains(body, host) {
		t.Fatalf("html report missing device %q", host)
	}
}

// Неизвестный тип отчёта → 404 (но за лицензией: гейт проходит, тип не найден).
func TestReportsUnknownType(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/bogus?format=csv", nil, tok)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown report type = %d, want 404", w.Code)
	}
}

// Некорректный format → 400.
func TestReportsBadFormat(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/devices?format=xml", nil, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("format=xml = %d, want 400", w.Code)
	}
}

// Без активной лицензии на фичу — 402 на всех типах.
func TestReportsRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	for _, typ := range []string{"devices", "inventory", "audit", "alerts"} {
		w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/"+typ+"?format=csv", nil, tok)
		if w.Code != http.StatusPaymentRequired {
			t.Errorf("GET /reports/%s без лицензии = %d, want 402", typ, w.Code)
		}
	}
}

// Отчёты — только it_admin (viewer → 403).
func TestReportsRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := reportsRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/reports/devices?format=csv", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /reports/devices = %d, want 403", w.Code)
	}
}

// /capabilities отражает активную лицензию reports.
func TestReportsCapability(t *testing.T) {
	mgr := licensedManager(t, []string{license.FeatureReports})
	rtr, db := reportsRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d", w.Code)
	}
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeatureReports] {
		t.Fatalf("ожидали reports=true, got %+v", caps)
	}
}
