package storage_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func TestCreateTask_ReturnsTask(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-task-%s", uniq(t)), "macos")

	task, err := db.CreateTask(context.Background(), d.ID, "echo hello", "macos", "normal")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.ID == "" {
		t.Error("expected task ID")
	}
	if task.Status != "pending" {
		t.Errorf("status = %q, want pending", task.Status)
	}
	if task.DeviceID != d.ID {
		t.Errorf("device_id = %q, want %q", task.DeviceID, d.ID)
	}
}

func TestGetTask_Found(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-gettask-%s", uniq(t)), "windows")
	created, _ := db.CreateTask(context.Background(), d.ID, "ipconfig", "windows", "normal")

	got, err := db.GetTask(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got == nil {
		t.Fatal("got nil task")
	}
	if got.ID != created.ID {
		t.Errorf("id = %q, want %q", got.ID, created.ID)
	}
}

func TestGetTask_NotFound_ReturnsNil(t *testing.T) {
	db := newDB(t)
	got, err := db.GetTask(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestGetPendingTasks_ReturnsPendingOnly(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-pending-%s", uniq(t)), "macos")

	t1, _ := db.CreateTask(context.Background(), d.ID, "task1", "macos", "normal")
	t2, _ := db.CreateTask(context.Background(), d.ID, "task2", "macos", "normal")

	// ack t1 so it's no longer pending
	_ = db.AckTask(context.Background(), t1.ID, d.ID)

	tasks, err := db.GetPendingTasks(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetPendingTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d pending tasks, want 1", len(tasks))
	}
	if tasks[0].ID != t2.ID {
		t.Errorf("pending task id = %q, want %q", tasks[0].ID, t2.ID)
	}
}

func TestAckTask_ChangesStatusToAcked(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-ack-%s", uniq(t)), "macos")
	task, _ := db.CreateTask(context.Background(), d.ID, "ls", "macos", "normal")

	if err := db.AckTask(context.Background(), task.ID, d.ID); err != nil {
		t.Fatalf("AckTask: %v", err)
	}
	got, _ := db.GetTask(context.Background(), task.ID)
	if got.Status != "acked" {
		t.Errorf("status = %q, want acked", got.Status)
	}
}

func TestCompleteTask_Success(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-complete-%s", uniq(t)), "windows")
	task, _ := db.CreateTask(context.Background(), d.ID, "dir", "windows", "normal")

	if err := db.CompleteTask(context.Background(), task.ID, d.ID, "completed", "output text", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	got, _ := db.GetTask(context.Background(), task.ID)
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.Output == nil || *got.Output != "output text" {
		t.Errorf("output = %v, want 'output text'", got.Output)
	}
}

func TestListDeviceTasks_ReturnsMostRecent(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-listtasks-%s", uniq(t)), "macos")
	db.CreateTask(context.Background(), d.ID, "cmd1", "macos", "normal")
	db.CreateTask(context.Background(), d.ID, "cmd2", "macos", "normal")

	tasks, err := db.ListDeviceTasks(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("ListDeviceTasks: %v", err)
	}
	if len(tasks) < 2 {
		t.Errorf("got %d tasks, want >=2", len(tasks))
	}
}

func TestCreateLockTask_ReturnsLockTask(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, "host-lock-"+uniq(t), "windows")
	task, err := db.CreateLockTask(context.Background(), d.ID, "$2a$10$hash", "нарушение ИБ", false, "overlay")
	if err != nil {
		t.Fatalf("CreateLockTask: %v", err)
	}
	if task.ID == "" {
		t.Error("expected task ID")
	}
	if task.TaskType != "lock" {
		t.Errorf("TaskType = %q, want lock", task.TaskType)
	}
	if task.LockHash != "$2a$10$hash" {
		t.Errorf("LockHash = %q, want $2a$10$hash", task.LockHash)
	}
	if task.LockReason != "нарушение ИБ" {
		t.Errorf("LockReason = %q, want нарушение ИБ", task.LockReason)
	}
	if task.LockUnlock != false {
		t.Errorf("LockUnlock = %v, want false", task.LockUnlock)
	}
	if task.Status != "pending" {
		t.Errorf("Status = %q, want pending", task.Status)
	}
}

func TestCreateLockTask_Unlock(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, "host-unlock-"+uniq(t), "windows")
	task, err := db.CreateLockTask(context.Background(), d.ID, "", "", true, "overlay")
	if err != nil {
		t.Fatalf("CreateLockTask: %v", err)
	}
	if task.LockUnlock != true {
		t.Errorf("LockUnlock = %v, want true", task.LockUnlock)
	}
}

// Регресс: platform lock-задачи должен браться из os устройства, а не быть
// захардкожен "windows" — иначе задачи на блок мака помечались как windows.
func TestCreateLockTask_PlatformFromDeviceOS(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, "host-lock-mac-"+uniq(t), "darwin")
	task, err := db.CreateLockTask(context.Background(), d.ID, "$2a$10$hash", "тест", false, "overlay")
	if err != nil {
		t.Fatalf("CreateLockTask: %v", err)
	}
	if task.Platform != "darwin" {
		t.Errorf("Platform = %q, want darwin (os устройства)", task.Platform)
	}
}

func TestUpdateDeviceLockStatus_LockedThenUnlocked(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	d := mustCreateDevice(t, db, "host-updatelock-"+uniq(t), "windows")

	if err := db.UpdateDeviceLockStatus(ctx, d.ID, "locked"); err != nil {
		t.Fatalf("UpdateDeviceLockStatus(locked): %v", err)
	}
	d1, _, err := db.GetDevice(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if d1.LockStatus != "locked" {
		t.Errorf("LockStatus = %q, want locked", d1.LockStatus)
	}

	if err := db.UpdateDeviceLockStatus(ctx, d.ID, "unlocked"); err != nil {
		t.Fatalf("UpdateDeviceLockStatus(unlocked): %v", err)
	}
	d2, _, err := db.GetDevice(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if d2.LockStatus != "unlocked" {
		t.Errorf("LockStatus = %q, want unlocked", d2.LockStatus)
	}
}

// Sweep застрявших в 'acked' задач. Порог проверяется самим параметром, без правки
// acked_at в обход API: 15 мин — свежая задача НЕ трогается, 0 — трогается.
// Главное здесь — лок-задача не должна попадать под sweep НИКОГДА: она штатно висит
// в 'acked' (агент отчитывается через ReportLockStatus), и без исключения по task_type
// каждый лок получал бы ложный failed.
func TestFailStaleAckedTasks(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-stale-%s", uniq(t)), "windows")

	script, err := db.CreateTask(ctx, d.ID, "whoami", "windows", "normal")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	lock, err := db.CreateLockTask(ctx, d.ID, "hash", "по требованию ИБ", false, storage.LockModeOverlay)
	if err != nil {
		t.Fatalf("CreateLockTask: %v", err)
	}
	pending, err := db.CreateTask(ctx, d.ID, "ipconfig", "windows", "normal")
	if err != nil {
		t.Fatalf("CreateTask(pending): %v", err)
	}
	for _, id := range []string{script.ID, lock.ID} {
		if err := db.AckTask(ctx, id, d.ID); err != nil {
			t.Fatalf("AckTask(%s): %v", id, err)
		}
	}

	statusOf := func(id string) string {
		t.Helper()
		got, err := db.GetTask(ctx, id)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		return got.Status
	}

	// Порог не истёк — не трогаем ничего.
	if _, err := db.FailStaleAckedTasks(ctx, storage.StaleAckedTimeoutMinutes); err != nil {
		t.Fatalf("FailStaleAckedTasks(15m): %v", err)
	}
	if s := statusOf(script.ID); s != "acked" {
		t.Errorf("свежая задача в пределах порога = %q, want acked", s)
	}

	// Порог истёк.
	// Счётчик проверяем на «хотя бы одну», а не на точное число: БД в пакете общая,
	// и соседние тесты оставляют в 'acked' свои задачи. Точность даёт проверка
	// статусов по конкретным id ниже.
	n, err := db.FailStaleAckedTasks(ctx, 0)
	if err != nil {
		t.Fatalf("FailStaleAckedTasks(0): %v", err)
	}
	if n < 1 {
		t.Errorf("закрыто задач = %d, want >= 1", n)
	}
	if s := statusOf(script.ID); s != "failed" {
		t.Errorf("просвистевшая script-задача = %q, want failed", s)
	}
	if s := statusOf(lock.ID); s != "acked" {
		t.Errorf("лок-задача = %q, want acked — она НЕ должна попадать под sweep", s)
	}
	if s := statusOf(pending.ID); s != "pending" {
		t.Errorf("pending-задача = %q, want pending", s)
	}
}
