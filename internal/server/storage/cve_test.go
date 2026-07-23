package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func cvssPtr(v float64) *float64 { return &v }

// cveDevice — активное (не-pending) устройство с ЯВНЫМИ версиями ПО. Идём через
// heartbeat + UpsertInventory (device_software пишется только инвентарём, матчится по
// fingerprint) — как activeDevice в compliance_test, но с контролем версии каждого пакета.
// IP из TEST-NET-1 (192.0.2.0/24) — синтетика для leak-guard.
func cveDevice(t *testing.T, db *storage.DB, name, os string, sw ...storage.SoftwareItem) string {
	t.Helper()
	ctx := context.Background()
	fp := "cvefp-" + name
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, name, name, "192.0.2.10")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat %s: %v", name, err)
	}
	if err := db.UpsertInventory(ctx, storage.InventoryData{
		CertFingerprint: fp, Hostname: name, OS: os, OSVersion: "1.0", Software: sw,
	}); err != nil {
		t.Fatalf("UpsertInventory %s: %v", name, err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("GetDeviceIDByFingerprint %s: id=%q err=%v", name, id, err)
	}
	return id
}

// findingsFor фильтрует находки конкретного устройства (пакет делит одну БД, в находках
// после скана лежит весь парк — свои отбираем по device_id).
func findingsFor(t *testing.T, db *storage.DB, deviceID string) []storage.CVEFinding {
	t.Helper()
	f, err := db.ListCVEFindings(context.Background(), deviceID, "")
	if err != nil {
		t.Fatalf("ListCVEFindings(%s): %v", deviceID, err)
	}
	return f
}

// Ядро фичи: заливаем фид с уязвимой версией, ставим устройство с этим ПО этой версии,
// сканируем → находка появилась; устройство с исправленной версией → находки нет.
func TestCVEScanMatch(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	product := "vulnapp-" + suffix
	cve := "CVE-2024-" + suffix

	n, err := db.LoadCVEFeed(ctx, []storage.CVEFeedEntry{{
		CVEID: cve, Product: product, VersionConstraint: "<2.0.0",
		Severity: "high", CVSS: cvssPtr(7.5), Summary: "тестовая уязвимость",
	}})
	if err != nil {
		t.Fatalf("LoadCVEFeed: %v", err)
	}
	if n != 1 {
		t.Fatalf("loaded = %d, want 1", n)
	}

	// Продукт уникален по suffix — только наши устройства могут совпасть, чужой инвентарь мимо.
	vuln := cveDevice(t, db, "cvuln-"+suffix, "Windows 11", storage.SoftwareItem{Name: product, Version: "1.5.0"})
	safe := cveDevice(t, db, "csafe-"+suffix, "Windows 11", storage.SoftwareItem{Name: product, Version: "2.0.0"})

	if _, err := db.ScanCVE(ctx); err != nil {
		t.Fatalf("ScanCVE: %v", err)
	}

	vf := findingsFor(t, db, vuln)
	if len(vf) != 1 {
		t.Fatalf("уязвимое устройство: %d находок, want 1: %+v", len(vf), vf)
	}
	got := vf[0]
	if got.CVEID != cve || got.Product != product || got.InstalledVersion != "1.5.0" || got.Severity != "high" {
		t.Errorf("находка неверна: %+v", got)
	}
	if got.CVSS == nil || *got.CVSS != 7.5 {
		t.Errorf("cvss = %v, want 7.5", got.CVSS)
	}

	if sf := findingsFor(t, db, safe); len(sf) != 0 {
		t.Fatalf("исправленное устройство (2.0.0 не < 2.0.0): %d находок, want 0: %+v", len(sf), sf)
	}
}

