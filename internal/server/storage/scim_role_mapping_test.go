package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func TestSCIMRoleMappingRoundtrip(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	// Дефолт на старте: admin-групп нет, роль viewer (it_admin через SCIM не выдаётся).
	m, err := db.GetSCIMRoleMapping(ctx)
	if err != nil {
		t.Fatalf("GetSCIMRoleMapping: %v", err)
	}
	if m.AdminGroupValues != "" || m.DefaultRole != "viewer" {
		t.Fatalf("дефолт: %+v", m)
	}

	if err := db.SetSCIMRoleMapping(ctx, "Admins,Ops", "viewer"); err != nil {
		t.Fatalf("SetSCIMRoleMapping: %v", err)
	}
	m, _ = db.GetSCIMRoleMapping(ctx)
	if m.AdminGroupValues != "Admins,Ops" || m.DefaultRole != "viewer" {
		t.Fatalf("после установки: %+v", m)
	}
	// Апсерт (singleton) — перезапись.
	if err := db.SetSCIMRoleMapping(ctx, "Root", "viewer"); err != nil {
		t.Fatalf("rewrite SetSCIMRoleMapping: %v", err)
	}
	m, _ = db.GetSCIMRoleMapping(ctx)
	if m.AdminGroupValues != "Root" {
		t.Fatalf("после перезаписи: %+v", m)
	}
}

func TestSCIMRoleMappingHelpers(t *testing.T) {
	m := storage.SCIMRoleMapping{AdminGroupValues: " a , b ,, a ", DefaultRole: "viewer"}
	set := m.AdminGroupSet()
	if !set["a"] || !set["b"] || len(set) != 2 {
		t.Fatalf("AdminGroupSet: %+v", set)
	}
	// EffectiveDefaultRole fail-closed: it_admin/пусто → viewer.
	for _, in := range []string{"it_admin", ""} {
		if got := (storage.SCIMRoleMapping{DefaultRole: in}).EffectiveDefaultRole(); got != "viewer" {
			t.Fatalf("EffectiveDefaultRole(%q) = %q, want viewer", in, got)
		}
	}
	if got := (storage.SCIMRoleMapping{DefaultRole: "viewer"}).EffectiveDefaultRole(); got != "viewer" {
		t.Fatalf("EffectiveDefaultRole(viewer) = %q", got)
	}
}

func TestSetSCIMUserRole(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	// SCIM-аккаунт: роль меняется, changed отражает реальное изменение.
	email := "scim-role-" + uniq(t) + "@test.com"
	u, err := db.CreateSCIMUser(ctx, email, "A", "B", "A B", "viewer", true)
	if err != nil {
		t.Fatalf("CreateSCIMUser: %v", err)
	}
	changed, err := db.SetSCIMUserRole(ctx, u.ID, "it_admin")
	if err != nil || !changed {
		t.Fatalf("SetSCIMUserRole(it_admin): changed=%v err=%v", changed, err)
	}
	if full, _ := db.GetUserByID(ctx, u.ID); full == nil || full.Role != "it_admin" {
		t.Fatalf("роль SCIM-юзера не изменилась: %+v", full)
	}
	// Та же роль повторно → changed=false (без лишнего аудита).
	if changed, _ := db.SetSCIMUserRole(ctx, u.ID, "it_admin"); changed {
		t.Fatal("повтор той же роли: changed должен быть false")
	}

	// НЕ-SCIM аккаунт (auth_source='local'): роль НЕ трогается даже по прямому вызову.
	local, err := db.CreateUser(ctx, "Local", "local-role-"+uniq(t)+"@test.com", "hash", "viewer")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	changed, err = db.SetSCIMUserRole(ctx, local.ID, "it_admin")
	if err != nil {
		t.Fatalf("SetSCIMUserRole(local): %v", err)
	}
	if changed {
		t.Fatal("роль локального аккаунта не должна меняться через SCIM-канал")
	}
	if full, _ := db.GetUserByID(ctx, local.ID); full == nil || full.Role != "viewer" {
		t.Fatalf("роль локального аккаунта изменена: %+v", full)
	}

	// Битый/несуществующий id → changed=false, без ошибки.
	if changed, err := db.SetSCIMUserRole(ctx, "not-a-uuid", "it_admin"); err != nil || changed {
		t.Fatalf("битый id: changed=%v err=%v", changed, err)
	}
}
