package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func tenantByID(list []storage.Tenant, id string) *storage.Tenant {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func TestCreateListAssignTenant(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	sfx := uniq(t) // "<ns>-<ctr>" — цифры и дефис, валидный slug

	tnt, err := db.CreateTenant(ctx, "Acme "+sfx, "acme-"+sfx)
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if tnt.ID == "" || tnt.IsDefault {
		t.Fatalf("новый тенант не должен быть default: %+v", tnt)
	}

	// Default присутствует и помечен is_default; новый тенант пуст.
	list, err := db.ListTenants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	def := tenantByID(list, storage.DefaultTenantID)
	if def == nil || !def.IsDefault {
		t.Fatalf("default-тенант должен быть в списке и помечен is_default")
	}
	mine := tenantByID(list, tnt.ID)
	if mine == nil || mine.DeviceCount != 0 || mine.UserCount != 0 {
		t.Fatalf("новый тенант должен быть пуст: %+v", mine)
	}

	// Назначаем устройство и пользователя → счётчики становятся 1/1.
	dev := mustCreateDevice(t, db, "tn-host-"+sfx, "windows")
	usr := mustCreateUser(t, db, "tn-user-"+sfx+"@test.com")
	if found, err := db.AssignDeviceTenant(ctx, dev.ID, tnt.ID); err != nil || !found {
		t.Fatalf("AssignDeviceTenant found=%v err=%v", found, err)
	}
	if found, err := db.AssignUserTenant(ctx, usr.ID, tnt.ID); err != nil || !found {
		t.Fatalf("AssignUserTenant found=%v err=%v", found, err)
	}

	list, _ = db.ListTenants(ctx)
	mine = tenantByID(list, tnt.ID)
	if mine == nil || mine.DeviceCount != 1 || mine.UserCount != 1 {
		t.Fatalf("после назначения ожидали 1 устройство и 1 юзера: %+v", mine)
	}

	// Непустой тенант удалить нельзя.
	if err := db.DeleteTenant(ctx, tnt.ID); !errors.Is(err, storage.ErrTenantNotEmpty) {
		t.Fatalf("удаление непустого = %v, want ErrTenantNotEmpty", err)
	}

	// Переназначаем обратно в default → тенант пуст → удаляется.
	if _, err := db.AssignDeviceTenant(ctx, dev.ID, storage.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AssignUserTenant(ctx, usr.ID, storage.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTenant(ctx, tnt.ID); err != nil {
		t.Fatalf("удаление пустого тенанта: %v", err)
	}
	if got, _ := db.GetTenant(ctx, tnt.ID); got != nil {
		t.Fatalf("тенант должен быть удалён, got %+v", got)
	}
}

func TestDefaultTenantNotDeletable(t *testing.T) {
	db := newDB(t)
	if err := db.DeleteTenant(context.Background(), storage.DefaultTenantID); !errors.Is(err, storage.ErrTenantIsDefault) {
		t.Fatalf("удаление default = %v, want ErrTenantIsDefault", err)
	}
}

func TestTenantDuplicateAndRename(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	sfx := uniq(t)

	if _, err := db.CreateTenant(ctx, "Dup "+sfx, "dup-"+sfx); err != nil {
		t.Fatal(err)
	}
	// Тот же slug → конфликт.
	if _, err := db.CreateTenant(ctx, "Other "+sfx, "dup-"+sfx); !errors.Is(err, storage.ErrTenantExists) {
		t.Fatalf("дубль slug = %v, want ErrTenantExists", err)
	}

	tnt, _ := db.CreateTenant(ctx, "Rename Me "+sfx, "rename-"+sfx)
	if err := db.RenameTenant(ctx, tnt.ID, "Renamed "+sfx); err != nil {
		t.Fatalf("RenameTenant: %v", err)
	}
	got, _ := db.GetTenant(ctx, tnt.ID)
	if got == nil || got.Name != "Renamed "+sfx {
		t.Fatalf("после переименования: %+v", got)
	}

	// Переименование несуществующего → ErrTenantNotFound.
	if err := db.RenameTenant(ctx, "00000000-0000-0000-0000-0000000000ff", "x"); !errors.Is(err, storage.ErrTenantNotFound) {
		t.Fatalf("переименование несуществующего = %v, want ErrTenantNotFound", err)
	}
}
