package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Новое устройство по умолчанию на stable (DEFAULT в миграции 038); SetDeviceUpdateChannel
// переводит его на beta; резолв по CN (то, что шлёт агент) возвращает актуальный канал.
func TestDeviceUpdateChannel_SetAndResolveByCN(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	cn := "dev-chan-" + uniq(t)
	fp := "fp-" + cn

	// Устройство заезжает через heartbeat: cert_cn = 3-й аргумент.
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, cn, cn, "192.0.2.20")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("GetDeviceIDByFingerprint: id=%q err=%v", id, err)
	}

	// Дефолт — stable.
	if got, _, err := db.GetDevice(ctx, id); err != nil || got == nil {
		t.Fatalf("GetDevice: %v", err)
	} else if got.UpdateChannel != storage.ChannelStable {
		t.Fatalf("дефолтный канал = %q, ожидали stable", got.UpdateChannel)
	}

	// Резолв по CN до перевода — stable, found=true.
	ch, found, err := db.GetDeviceUpdateChannelByCN(ctx, cn)
	if err != nil || !found {
		t.Fatalf("GetDeviceUpdateChannelByCN: ch=%q found=%v err=%v", ch, found, err)
	}
	if ch != storage.ChannelStable {
		t.Fatalf("канал по CN = %q, ожидали stable", ch)
	}

	// Перевод на beta.
	updated, err := db.SetDeviceUpdateChannel(ctx, id, storage.ChannelBeta)
	if err != nil || !updated {
		t.Fatalf("SetDeviceUpdateChannel: updated=%v err=%v", updated, err)
	}

	ch, found, err = db.GetDeviceUpdateChannelByCN(ctx, cn)
	if err != nil || !found {
		t.Fatalf("GetDeviceUpdateChannelByCN after set: found=%v err=%v", found, err)
	}
	if ch != storage.ChannelBeta {
		t.Fatalf("канал по CN после перевода = %q, ожидали beta", ch)
	}

	if got, _, err := db.GetDevice(ctx, id); err != nil || got == nil {
		t.Fatalf("GetDevice after set: %v", err)
	} else if got.UpdateChannel != storage.ChannelBeta {
		t.Fatalf("канал в карточке = %q, ожидали beta", got.UpdateChannel)
	}
}

// Резолв по неизвестному/пустому CN не находит устройства — вызывающий трактует это как
// stable (fail-safe). Пустой CN даже не ходит в БД.
func TestGetDeviceUpdateChannelByCN_Unknown(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	if _, found, err := db.GetDeviceUpdateChannelByCN(ctx, "no-such-cn-"+uniq(t)); err != nil || found {
		t.Fatalf("неизвестный CN: found=%v err=%v, ожидали found=false, err=nil", found, err)
	}
	if _, found, err := db.GetDeviceUpdateChannelByCN(ctx, ""); err != nil || found {
		t.Fatalf("пустой CN: found=%v err=%v, ожидали found=false, err=nil", found, err)
	}
}

// SetDeviceUpdateChannel на несуществующем id возвращает found=false (→ 404 у ручки).
func TestSetDeviceUpdateChannel_NotFound(t *testing.T) {
	db := newDB(t)
	found, err := db.SetDeviceUpdateChannel(context.Background(),
		"00000000-0000-0000-0000-000000000000", storage.ChannelBeta)
	if err != nil {
		t.Fatalf("SetDeviceUpdateChannel: %v", err)
	}
	if found {
		t.Fatal("ожидали found=false для несуществующего устройства")
	}
}
