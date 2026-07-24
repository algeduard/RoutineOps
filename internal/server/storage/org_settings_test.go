package storage_test

import (
	"context"
	"testing"
)

func TestOrgSettingsMFAPolicy(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	// Дефолт (строка засеяна миграцией 054) — политика выключена ('').
	role, err := db.GetMFARequiredRole(ctx)
	if err != nil {
		t.Fatalf("GetMFARequiredRole: %v", err)
	}
	if role != "" {
		t.Fatalf("default policy = %q, want '' (off)", role)
	}

	// Установка и чтение обратно — по всем допустимым значениям.
	for _, want := range []string{"it_admin", "all", ""} {
		if err := db.SetMFARequiredRole(ctx, want); err != nil {
			t.Fatalf("SetMFARequiredRole(%q): %v", want, err)
		}
		got, err := db.GetMFARequiredRole(ctx)
		if err != nil {
			t.Fatalf("GetMFARequiredRole after set %q: %v", want, err)
		}
		if got != want {
			t.Fatalf("policy = %q, want %q", got, want)
		}
	}
}
