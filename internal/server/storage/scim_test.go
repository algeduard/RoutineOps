package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func TestSCIMTokenHashRoundtrip(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	// Пусто на старте: SCIM выключен.
	h, err := db.GetSCIMTokenHash(ctx)
	if err != nil {
		t.Fatalf("GetSCIMTokenHash: %v", err)
	}
	if h != "" {
		t.Fatalf("ожидали пустой хеш на старте, got %q", h)
	}
	if en, _ := db.SCIMEnabled(ctx); en {
		t.Fatal("ожидали SCIMEnabled=false на старте")
	}

	if err := db.SetSCIMTokenHash(ctx, "hash-aaa"); err != nil {
		t.Fatalf("SetSCIMTokenHash: %v", err)
	}
	if h, _ = db.GetSCIMTokenHash(ctx); h != "hash-aaa" {
		t.Fatalf("после установки got %q, want hash-aaa", h)
	}
	if en, _ := db.SCIMEnabled(ctx); !en {
		t.Fatal("ожидали SCIMEnabled=true после установки")
	}

	// Ротация = перезапись singleton.
	if err := db.SetSCIMTokenHash(ctx, "hash-bbb"); err != nil {
		t.Fatalf("rotate SetSCIMTokenHash: %v", err)
	}
	if h, _ = db.GetSCIMTokenHash(ctx); h != "hash-bbb" {
		t.Fatalf("после ротации got %q, want hash-bbb", h)
	}
}

func TestCreateSCIMUserAndDuplicate(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	email := "scim-" + uniq(t) + "@test.com"

	u, err := db.CreateSCIMUser(ctx, email, "Ada", "Lovelace", "Ada Lovelace", "viewer", true)
	if err != nil {
		t.Fatalf("CreateSCIMUser: %v", err)
	}
	if u.UserName != email || u.GivenName != "Ada" || u.FamilyName != "Lovelace" || u.Formatted != "Ada Lovelace" {
		t.Fatalf("поля SCIM-юзера неверны: %+v", u)
	}
	if !u.Active || u.AuthSource != "scim" {
		t.Fatalf("ожидали active=true, auth_source=scim: %+v", u)
	}
	if active, _ := db.IsUserActive(ctx, u.ID); !active {
		t.Fatal("IsUserActive должен быть true у свежесозданного")
	}
	// Провижининг с unusable-паролем: локальный вход невозможен. Роль least-privilege.
	full, _ := db.GetUserByID(ctx, u.ID)
	if full == nil || full.Role != "viewer" {
		t.Fatalf("ожидали роль viewer, got %+v", full)
	}

	// Дубль userName (email) → ErrSCIMUserExists.
	if _, err := db.CreateSCIMUser(ctx, email, "X", "Y", "X Y", "viewer", true); !errors.Is(err, storage.ErrSCIMUserExists) {
		t.Fatalf("дубль: got %v, want ErrSCIMUserExists", err)
	}
}

func TestSCIMUserGetAndActiveToggle(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	email := "scim-" + uniq(t) + "@test.com"

	u, err := db.CreateSCIMUser(ctx, email, "Grace", "Hopper", "Grace Hopper", "viewer", true)
	if err != nil {
		t.Fatalf("CreateSCIMUser: %v", err)
	}

	got, err := db.GetSCIMUserByID(ctx, u.ID)
	if err != nil || got == nil || got.ID != u.ID {
		t.Fatalf("GetSCIMUserByID: %v %+v", err, got)
	}
	byEmail, err := db.GetSCIMUserByEmail(ctx, email)
	if err != nil || byEmail == nil || byEmail.ID != u.ID {
		t.Fatalf("GetSCIMUserByEmail: %v %+v", err, byEmail)
	}

	// Деактивация (DELETE-путь).
	deact, err := db.SetSCIMUserActive(ctx, u.ID, false)
	if err != nil || deact == nil || deact.Active {
		t.Fatalf("SetSCIMUserActive(false): %v %+v", err, deact)
	}
	if active, _ := db.IsUserActive(ctx, u.ID); active {
		t.Fatal("после деактивации IsUserActive должен быть false")
	}

	// Несуществующий / битый id → nil (не ошибка).
	if got, err := db.GetSCIMUserByID(ctx, "not-a-uuid"); err != nil || got != nil {
		t.Fatalf("битый id: got %+v err %v, want nil,nil", got, err)
	}
	if got, err := db.SetSCIMUserActive(ctx, "not-a-uuid", false); err != nil || got != nil {
		t.Fatalf("SetSCIMUserActive битый id: got %+v err %v, want nil,nil", got, err)
	}
}

// UpdateSCIMUser НЕ трогает роль/пароль существующего локального админа — только active+имя.
func TestUpdateSCIMUserPreservesRoleAndPassword(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	email := "local-admin-" + uniq(t) + "@test.com"

	admin, err := db.CreateUser(ctx, "Local Admin", email, "local-pw-hash", "it_admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// SCIM деактивирует и переименовывает — роль/пароль остаются нетронутыми.
	upd, err := db.UpdateSCIMUser(ctx, admin.ID, "New", "Name", "New Name", false)
	if err != nil || upd == nil {
		t.Fatalf("UpdateSCIMUser: %v %+v", err, upd)
	}
	if upd.Active {
		t.Fatal("ожидали active=false после апдейта")
	}

	full, _ := db.GetUserByID(ctx, admin.ID)
	if full == nil || full.Role != "it_admin" {
		t.Fatalf("роль локального админа изменена: %+v", full)
	}
	if full.PasswordHash != "local-pw-hash" {
		t.Fatalf("пароль локального админа изменён: %q", full.PasswordHash)
	}
}

func TestListSCIMUsersFilterAndPagination(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)
	target := "scim-list-target-" + suffix + "@test.com"

	if _, err := db.CreateSCIMUser(ctx, target, "T", "One", "T One", "viewer", true); err != nil {
		t.Fatalf("create target: %v", err)
	}
	for i := 0; i < 3; i++ {
		e := "scim-list-" + uniq(t) + "@test.com"
		if _, err := db.CreateSCIMUser(ctx, e, "N", "N", "N N", "viewer", true); err != nil {
			t.Fatalf("create filler: %v", err)
		}
	}

	// Фильтр по userName eq (case-insensitive) → ровно один.
	got, total, err := db.ListSCIMUsers(ctx, target, 1, 100)
	if err != nil {
		t.Fatalf("ListSCIMUsers filter: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].UserName != target {
		t.Fatalf("фильтр: total=%d len=%d %+v", total, len(got), got)
	}

	// Пагинация: count=2 отдаёт максимум 2, но total считает всех (>=4).
	page, total, err := db.ListSCIMUsers(ctx, "", 1, 2)
	if err != nil {
		t.Fatalf("ListSCIMUsers page: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("ожидали страницу 2, got %d", len(page))
	}
	if total < 4 {
		t.Fatalf("ожидали total>=4, got %d", total)
	}
}
