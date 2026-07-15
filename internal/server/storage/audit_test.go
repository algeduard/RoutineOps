package storage_test

import (
	"context"
	"fmt"
	"testing"
)

func TestWriteAuditLog_NoError(t *testing.T) {
	db := newDB(t)
	u := mustCreateUser(t, db, fmt.Sprintf("audit-%s@test.com", uniq(t)))

	err := db.WriteAuditLog(context.Background(),
		u.ID, u.Email, "LOGIN", "user", u.ID,
		map[string]string{"ip": "127.0.0.1"})
	if err != nil {
		t.Fatalf("WriteAuditLog: %v", err)
	}
}

func TestWriteAuditLog_NoUserID_NoError(t *testing.T) {
	db := newDB(t)
	err := db.WriteAuditLog(context.Background(),
		"", "system", "STARTUP", "system", "", nil)
	if err != nil {
		t.Fatalf("WriteAuditLog (no user): %v", err)
	}
}

func TestListAuditLog_ContainsWritten(t *testing.T) {
	db := newDB(t)
	u := mustCreateUser(t, db, fmt.Sprintf("audit2-%s@test.com", uniq(t)))
	action := fmt.Sprintf("TEST_ACTION_%s", uniq(t))

	_ = db.WriteAuditLog(context.Background(), u.ID, u.Email, action, "device", "dev-1", nil)

	entries, err := db.ListAuditLog(context.Background(), action, 10)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one audit entry")
	}
	if entries[0].Action != action {
		t.Errorf("action = %q, want %q", entries[0].Action, action)
	}
	if entries[0].UserEmail != u.Email {
		t.Errorf("user_email = %q, want %q", entries[0].UserEmail, u.Email)
	}
}

func TestListAuditLog_FilterByAction_Isolates(t *testing.T) {
	db := newDB(t)
	u := mustCreateUser(t, db, fmt.Sprintf("audit3-%s@test.com", uniq(t)))
	actionA := fmt.Sprintf("ACTION_A_%s", uniq(t))
	actionB := fmt.Sprintf("ACTION_B_%s", uniq(t))

	_ = db.WriteAuditLog(context.Background(), u.ID, u.Email, actionA, "x", "1", nil)
	_ = db.WriteAuditLog(context.Background(), u.ID, u.Email, actionB, "x", "2", nil)

	entries, err := db.ListAuditLog(context.Background(), actionA, 10)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	for _, e := range entries {
		if e.Action != actionA {
			t.Errorf("got action %q, want %q", e.Action, actionA)
		}
	}
}
