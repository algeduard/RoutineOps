//go:build enterprise

package remediation

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/Floodww/RoutineOps/internal/server/testutil"
)

var sharedDSN string

func TestMain(m *testing.M) {
	dsn, cleanup := testutil.NewDSNWithCleanup()
	sharedDSN = dsn
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// newDB даёт чистую БД на каждый тест: ремедиатор смотрит на ВЕСЬ парк, поэтому изолируем
// устройства/задачи/правила/конфиг, чтобы точные счётчики были детерминированы.
func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Connect(context.Background(), sharedDSN)
	if err != nil {
		t.Fatalf("storage.Connect: %v", err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Pool().Exec(context.Background(),
		`TRUNCATE devices, tasks, software_policy_rules, auto_remediation_config, auto_remediation_log RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

// dirtyDevice — активное устройство (heartbeat создаёт сразу 'active') с запрещённым ПО в
// инвентаре (UpsertInventory пишет device_software по fingerprint).
func dirtyDevice(t *testing.T, db *storage.DB, name, app string) string {
	t.Helper()
	ctx := context.Background()
	fp := "fp-" + name
	if err := db.UpsertDeviceHeartbeat(ctx, storage.HeartbeatData{
		CertFingerprint: fp, CertCN: name, IPAddress: "192.0.2.20",
	}); err != nil {
		t.Fatalf("heartbeat %s: %v", name, err)
	}
	if err := db.UpsertInventory(ctx, storage.InventoryData{
		CertFingerprint: fp, Hostname: name, OS: "Windows 11", OSVersion: "1.0",
		Software: []storage.SoftwareItem{{Name: app, Version: "2.3"}},
	}); err != nil {
		t.Fatalf("inventory %s: %v", name, err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("device id %s: %v", name, err)
	}
	return id
}

func newRemediator(db *storage.DB, licensed bool) *Remediator {
	return NewRemediator(db, func() bool { return licensed }, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// countOpenRemoveTasks — сколько pending/acked remove_software-задач по паре (device, app).
func countRemoveTasks(t *testing.T, db *storage.DB, deviceID string) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM tasks WHERE device_id = $1 AND task_type = 'remove_software'`, deviceID).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}

// enabled=true (реальный режим): forbidden-нарушение → создаётся remove_software-задача и
// запись 'removed' в логе.
func TestRemediateEnabledCreatesTaskAndLog(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	app := "EvilApp"
	dev := dirtyDevice(t, db, "rem-on", app)
	if _, err := db.CreatePolicyRule(ctx, app, "forbidden", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAutoRemediationConfig(ctx, true, false); err != nil {
		t.Fatal(err)
	}

	newRemediator(db, true).tick()

	if got := countRemoveTasks(t, db, dev); got != 1 {
		t.Fatalf("ожидали 1 remove_software-задачу, got %d", got)
	}
	entries, _ := db.ListRemediationLog(ctx, 200)
	if len(entries) != 1 || entries[0].Action != "removed" || entries[0].TaskID == "" {
		t.Fatalf("ожидали 1 removed-запись с task_id, got %+v", entries)
	}

	// Дедуп: повторный тик НЕ плодит вторую задачу (незавершённая уже висит).
	newRemediator(db, true).tick()
	if got := countRemoveTasks(t, db, dev); got != 1 {
		t.Fatalf("после повторного тика ожидали всё ещё 1 задачу (дедуп), got %d", got)
	}
}

// Cooldown-дедуп: задача удаления ушла в ТЕРМИНАЛЬНЫЙ статус (failed — напр. ПО удалить нельзя),
// а нарушение осталось в инвентаре. Открытой задачи больше нет, но ремедиатор НЕ плодит новую на
// каждом тике — держит cooldown по недавней 'removed'-записи; повтор только за пределами окна.
func TestRemediateCooldownAfterTerminalTask(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	app := "EvilApp"
	dev := dirtyDevice(t, db, "rem-cool", app)
	if _, err := db.CreatePolicyRule(ctx, app, "forbidden", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAutoRemediationConfig(ctx, true, false); err != nil {
		t.Fatal(err)
	}

	// Первый тик создаёт задачу.
	newRemediator(db, true).tick()
	if got := countRemoveTasks(t, db, dev); got != 1 {
		t.Fatalf("ожидали 1 задачу после первого тика, got %d", got)
	}
	// Задача уходит в failed (ПО удалить не удалось), но device_software не очищен.
	if _, err := db.Pool().Exec(ctx,
		`UPDATE tasks SET status = 'failed' WHERE device_id = $1 AND task_type = 'remove_software'`, dev); err != nil {
		t.Fatal(err)
	}

	// Тик в пределах cooldown: открытой задачи нет, но недавняя 'removed'-запись держит cooldown —
	// второй задачи НЕ создаём (иначе спам на каждом тике для неустранимого ПО).
	newRemediator(db, true).tick()
	if got := countRemoveTasks(t, db, dev); got != 1 {
		t.Fatalf("в пределах cooldown ожидали всё ещё 1 задачу, got %d", got)
	}

	// Сдвигаем лог за пределы cooldown (6ч) → повторная попытка разрешена.
	if _, err := db.Pool().Exec(ctx,
		`UPDATE auto_remediation_log SET created_at = now() - interval '7 hours' WHERE device_id = $1 AND action = 'removed'`, dev); err != nil {
		t.Fatal(err)
	}
	newRemediator(db, true).tick()
	if got := countRemoveTasks(t, db, dev); got != 2 {
		t.Fatalf("за пределами cooldown ожидали повторную задачу (2), got %d", got)
	}
}

// enabled=false: ничего не делаем (задач и лога нет).
func TestRemediateDisabledDoesNothing(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	app := "EvilApp"
	dev := dirtyDevice(t, db, "rem-off", app)
	if _, err := db.CreatePolicyRule(ctx, app, "forbidden", nil, nil); err != nil {
		t.Fatal(err)
	}
	// конфиг не трогаем — дефолт выключен.

	newRemediator(db, true).tick()

	if got := countRemoveTasks(t, db, dev); got != 0 {
		t.Fatalf("при выключенном авто-устранении задач быть не должно, got %d", got)
	}
	entries, _ := db.ListRemediationLog(ctx, 200)
	if len(entries) != 0 {
		t.Fatalf("лог должен быть пуст, got %+v", entries)
	}
}

// dry_run: запись 'dry_run' в логе, но задача НЕ создаётся; повторный тик не дублирует лог.
func TestRemediateDryRunLogsWithoutTask(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	app := "EvilApp"
	dev := dirtyDevice(t, db, "rem-dry", app)
	if _, err := db.CreatePolicyRule(ctx, app, "forbidden", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAutoRemediationConfig(ctx, true, true); err != nil {
		t.Fatal(err)
	}

	newRemediator(db, true).tick()

	if got := countRemoveTasks(t, db, dev); got != 0 {
		t.Fatalf("в dry_run задач быть не должно, got %d", got)
	}
	entries, _ := db.ListRemediationLog(ctx, 200)
	if len(entries) != 1 || entries[0].Action != "dry_run" || entries[0].TaskID != "" {
		t.Fatalf("ожидали 1 dry_run-запись без task_id, got %+v", entries)
	}

	// Дедуп dry_run-логирования: повторный тик не пишет ту же строку снова.
	newRemediator(db, true).tick()
	entries, _ = db.ListRemediationLog(ctx, 200)
	if len(entries) != 1 {
		t.Fatalf("после повторного тика ожидали всё ещё 1 dry_run-запись, got %d", len(entries))
	}
}

// Без лицензии тик пуст даже при enabled=true.
func TestRemediateUnlicensedNoop(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	app := "EvilApp"
	dev := dirtyDevice(t, db, "rem-unl", app)
	if _, err := db.CreatePolicyRule(ctx, app, "forbidden", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAutoRemediationConfig(ctx, true, false); err != nil {
		t.Fatal(err)
	}

	newRemediator(db, false).tick() // licensed=false

	if got := countRemoveTasks(t, db, dev); got != 0 {
		t.Fatalf("без лицензии тик должен быть пуст, got %d задач", got)
	}
}
