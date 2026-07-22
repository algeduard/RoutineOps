package storage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func mustCreateHelpRequest(t *testing.T, db *storage.DB, deviceID, message string, screenshot []byte) string {
	t.Helper()
	id, err := db.CreateHelpRequest(context.Background(), deviceID, `OFFICE\ivanov`, message, screenshot, time.Now())
	if err != nil {
		t.Fatalf("CreateHelpRequest: %v", err)
	}
	return id
}

func TestCreateHelpRequest_ListedAsNew(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-hr-%s", uniq(t)), "windows")

	id := mustCreateHelpRequest(t, db, d.ID, "принтер не печатает", nil)
	if id == "" {
		t.Fatal("expected non-empty request ID")
	}

	rows, err := db.ListHelpRequests(context.Background(), d.ID, "")
	if err != nil {
		t.Fatalf("ListHelpRequests: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Status != "new" || r.Message != "принтер не печатает" || r.Reporter != `OFFICE\ivanov` {
		t.Errorf("строка обращения неверна: %+v", r)
	}
	if r.HasScreenshot {
		t.Error("HasScreenshot = true для обращения без скриншота")
	}
	if r.DeviceHostname == "" {
		t.Error("DeviceHostname пуст — JOIN c devices не сработал")
	}
}

// Скриншот не отдаётся в списке (только флаг), но целиком достаётся отдельным запросом.
func TestHelpRequestScreenshot(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-hrs-%s", uniq(t)), "windows")
	shot := []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}

	id := mustCreateHelpRequest(t, db, d.ID, "со скрином", shot)

	rows, _ := db.ListHelpRequests(context.Background(), d.ID, "")
	if len(rows) != 1 || !rows[0].HasScreenshot {
		t.Fatalf("ожидали 1 строку с HasScreenshot=true: %+v", rows)
	}

	got, err := db.GetHelpRequestScreenshot(context.Background(), id)
	if err != nil {
		t.Fatalf("GetHelpRequestScreenshot: %v", err)
	}
	if !bytes.Equal(got, shot) {
		t.Errorf("скриншот повреждён: %v", got)
	}
}

// Обращение без скриншота и несуществующий id → (nil, nil), не ошибка.
func TestHelpRequestScreenshotAbsent(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-hrn-%s", uniq(t)), "windows")
	id := mustCreateHelpRequest(t, db, d.ID, "без скрина", nil)

	if got, err := db.GetHelpRequestScreenshot(context.Background(), id); err != nil || got != nil {
		t.Errorf("без скрина: got=%v err=%v, want nil,nil", got, err)
	}
	if got, err := db.GetHelpRequestScreenshot(context.Background(), "не-uuid"); err != nil || got != nil {
		t.Errorf("кривой id: got=%v err=%v, want nil,nil", got, err)
	}
}

func TestSetHelpRequestStatus_CloseAndReopen(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-hrc-%s", uniq(t)), "windows")
	u := mustCreateUser(t, db, fmt.Sprintf("hrc-%s@test.com", uniq(t)))
	id := mustCreateHelpRequest(t, db, d.ID, "закрой меня", nil)

	if err := db.SetHelpRequestStatus(context.Background(), id, "closed", u.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	rows, _ := db.ListHelpRequests(context.Background(), d.ID, "closed")
	if len(rows) != 1 || rows[0].ClosedAt == nil || rows[0].ClosedByEmail == "" {
		t.Fatalf("после закрытия: %+v", rows)
	}

	// Повторное закрытие — 0 строк → ErrHelpRequestNotFound (идемпотентность для UI).
	if err := db.SetHelpRequestStatus(context.Background(), id, "closed", u.ID); !errors.Is(err, storage.ErrHelpRequestNotFound) {
		t.Fatalf("повторное закрытие: %v, want ErrHelpRequestNotFound", err)
	}

	if err := db.SetHelpRequestStatus(context.Background(), id, "new", u.ID); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rows, _ = db.ListHelpRequests(context.Background(), d.ID, "new")
	if len(rows) != 1 || rows[0].ClosedAt != nil || rows[0].ClosedBy != nil {
		t.Fatalf("после переоткрытия closed_* должны быть NULL: %+v", rows)
	}
}

func TestLastHelpRequestAt(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-hrl-%s", uniq(t)), "windows")

	if last, err := db.LastHelpRequestAt(context.Background(), d.ID); err != nil || !last.IsZero() {
		t.Fatalf("до обращений: last=%v err=%v, want zero,nil", last, err)
	}
	mustCreateHelpRequest(t, db, d.ID, "первое", nil)
	last, err := db.LastHelpRequestAt(context.Background(), d.ID)
	if err != nil || last.IsZero() {
		t.Fatalf("после обращения: last=%v err=%v", last, err)
	}
	if time.Since(last) > time.Minute {
		t.Errorf("received_at слишком старый: %v", last)
	}
}

// Фильтр статуса и скоуп по устройству не путают чужие обращения.
func TestListHelpRequests_Filters(t *testing.T) {
	db := newDB(t)
	d1 := mustCreateDevice(t, db, fmt.Sprintf("host-hrf1-%s", uniq(t)), "windows")
	d2 := mustCreateDevice(t, db, fmt.Sprintf("host-hrf2-%s", uniq(t)), "windows")
	u := mustCreateUser(t, db, fmt.Sprintf("hrf-%s@test.com", uniq(t)))

	idClosed := mustCreateHelpRequest(t, db, d1.ID, "закрытое", nil)
	mustCreateHelpRequest(t, db, d1.ID, "новое", nil)
	mustCreateHelpRequest(t, db, d2.ID, "чужое", nil)
	if err := db.SetHelpRequestStatus(context.Background(), idClosed, "closed", u.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := db.ListHelpRequests(context.Background(), d1.ID, "new")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Message != "новое" {
		t.Errorf("фильтр new по d1: %+v", rows)
	}
	// Кривой device_id из query-string → пустой список, не 500 (22P02).
	if rows, err := db.ListHelpRequests(context.Background(), "не-uuid", ""); err != nil || len(rows) != 0 {
		t.Errorf("кривой device_id: rows=%v err=%v", rows, err)
	}
}
