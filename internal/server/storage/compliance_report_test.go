package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// findCheck достаёт проверку по id из отчёта (или падает). Отчёт агрегирует всю общую
// тест-БД, поэтому абсолютные значения зависят от соседних тестов — сверяем структуру и
// ДЕЛЬТЫ (создали объект → счётчик сдвинулся), а не конкретные числа.
func findCheck(t *testing.T, rep storage.ComplianceReport, id string) storage.ComplianceCheck {
	t.Helper()
	for _, c := range rep.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("проверка %q отсутствует в отчёте: %+v", id, rep.Checks)
	return storage.ComplianceCheck{}
}

func TestComplianceReportStructure(t *testing.T) {
	db := newDB(t)
	rep, err := db.ComplianceReport(context.Background())
	if err != nil {
		t.Fatalf("ComplianceReport: %v", err)
	}

	if rep.Score < 0 || rep.Score > 100 {
		t.Errorf("score = %d, вне диапазона 0..100", rep.Score)
	}
	if d := time.Since(rep.GeneratedAt); d < 0 || d > time.Minute {
		t.Errorf("generated_at = %v, ожидали близко к now", rep.GeneratedAt)
	}

	// Полный набор проверок присутствует всегда, независимо от данных.
	want := []string{
		"admin_mfa", "mfa_adoption", "disk_encryption", "device_checkin",
		"device_ownership", "enrollment_backlog", "stale_admin_access",
		"software_policy", "script_policy", "audit_tamper_evident",
	}
	for _, id := range want {
		c := findCheck(t, rep, id)
		if c.Category == "" {
			t.Errorf("%s: пустая категория", id)
		}
		if c.Status != "pass" && c.Status != "warn" && c.Status != "fail" {
			t.Errorf("%s: неизвестный статус %q", id, c.Status)
		}
		if c.Passed < 0 || c.Total < 0 || c.Passed > c.Total {
			t.Errorf("%s: некорректные счётчики passed=%d total=%d", id, c.Passed, c.Total)
		}
		if c.Detail == "" {
			t.Errorf("%s: пустая деталь", id)
		}
	}
}

// Шифрование диска: активное устройство без disk_encryption увеличивает total, но не
// passed; проставили 'enabled' — passed растёт. Проверяем именно дельту, т.к. парк общий.
func TestComplianceReportDiskEncryptionDelta(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	before := findCheck(t, mustReport(t, db), "disk_encryption")

	// activeDevice (heartbeat+inventory) не проставляет disk_encryption → остаётся '' →
	// в passed не попадает, но устройство active → в total попадает.
	dev := activeDevice(t, db, "disk-"+uniq(t), "Windows 11")
	afterAdd := findCheck(t, mustReport(t, db), "disk_encryption")
	if afterAdd.Total != before.Total+1 {
		t.Fatalf("total = %d, ожидали %d (новое active-устройство)", afterAdd.Total, before.Total+1)
	}
	if afterAdd.Passed != before.Passed {
		t.Fatalf("passed = %d, ожидали без изменений %d (шифрование не подтверждено)", afterAdd.Passed, before.Passed)
	}

	if _, err := db.Pool().Exec(ctx, `UPDATE devices SET disk_encryption = 'enabled' WHERE id = $1`, dev); err != nil {
		t.Fatalf("UPDATE disk_encryption: %v", err)
	}
	afterEnc := findCheck(t, mustReport(t, db), "disk_encryption")
	if afterEnc.Passed != before.Passed+1 {
		t.Fatalf("passed = %d, ожидали %d (устройство зашифровано)", afterEnc.Passed, before.Passed+1)
	}
}

// it_admin без MFA — критично: любой такой аккаунт валит проверку в fail (порог 100%).
func TestComplianceReportAdminMFAFails(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	if _, err := db.CreateUser(ctx, "No MFA Admin", "admin-nomfa-"+uniq(t)+"@test.com", "hash", "it_admin"); err != nil {
		t.Fatalf("CreateUser it_admin: %v", err)
	}

	c := findCheck(t, mustReport(t, db), "admin_mfa")
	if c.Status != "fail" {
		t.Fatalf("admin_mfa status = %q, ожидали fail (есть админ без MFA)", c.Status)
	}
	if c.Total < 1 || c.Passed >= c.Total {
		t.Fatalf("admin_mfa passed=%d total=%d, ожидали passed<total и total>=1", c.Passed, c.Total)
	}
}

// Проверка аудита в отчёте обязана совпадать с VerifyAuditIntegrity под тем же ключом:
// ключ задан → цепочка проверяется, статус pass (цела) / fail (нарушена).
func TestComplianceReportAuditConfigured(t *testing.T) {
	t.Setenv("ROUTINEOPS_AUDIT_HMAC_KEY", "compliance-audit-key")
	db := newDB(t)
	ctx := context.Background()

	// Пара подписанных записей — чтобы цепочке было что проверять.
	for i := 0; i < 2; i++ {
		if err := db.WriteAuditLog(ctx, "", "comp-"+uniq(t), "compliance_probe", "compliance", "", map[string]any{"i": i}); err != nil {
			t.Fatalf("WriteAuditLog: %v", err)
		}
	}

	ai, err := db.VerifyAuditIntegrity(ctx, 0)
	if err != nil {
		t.Fatalf("VerifyAuditIntegrity: %v", err)
	}
	if !ai.Configured {
		t.Fatal("ожидали configured=true (ключ задан)")
	}
	want := "pass"
	if ai.Tampered {
		want = "fail"
	}

	c := findCheck(t, mustReport(t, db), "audit_tamper_evident")
	if c.Status != want {
		t.Fatalf("audit статус = %q, ожидали %q (Verify: tampered=%v)", c.Status, want, ai.Tampered)
	}
}

// Ключ не задан → tamper-evidence не настроен: проверка warn, вне скоринга (Total=0),
// отчёт при этом не падает и скор остаётся валидным.
func TestComplianceReportAuditUnconfigured(t *testing.T) {
	t.Setenv("ROUTINEOPS_AUDIT_HMAC_KEY", "")
	db := newDB(t)

	rep := mustReport(t, db)
	c := findCheck(t, rep, "audit_tamper_evident")
	if c.Status != "warn" || c.Total != 0 {
		t.Fatalf("audit (без ключа) = {status:%q total:%d}, ожидали {warn 0}", c.Status, c.Total)
	}
	if rep.Score < 0 || rep.Score > 100 {
		t.Fatalf("score = %d вне 0..100 при ненастроенном аудите", rep.Score)
	}
}

func mustReport(t *testing.T, db *storage.DB) storage.ComplianceReport {
	t.Helper()
	rep, err := db.ComplianceReport(context.Background())
	if err != nil {
		t.Fatalf("ComplianceReport: %v", err)
	}
	return rep
}