// Матчинг имени — регистронезависимая подстрока: фид "chrome" ловит инвентарную
// "Google Chrome". Ограничение '*' → уязвима любая версия.
func TestCVEScanSubstringAndWildcard(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	// Уникальный «бренд» в имени, чтобы подстрока не цепляла чужой инвентарь.
	installedName := "MegaBrowser " + suffix + " 120"
	if _, err := db.LoadCVEFeed(ctx, []storage.CVEFeedEntry{{
		CVEID: "CVE-9000-" + suffix, Product: "megabrowser " + suffix, VersionConstraint: "*",
		Severity: "critical",
	}}); err != nil {
		t.Fatalf("LoadCVEFeed: %v", err)
	}
	dev := cveDevice(t, db, "csub-"+suffix, "Windows 11", storage.SoftwareItem{Name: installedName, Version: "120.0"})

	if _, err := db.ScanCVE(ctx); err != nil {
		t.Fatalf("ScanCVE: %v", err)
	}
	f := findingsFor(t, db, dev)
	if len(f) != 1 || f[0].Severity != "critical" || f[0].Product != installedName {
		t.Fatalf("ожидали одну critical-находку с product=%q, got %+v", installedName, f)
	}
}

// Скан на пустом фиде не падает и обнуляет находки. Пересборка: после скана с находкой
// повторный скан по очищенному фиду убирает её.
func TestCVEScanEmptyFeedClearsFindings(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	product := "clearapp-" + suffix

	if _, err := db.LoadCVEFeed(ctx, []storage.CVEFeedEntry{{
		CVEID: "CVE-1-" + suffix, Product: product, VersionConstraint: "", Severity: "medium",
	}}); err != nil {
		t.Fatalf("LoadCVEFeed: %v", err)
	}
	dev := cveDevice(t, db, "cclr-"+suffix, "Windows 11", storage.SoftwareItem{Name: product, Version: "1.0"})
	if _, err := db.ScanCVE(ctx); err != nil {
		t.Fatalf("ScanCVE: %v", err)
	}
	if len(findingsFor(t, db, dev)) != 1 {
		t.Fatalf("до очистки ожидали 1 находку")
	}

	cleared, err := db.ClearCVEFeed(ctx)
	if err != nil {
		t.Fatalf("ClearCVEFeed: %v", err)
	}
	if cleared < 1 {
		t.Fatalf("cleared = %d, want >= 1", cleared)
	}
	cnt, err := db.ScanCVE(ctx) // пустой фид → находки обнуляются, без паники
	if err != nil {
		t.Fatalf("ScanCVE (пустой фид): %v", err)
	}
	if cnt != 0 {
		t.Fatalf("скан по пустому фиду = %d находок, want 0", cnt)
	}
	if len(findingsFor(t, db, dev)) != 0 {
		t.Fatalf("после очистки фида находки устройства должны исчезнуть")
	}
}

// LoadCVEFeed = ЗАМЕНА целиком, а не добавление; строки без cve_id/product отсекаются.
func TestCVEFeedReplaceAndValidate(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)

	n, err := db.LoadCVEFeed(ctx, []storage.CVEFeedEntry{
		{CVEID: "CVE-A-" + suffix, Product: "app-a-" + suffix},
		{CVEID: "", Product: "no-cve-" + suffix}, // пропустится
		{CVEID: "CVE-B-" + suffix, Product: ""},  // пропустится
		{CVEID: "CVE-C-" + suffix, Product: "app-c-" + suffix},
	})
	if err != nil {
		t.Fatalf("LoadCVEFeed: %v", err)
	}
	if n != 2 {
		t.Fatalf("loaded = %d, want 2 (две мусорных строки отсеяны)", n)
	}

	// Повторная заливка ЗАМЕНЯЕТ: одна запись → в фиде ровно одна.
	if _, err := db.LoadCVEFeed(ctx, []storage.CVEFeedEntry{
		{CVEID: "CVE-D-" + suffix, Product: "app-d-" + suffix},
	}); err != nil {
		t.Fatalf("LoadCVEFeed replace: %v", err)
	}
	total, err := db.CVEFeedCount(ctx)
	if err != nil {
		t.Fatalf("CVEFeedCount: %v", err)
	}
	if total != 1 {
		t.Fatalf("после замены в фиде %d записей, want 1", total)
	}
}

