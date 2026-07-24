package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// remediationDevice — активное устройство с инвентарём ПО (как compliance-тесты): идём через
// heartbeat (создаёт устройство сразу в 'active' с fingerprint/cert_cn) + UpsertInventory
// (пишет device_software, матчится по fingerprint). Просто CreatePendingDevice не годится:
// он оставляет 'pending', а device_software без инвентаря пуст.
func remediationDevice(t *testing.T, db *storage.DB, name, os string, software ...string) string {
	t.Helper()
	ctx := context.Background()
	fp := "fp-" + name
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, name, name, "192.0.2.10")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat %s: %v", name, err)
	}
	if err := db.UpsertInventory(ctx, storageInventoryData(fp, name, os, "1.0", software)); err != nil {
		t.Fatalf("UpsertInventory %s: %v", name, err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("GetDeviceIDByFingerprint %s: %v", name, err)
	}
	return id
}

// findViolation ищет в срезе нарушение по (device, software) — ListForbiddenViolations
// смотрит на ВЕСЬ парк (тесты делят БД), поэтому проверяем присутствие своей пары, а не count.
func findViolation(vs []storage.ForbiddenViolation, deviceID, software string) bool {
	for _, v := range vs {
		if v.DeviceID == deviceID && v.SoftwareName == software {
			return true
		}
	}
	return false
}

