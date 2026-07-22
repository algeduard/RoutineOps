package storage_test

import (
	"context"
	"fmt"
	"testing"
)

// unattended-политика удалённого рабочего стола (миграция 039): opt-in на устройство,
// DEFAULT false. Проверяем round-trip set/get, дефолт, found-семантику и что карточка
// устройства (GetDevice) отдаёт актуальное значение — на нём держится инвариант
// «согласие пропускается ТОЛЬКО когда включено».

func TestRDUnattended_DefaultOffAndRoundTrip(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	d := mustCreateDevice(t, db, fmt.Sprintf("host-rd-%s", uniq(t)), "windows")

	// Дефолт: выключено (fail-safe — без opt-in согласие требуется).
	enabled, found, err := db.GetRDUnattended(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetRDUnattended: %v", err)
	}
	if !found {
		t.Fatalf("found = false для существующего устройства")
	}
	if enabled {
		t.Fatalf("rd_unattended по умолчанию = true, ожидалось false (opt-in, DEFAULT OFF)")
	}

	// Карточка устройства тоже показывает выключено по умолчанию.
	dev, _, err := db.GetDevice(ctx, d.ID)
	if err != nil || dev == nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if dev.RDUnattended {
		t.Fatalf("GetDevice.RDUnattended = true по умолчанию, ожидалось false")
	}

	// Включаем.
	foundSet, err := db.SetRDUnattended(ctx, d.ID, true)
	if err != nil {
		t.Fatalf("SetRDUnattended(true): %v", err)
	}
	if !foundSet {
		t.Fatalf("SetRDUnattended found = false для существующего устройства")
	}
	enabled, _, err = db.GetRDUnattended(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetRDUnattended после включения: %v", err)
	}
	if !enabled {
		t.Fatalf("rd_unattended = false после включения")
	}
	dev, _, _ = db.GetDevice(ctx, d.ID)
	if !dev.RDUnattended {
		t.Fatalf("GetDevice.RDUnattended = false после включения")
	}

	// Выключаем обратно.
	if _, err := db.SetRDUnattended(ctx, d.ID, false); err != nil {
		t.Fatalf("SetRDUnattended(false): %v", err)
	}
	enabled, _, _ = db.GetRDUnattended(ctx, d.ID)
	if enabled {
		t.Fatalf("rd_unattended = true после выключения")
	}
}

func TestRDUnattended_UnknownDeviceFailSafe(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	const missing = "00000000-0000-0000-0000-000000000000"

	// Неизвестное устройство: (false, false) — fail-safe, не ошибка. Сервер трактует
	// это как «согласие НЕ пропускать».
	enabled, found, err := db.GetRDUnattended(ctx, missing)
	if err != nil {
		t.Fatalf("GetRDUnattended(missing): %v", err)
	}
	if found {
		t.Fatalf("found = true для несуществующего устройства")
	}
	if enabled {
		t.Fatalf("enabled = true для несуществующего устройства (должно быть false, fail-safe)")
	}

	// Set на несуществующее устройство: found=false, 0 строк.
	foundSet, err := db.SetRDUnattended(ctx, missing, true)
	if err != nil {
		t.Fatalf("SetRDUnattended(missing): %v", err)
	}
	if foundSet {
		t.Fatalf("SetRDUnattended found = true для несуществующего устройства")
	}
}