// Сводка: по severity фиксированный порядок critical→low с нулями; затронутое устройство
// присутствует с верной разбивкой. Фид заменён под уникальные продукты, поэтому находки —
// только наши, и суммарные счётчики детерминированы.
func TestCVESummary(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	critApp := "sumcrit-" + suffix
	highApp := "sumhigh-" + suffix

	if _, err := db.LoadCVEFeed(ctx, []storage.CVEFeedEntry{
		{CVEID: "CVE-CR-" + suffix, Product: critApp, VersionConstraint: "*", Severity: "critical"},
		{CVEID: "CVE-HI-" + suffix, Product: highApp, VersionConstraint: "*", Severity: "high"},
	}); err != nil {
		t.Fatalf("LoadCVEFeed: %v", err)
	}
	dev := cveDevice(t, db, "csum-"+suffix, "Windows 11",
		storage.SoftwareItem{Name: critApp, Version: "1.0"},
		storage.SoftwareItem{Name: highApp, Version: "1.0"})

	if _, err := db.ScanCVE(ctx); err != nil {
		t.Fatalf("ScanCVE: %v", err)
	}

	sum, err := db.CVESummaryData(ctx)
	if err != nil {
		t.Fatalf("CVESummaryData: %v", err)
	}
	if sum.FeedCount != 2 {
		t.Errorf("feed_count = %d, want 2", sum.FeedCount)
	}
	if sum.TotalFindings != 2 {
		t.Errorf("total_findings = %d, want 2", sum.TotalFindings)
	}
	if sum.AffectedDevices != 1 {
		t.Errorf("affected_devices = %d, want 1", sum.AffectedDevices)
	}
	// Фиксированный порядок severity.
	wantOrder := []string{"critical", "high", "medium", "low"}
	if len(sum.BySeverity) != 4 {
		t.Fatalf("by_severity len = %d, want 4", len(sum.BySeverity))
	}
	for i, s := range wantOrder {
		if sum.BySeverity[i].Severity != s {
			t.Errorf("by_severity[%d] = %q, want %q", i, sum.BySeverity[i].Severity, s)
		}
	}
	sevCount := map[string]int{}
	for _, s := range sum.BySeverity {
		sevCount[s.Severity] = s.Count
	}
	if sevCount["critical"] != 1 || sevCount["high"] != 1 || sevCount["medium"] != 0 || sevCount["low"] != 0 {
		t.Errorf("severity counts = %+v, want critical:1 high:1 medium:0 low:0", sevCount)
	}
	// Наше устройство в разрезе по устройствам.
	var row *storage.CVEDeviceCount
	for i := range sum.ByDevice {
		if sum.ByDevice[i].DeviceID == dev {
			row = &sum.ByDevice[i]
		}
	}
	if row == nil {
		t.Fatalf("устройство %s отсутствует в by_device: %+v", dev, sum.ByDevice)
	}
	if row.Count != 2 || row.Critical != 1 || row.High != 1 {
		t.Errorf("by_device row = %+v, want count:2 critical:1 high:1", *row)
	}
}

// Фильтр по severity в списке находок.
func TestCVEFindingsSeverityFilter(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	critApp := "fcrit-" + suffix
	lowApp := "flow-" + suffix

	if _, err := db.LoadCVEFeed(ctx, []storage.CVEFeedEntry{
		{CVEID: "CVE-FC-" + suffix, Product: critApp, VersionConstraint: "*", Severity: "critical"},
		{CVEID: "CVE-FL-" + suffix, Product: lowApp, VersionConstraint: "*", Severity: "low"},
	}); err != nil {
		t.Fatalf("LoadCVEFeed: %v", err)
	}
	dev := cveDevice(t, db, "cfilt-"+suffix, "Windows 11",
		storage.SoftwareItem{Name: critApp, Version: "1.0"},
		storage.SoftwareItem{Name: lowApp, Version: "1.0"})
	if _, err := db.ScanCVE(ctx); err != nil {
		t.Fatalf("ScanCVE: %v", err)
	}

	all := findingsFor(t, db, dev)
	if len(all) != 2 {
		t.Fatalf("без фильтра %d находок, want 2", len(all))
	}
	crit, err := db.ListCVEFindings(ctx, dev, "critical")
	if err != nil {
		t.Fatalf("ListCVEFindings critical: %v", err)
	}
	if len(crit) != 1 || crit[0].Severity != "critical" {
		t.Fatalf("фильтр critical: %+v", crit)
	}
}
