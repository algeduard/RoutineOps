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

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func cveRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.CVERoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

// cveDeviceWithSoftware — активное устройство с одним пакетом ПО заданной версии
// (device_software пишется только инвентарём, матчится по fingerprint).
func cveDeviceWithSoftware(t *testing.T, db *storage.DB, host, product, version string) string {
	t.Helper()
	ctx := context.Background()
	fp := "cveapifp-" + host
	if err := db.UpsertDeviceHeartbeat(ctx, storage.HeartbeatData{
		CertFingerprint: fp, DeviceID: host, CertCN: host, IPAddress: "192.0.2.20",
	}); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	if err := db.UpsertInventory(ctx, storage.InventoryData{
		CertFingerprint: fp, Hostname: host, OS: "Windows 11", OSVersion: "1.0",
		Software: []storage.SoftwareItem{{Name: product, Version: version}},
	}); err != nil {
		t.Fatalf("UpsertInventory: %v", err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("GetDeviceIDByFingerprint: id=%q err=%v", id, err)
	}
	return id
}

type cveFindingResp struct {
	DeviceID         string `json:"device_id"`
	CVEID            string `json:"cve_id"`
	Product          string `json:"product"`
	InstalledVersion string `json:"installed_version"`
	Severity         string `json:"severity"`
}

// E2E за лицензией: залить фид → устройство с уязвимой версией → скан → находка в списке и сводке.
func TestCVEFeedScanFindingsLicensed(t *testing.T) {
	mgr := licensedManager(t, nil) // вся редакция
	rtr, db := cveRouter(t, mgr)
	tok := authToken(t, rtr, db)
	name := t.Name()
	product := "apivuln-" + name
	cve := "CVE-2024-" + name

	feed := []map[string]any{{
		"cve_id": cve, "product": product, "version_constraint": "<2.0.0",
		"severity": "high", "cvss": 8.1, "summary": "e2e",
	}}
	body, _ := json.Marshal(feed)
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/cve/feed", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /cve/feed = %d, body %s", w.Code, w.Body)
	}
	var loaded struct {
		Loaded int `json:"loaded"`
	}
	json.NewDecoder(w.Body).Decode(&loaded)
	if loaded.Loaded != 1 {
		t.Fatalf("loaded = %d, want 1", loaded.Loaded)
	}

	dev := cveDeviceWithSoftware(t, db, "apidev-"+name, product, "1.5.0")

	w = authedDo(t, rtr, http.MethodPost, "/api/v1/cve/scan", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /cve/scan = %d, body %s", w.Code, w.Body)
	}
	var scan struct {
		Findings int `json:"findings"`
	}
	json.NewDecoder(w.Body).Decode(&scan)
	if scan.Findings < 1 {
		t.Fatalf("scan findings = %d, want >= 1", scan.Findings)
	}

	// Находки конкретного устройства.
	w = authedDo(t, rtr, http.MethodGet, fmt.Sprintf("/api/v1/cve/findings?device_id=%s", dev), nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /cve/findings = %d", w.Code)
	}
	var findings []cveFindingResp
	json.NewDecoder(w.Body).Decode(&findings)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	if findings[0].CVEID != cve || findings[0].Product != product || findings[0].InstalledVersion != "1.5.0" || findings[0].Severity != "high" {
		t.Fatalf("находка неверна: %+v", findings[0])
	}

	// Сводка отражает находку.
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/cve/summary", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /cve/summary = %d", w.Code)
	}
	var sum struct {
		FeedCount     int `json:"feed_count"`
		TotalFindings int `json:"total_findings"`
		BySeverity    []struct {
			Severity string `json:"severity"`
			Count    int    `json:"count"`
		} `json:"by_severity"`
	}
	json.NewDecoder(w.Body).Decode(&sum)
	if sum.FeedCount != 1 || sum.TotalFindings != 1 {
		t.Fatalf("сводка: feed_count=%d total_findings=%d, want 1/1", sum.FeedCount, sum.TotalFindings)
	}
	if len(sum.BySeverity) != 4 || sum.BySeverity[0].Severity != "critical" {
		t.Fatalf("by_severity фиксированного порядка нет: %+v", sum.BySeverity)
	}
}

// Устройство с исправленной версией — находки нет (граница <2.0.0, установлено 2.0.0).
func TestCVEScanNoFindingForPatched(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveRouter(t, mgr)
	tok := authToken(t, rtr, db)
	name := t.Name()
	product := "apisafe-" + name

	feed := []map[string]any{{"cve_id": "CVE-x-" + name, "product": product, "version_constraint": "<2.0.0", "severity": "high"}}
	body, _ := json.Marshal(feed)
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/cve/feed", body, tok); w.Code != http.StatusOK {
		t.Fatalf("POST /cve/feed = %d", w.Code)
	}
	dev := cveDeviceWithSoftware(t, db, "apisafedev-"+name, product, "2.0.0")
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/cve/scan", nil, tok); w.Code != http.StatusOK {
		t.Fatalf("POST /cve/scan = %d", w.Code)
	}
	w := authedDo(t, rtr, http.MethodGet, fmt.Sprintf("/api/v1/cve/findings?device_id=%s", dev), nil, tok)
	var findings []cveFindingResp
	json.NewDecoder(w.Body).Decode(&findings)
	if len(findings) != 0 {
		t.Fatalf("исправленная версия дала %d находок, want 0: %+v", len(findings), findings)
	}
}

// DELETE /cve/feed очищает фид.
func TestCVEFeedClear(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveRouter(t, mgr)
	tok := authToken(t, rtr, db)

	feed := []map[string]any{{"cve_id": "CVE-clr-" + t.Name(), "product": "clr-" + t.Name()}}
	body, _ := json.Marshal(feed)
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/cve/feed", body, tok); w.Code != http.StatusOK {
		t.Fatalf("POST /cve/feed = %d", w.Code)
	}
	w := authedDo(t, rtr, http.MethodDelete, "/api/v1/cve/feed", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /cve/feed = %d, body %s", w.Code, w.Body)
	}
}

// Без активной лицензии на фичу — 402 на всех роутах, БД не трогаем.
func TestCVERequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := cveRouter(t, mgr)
	tok := authToken(t, rtr, db)

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/cve/findings"},
		{http.MethodGet, "/api/v1/cve/summary"},
		{http.MethodPost, "/api/v1/cve/scan"},
		{http.MethodPost, "/api/v1/cve/feed"},
		{http.MethodDelete, "/api/v1/cve/feed"},
	} {
		w := authedDo(t, rtr, tc.method, tc.path, []byte("[]"), tok)
		if w.Code != http.StatusPaymentRequired {
			t.Errorf("%s %s без лицензии = %d, want 402", tc.method, tc.path, w.Code)
		}
	}
}

// CVE-роуты — только it_admin (viewer → 403).
func TestCVERequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/cve/findings", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /cve/findings = %d, want 403", w.Code)
	}
}

// Некорректный severity в фильтре → 400.
func TestCVEFindingsBadSeverity(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := cveRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/cve/findings?severity=bogus", nil, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("severity=bogus = %d, want 400", w.Code)
	}
}

// /capabilities отражает лицензию на CVE-скан.
func TestCVECapability(t *testing.T) {
	mgr := licensedManager(t, []string{license.FeatureCVEScan})
	rtr, db := cveRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /capabilities = %d", w.Code)
	}
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeatureCVEScan] {
		t.Fatalf("ожидали cve_scan=true, got %+v", caps)
	}
}
