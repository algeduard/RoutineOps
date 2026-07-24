package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// collectReport собирает все строки стрим-отчёта в слайс (для проверок в тесте).
func collectReport(t *testing.T, run func(emit func([]string) error) error) [][]string {
	t.Helper()
	var out [][]string
	if err := run(func(rec []string) error {
		cp := append([]string(nil), rec...)
		out = append(out, cp)
		return nil
	}); err != nil {
		t.Fatalf("stream report: %v", err)
	}
	return out
}

func TestStreamDeviceReport(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	host := "devrep-" + uniq(t)

	// Активное устройство с инвентарём, чтобы попасть в парк.
	fp := "devrepfp-" + host
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, host, host, "192.0.2.30")); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := db.UpsertInventory(ctx, storageInventoryData(fp, host, "Windows 11", "23H2", nil)); err != nil {
		t.Fatalf("inventory: %v", err)
	}

	rows := collectReport(t, func(emit func([]string) error) error {
		return db.StreamDeviceReport(ctx, emit)
	})

	found := false
	for _, r := range rows {
		if len(r) != 9 {
			t.Fatalf("device row has %d cols, want 9: %+v", len(r), r)
		}
		if r[0] == host {
			found = true
			if r[1] != "Windows 11" {
				t.Errorf("os = %q, want Windows 11", r[1])
			}
		}
	}
	if !found {
		t.Fatalf("device %q not in report", host)
	}
}

func TestStreamSoftwareInventoryReport(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	host := "swrep-" + uniq(t)
	swName := "ReportPkg-" + uniq(t)

	fp := "swrepfp-" + host
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, host, host, "192.0.2.31")); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := db.UpsertInventory(ctx, storage.InventoryData{
		CertFingerprint: fp, Hostname: host, OS: "macOS", OSVersion: "14",
		Software: []storage.SoftwareItem{{Name: swName, Version: "3.2.1"}},
	}); err != nil {
		t.Fatalf("inventory: %v", err)
	}

	rows := collectReport(t, func(emit func([]string) error) error {
		return db.StreamSoftwareInventoryReport(ctx, emit)
	})

	found := false
	for _, r := range rows {
		if len(r) != 3 {
			t.Fatalf("inventory row has %d cols, want 3: %+v", len(r), r)
		}
		if r[0] == swName {
			found = true
			if r[1] != "3.2.1" {
				t.Errorf("version = %q, want 3.2.1", r[1])
			}
			if r[2] != "1" {
				t.Errorf("device_count = %q, want 1", r[2])
			}
		}
	}
	if !found {
		t.Fatalf("software %q not in inventory report", swName)
	}
}

func TestStreamAuditReportPeriodFilter(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	action := "report_test_action_" + uniq(t)

	// Пишем запись аудита «сейчас» (created_at = now() внутри WriteAuditLog).
	before := time.Now().Add(-time.Hour)
	if err := db.WriteAuditLog(ctx, "", "audit-rep@test.com", action, "report", "x", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("WriteAuditLog: %v", err)
	}
	after := time.Now().Add(time.Hour)

	countAction := func(from, to time.Time) int {
		rows := collectReport(t, func(emit func([]string) error) error {
			return db.StreamAuditReport(ctx, from, to, emit)
		})
		n := 0
		for _, r := range rows {
			if len(r) != 6 {
				t.Fatalf("audit row has %d cols, want 6: %+v", len(r), r)
			}
			if r[2] == action {
				n++
			}
		}
		return n
	}

	// Широкое окно [before, after) — запись попадает.
	if got := countAction(before, after); got != 1 {
		t.Fatalf("wide window: got %d rows for action, want 1", got)
	}
	// Окно целиком в прошлом [before, before+1s) — запись НЕ попадает (created_at позже).
	if got := countAction(before, before.Add(time.Second)); got != 0 {
		t.Fatalf("past window: got %d rows for action, want 0", got)
	}
	// Окно целиком в будущем [after, after+1h) — запись НЕ попадает.
	if got := countAction(after, after.Add(time.Hour)); got != 0 {
		t.Fatalf("future window: got %d rows for action, want 0", got)
	}
}

func TestStreamAlertsReport(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	host := "alrep-" + uniq(t)

	d := mustCreateActiveDevice(t, db, host, "Windows")
	details := "alert-detail-" + uniq(t)
	if _, err := db.CreateAlert(ctx, d.ID, "forbidden_software", details, ""); err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}

	rows := collectReport(t, func(emit func([]string) error) error {
		return db.StreamAlertsReport(ctx, emit)
	})

	found := false
	for _, r := range rows {
		if len(r) != 6 {
			t.Fatalf("alert row has %d cols, want 6: %+v", len(r), r)
		}
		if r[3] == details {
			found = true
			if r[1] != "forbidden_software" {
				t.Errorf("alert_type = %q, want forbidden_software", r[1])
			}
			if r[2] != "critical" { // severityForAlertType(forbidden_software) = critical
				t.Errorf("severity = %q, want critical", r[2])
			}
		}
	}
	if !found {
		t.Fatalf("alert with details %q not in report", details)
	}
}