func TestAutoRemediationConfigGetSet(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	// Дефолт — авто-устранение выключено (строки конфига ещё нет).
	cfg, err := db.GetAutoRemediationConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.DryRun {
		t.Fatalf("дефолт должен быть выключен: %+v", cfg)
	}

	// Включаем в dry_run.
	if err := db.SetAutoRemediationConfig(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	cfg, _ = db.GetAutoRemediationConfig(ctx)
	if !cfg.Enabled || !cfg.DryRun {
		t.Fatalf("после включения dry_run: %+v", cfg)
	}
	if cfg.UpdatedAt.IsZero() {
		t.Fatal("updated_at не проставлен")
	}

	// Переключаем в реальный режим (singleton апсертится, не плодит строк).
	if err := db.SetAutoRemediationConfig(ctx, true, false); err != nil {
		t.Fatal(err)
	}
	cfg, _ = db.GetAutoRemediationConfig(ctx)
	if !cfg.Enabled || cfg.DryRun {
		t.Fatalf("после переключения в реальный режим: %+v", cfg)
	}
}

func TestListForbiddenViolations(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	app := "BadApp-" + suffix

	// Нарушитель: активное устройство с запрещённым ПО в инвентаре.
	dirty := remediationDevice(t, db, "arm-dirty-"+suffix, "Windows 11", app, "Good App "+suffix)
	// Чистое устройство: то же правило, но ПО не установлено.
	clean := remediationDevice(t, db, "arm-clean-"+suffix, "Windows 11", "Good App "+suffix)

	// Глобальное forbidden-правило на это ПО.
	if _, err := db.CreatePolicyRule(ctx, app, "forbidden", nil, nil); err != nil {
		t.Fatalf("CreatePolicyRule: %v", err)
	}

	vs, err := db.ListForbiddenViolations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !findViolation(vs, dirty, app) {
		t.Fatalf("нарушение на dirty-устройстве не найдено: %+v", vs)
	}
	if findViolation(vs, clean, app) {
		t.Fatal("чистое устройство ошибочно помечено нарушителем")
	}

	// allowed-правило нарушением НЕ считается.
	allowApp := "AllowApp-" + suffix
	allowDev := remediationDevice(t, db, "arm-allow-"+suffix, "Windows 11", allowApp)
	if _, err := db.CreatePolicyRule(ctx, allowApp, "allowed", nil, nil); err != nil {
		t.Fatalf("CreatePolicyRule allowed: %v", err)
	}
	vs, _ = db.ListForbiddenViolations(ctx)
	if findViolation(vs, allowDev, allowApp) {
		t.Fatal("allowed-правило не должно давать нарушение")
	}
}

func TestForbiddenViolationsExcludeNonActive(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	app := "BlockedBad-" + suffix

	dev := remediationDevice(t, db, "arm-blk-"+suffix, "Windows 11", app)
	if _, err := db.CreatePolicyRule(ctx, app, "forbidden", nil, nil); err != nil {
		t.Fatalf("CreatePolicyRule: %v", err)
	}
	// Пока active — нарушение видно.
	vs, _ := db.ListForbiddenViolations(ctx)
	if !findViolation(vs, dev, app) {
		t.Fatalf("active-устройство должно нарушать: %+v", vs)
	}
	// Блокируем — устройство выпадает из выборки (задачу удаления оно не примет).
	if err := db.UpdateDeviceStatus(ctx, dev, "blocked"); err != nil {
		t.Fatal(err)
	}
	vs, _ = db.ListForbiddenViolations(ctx)
	if findViolation(vs, dev, app) {
		t.Fatal("не-active устройство не должно быть в нарушениях")
	}
}

func TestHasOpenRemoveSoftwareTask(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	app := "DedupApp-" + suffix
	dev := remediationDevice(t, db, "arm-dedup-"+suffix, "Windows 11", app)

	open, err := db.HasOpenRemoveSoftwareTask(ctx, dev, app)
	if err != nil {
		t.Fatal(err)
	}
	if open {
		t.Fatal("задач ещё нет — не должно быть открытой")
	}

	task, err := db.CreateRemoveSoftwareTask(ctx, dev, app, "1.0")
	if err != nil {
		t.Fatalf("CreateRemoveSoftwareTask: %v", err)
	}
	open, _ = db.HasOpenRemoveSoftwareTask(ctx, dev, app)
	if !open {
		t.Fatal("pending remove_software-задача должна считаться открытой")
	}
	// Другое ПО на том же устройстве — не считается открытой для этой пары.
	other, _ := db.HasOpenRemoveSoftwareTask(ctx, dev, "OtherApp-"+suffix)
	if other {
		t.Fatal("дедуп не должен срабатывать на другое ПО")
	}

	// Завершаем задачу → дедуп снимается (переустановка ПО сможет снова триггерить).
	if _, _, err := db.CompleteTask(ctx, task.ID, dev, "completed", "ok", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	open, _ = db.HasOpenRemoveSoftwareTask(ctx, dev, app)
	if open {
		t.Fatal("завершённая задача не должна считаться открытой")
	}
}

func TestRemediationLog(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	app := "LogApp-" + suffix
	dev := remediationDevice(t, db, "arm-log-"+suffix, "Windows 11", app)

	// dry_run-запись: task_id пуст, дедуп dry_run-лога срабатывает.
	if has, _ := db.HasDryRunRemediationLog(ctx, dev, app); has {
		t.Fatal("dry_run-лога ещё быть не должно")
	}
	if _, err := db.AddRemediationLog(ctx, dev, app, "", "dry_run"); err != nil {
		t.Fatalf("AddRemediationLog dry_run: %v", err)
	}
	if has, _ := db.HasDryRunRemediationLog(ctx, dev, app); !has {
		t.Fatal("dry_run-лог должен обнаруживаться после записи")
	}

	// removed-запись с task_id.
	task, err := db.CreateRemoveSoftwareTask(ctx, dev, app, "1.0")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := db.AddRemediationLog(ctx, dev, app, task.ID, "removed")
	if err != nil {
		t.Fatalf("AddRemediationLog removed: %v", err)
	}
	if entry.TaskID != task.ID || entry.Action != "removed" {
		t.Fatalf("removed-запись неверна: %+v", entry)
	}

	entries, err := db.ListRemediationLog(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	var seenRemoved, seenDry bool
	for _, e := range entries {
		if e.DeviceID != dev || e.SoftwareName != app {
			continue
		}
		switch e.Action {
		case "removed":
			seenRemoved = true
			if e.Hostname == "" {
				t.Fatal("hostname должен подтягиваться джойном")
			}
		case "dry_run":
			seenDry = true
			if e.TaskID != "" {
				t.Fatal("у dry_run-записи task_id должен быть пуст")
			}
		}
	}
	if !seenRemoved || !seenDry {
		t.Fatalf("ожидали в логе обе записи (removed+dry_run), got removed=%v dry=%v", seenRemoved, seenDry)
	}
}
