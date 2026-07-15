package storage_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func TestGetDevice_Found(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-get-%s", uniq(t)), "macos")

	got, sw, err := db.GetDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got == nil {
		t.Fatal("got nil device")
	}
	if got.ID != d.ID {
		t.Errorf("id = %q, want %q", got.ID, d.ID)
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want pending", got.Status)
	}
	_ = sw // nil is a valid empty result when device has no software
}

func TestGetDevice_NotFound_ReturnsNil(t *testing.T) {
	db := newDB(t)
	got, _, err := db.GetDevice(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListDevices_ContainsCreated(t *testing.T) {
	db := newDB(t)
	hostname := fmt.Sprintf("host-list-%s", uniq(t))
	d := mustCreateDevice(t, db, hostname, "windows")

	devices, err := db.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	found := false
	for _, dev := range devices {
		if dev.ID == d.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created device %s not in list", d.ID)
	}
}

func TestUpdateDeviceStatus(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-status-%s", uniq(t)), "macos")

	if err := db.UpdateDeviceStatus(context.Background(), d.ID, "blocked"); err != nil {
		t.Fatalf("UpdateDeviceStatus: %v", err)
	}
	got, _, err := db.GetDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got.Status != "blocked" {
		t.Errorf("status = %q, want blocked", got.Status)
	}
}

func TestDeleteDevice_RemovesAndReports(t *testing.T) {
	db := newDB(t)
	d := mustCreateDevice(t, db, fmt.Sprintf("host-del-%s", uniq(t)), "windows")

	found, err := db.DeleteDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true для существующего устройства")
	}
	got, _, err := db.GetDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDevice после удаления: %v", err)
	}
	if got != nil {
		t.Errorf("устройство всё ещё существует после DeleteDevice: %+v", got)
	}
}

func TestDeleteDevice_NotFound(t *testing.T) {
	db := newDB(t)
	found, err := db.DeleteDevice(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if found {
		t.Error("found = true для несуществующего устройства, want false (→ 404)")
	}
}

func TestUpsertDeviceHeartbeat_CreatesThenUpdates(t *testing.T) {
	db := newDB(t)
	fp := fmt.Sprintf("fp-%s", uniq(t))

	hb := storageHeartbeatData(fp, "agent-1", "agent-1", "1.2.3.4")
	if err := db.UpsertDeviceHeartbeat(context.Background(), hb); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat (create): %v", err)
	}

	// update IP
	hb.IPAddress = "5.6.7.8"
	if err := db.UpsertDeviceHeartbeat(context.Background(), hb); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat (update): %v", err)
	}

	// verify by fingerprint
	id, err := db.GetDeviceIDByFingerprint(context.Background(), fp)
	if err != nil {
		t.Fatalf("GetDeviceIDByFingerprint: %v", err)
	}
	if id == "" {
		t.Error("expected device ID after upsert")
	}
}

// enrollDevice прогоняет устройство через pending→enrolled с сохранением fingerprint,
// чтобы heartbeat шёл по ветке ON CONFLICT (как на проде), а не создавал новую строку.
func enrollDevice(t *testing.T, db *storage.DB, hostname, fp string) string {
	t.Helper()
	d := mustCreateDevice(t, db, hostname, "macos")
	tok := fmt.Sprintf("tok-%s", uniq(t))
	if err := db.CreateEnrollmentToken(context.Background(), d.ID, tok, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateEnrollmentToken: %v", err)
	}
	et, err := db.GetEnrollmentToken(context.Background(), tok)
	if err != nil || et == nil {
		t.Fatalf("GetEnrollmentToken: %v", err)
	}
	if err := db.EnrollDevice(context.Background(), et.ID, d.ID, "serial-"+fp, fp); err != nil {
		t.Fatalf("EnrollDevice: %v", err)
	}
	return d.ID
}

// Первый heartbeat должен перевести устройство enrolled→active (иначе UI блокирует действия).
func TestUpsertDeviceHeartbeat_PromotesEnrolledToActive(t *testing.T) {
	db := newDB(t)
	fp := fmt.Sprintf("fp-promote-%s", uniq(t))
	enrollDevice(t, db, fmt.Sprintf("host-promote-%s", uniq(t)), fp)

	if st, err := db.GetDeviceStatusByFingerprint(context.Background(), fp); err != nil {
		t.Fatalf("status before heartbeat: %v", err)
	} else if st != "enrolled" {
		t.Fatalf("status before heartbeat = %q, want enrolled", st)
	}

	if err := db.UpsertDeviceHeartbeat(context.Background(), storageHeartbeatData(fp, "agent-p", "agent-p", "1.2.3.4")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}

	if st, err := db.GetDeviceStatusByFingerprint(context.Background(), fp); err != nil {
		t.Fatalf("status after heartbeat: %v", err)
	} else if st != "active" {
		t.Errorf("status after heartbeat = %q, want active", st)
	}
}

// Реенролл сбрасывает устройство в pending (ResetDeviceForReenroll). Если агент
// переподключается тем же сертификатом (свежий токен не использован), heartbeat идёт по
// ON CONFLICT (fingerprint) — он обязан промоутить pending→active, иначе живой девайс
// навсегда прячется из ListEnrolledDevices (WHERE status != 'pending').
func TestUpsertDeviceHeartbeat_PromotesPendingToActive(t *testing.T) {
	db := newDB(t)
	fp := fmt.Sprintf("fp-pending-%s", uniq(t))
	id := enrollDevice(t, db, fmt.Sprintf("host-pending-%s", uniq(t)), fp)

	if err := db.UpdateDeviceStatus(context.Background(), id, "pending"); err != nil {
		t.Fatalf("UpdateDeviceStatus: %v", err)
	}
	if err := db.UpsertDeviceHeartbeat(context.Background(), storageHeartbeatData(fp, "agent-pd", "agent-pd", "1.2.3.4")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}

	if st, err := db.GetDeviceStatusByFingerprint(context.Background(), fp); err != nil {
		t.Fatalf("status after heartbeat: %v", err)
	} else if st != "active" {
		t.Errorf("status after heartbeat = %q, want active", st)
	}
}

// heartbeat не должен снимать blocked: иначе заблокированное устройство молча
// разблокируется собственным heartbeat'ом ещё до проверки в gateway.
func TestUpsertDeviceHeartbeat_KeepsBlocked(t *testing.T) {
	db := newDB(t)
	fp := fmt.Sprintf("fp-blocked-%s", uniq(t))
	id := enrollDevice(t, db, fmt.Sprintf("host-blocked-%s", uniq(t)), fp)

	if err := db.UpdateDeviceStatus(context.Background(), id, "blocked"); err != nil {
		t.Fatalf("UpdateDeviceStatus: %v", err)
	}
	if err := db.UpsertDeviceHeartbeat(context.Background(), storageHeartbeatData(fp, "agent-b", "agent-b", "1.2.3.4")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}

	if st, err := db.GetDeviceStatusByFingerprint(context.Background(), fp); err != nil {
		t.Fatalf("status after heartbeat: %v", err)
	} else if st != "blocked" {
		t.Errorf("status after heartbeat = %q, want blocked", st)
	}
}

func TestGetDeviceStatusByFingerprint(t *testing.T) {
	db := newDB(t)
	fp := fmt.Sprintf("fp-status-%s", uniq(t))

	if err := db.UpsertDeviceHeartbeat(context.Background(), storageHeartbeatData(fp, "agentx", "agentx", "1.1.1.1")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}

	status, err := db.GetDeviceStatusByFingerprint(context.Background(), fp)
	if err != nil {
		t.Fatalf("GetDeviceStatusByFingerprint: %v", err)
	}
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
}

func TestUpsertInventory_UpdatesAndReplacesSoftware(t *testing.T) {
	db := newDB(t)
	fp := fmt.Sprintf("fp-inv-%s", uniq(t))

	// create device via heartbeat first
	if err := db.UpsertDeviceHeartbeat(context.Background(), storageHeartbeatData(fp, "inv-host", "inv-host", "192.0.2.1")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	deviceID, _ := db.GetDeviceIDByFingerprint(context.Background(), fp)

	inv := storageInventoryData(fp, "my-host", "macos", "14.0", []string{"Chrome", "Slack"})
	if err := db.UpsertInventory(context.Background(), inv); err != nil {
		t.Fatalf("UpsertInventory: %v", err)
	}

	got, sw, err := db.GetDevice(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got.Hostname != "my-host" {
		t.Errorf("hostname = %q, want my-host", got.Hostname)
	}
	if len(sw) != 2 {
		t.Errorf("software count = %d, want 2", len(sw))
	}

	// replace software with just one item
	inv2 := storageInventoryData(fp, "my-host", "macos", "14.1", []string{"Firefox"})
	if err := db.UpsertInventory(context.Background(), inv2); err != nil {
		t.Fatalf("UpsertInventory (replace): %v", err)
	}
	_, sw2, err := db.GetDevice(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("GetDevice after replace: %v", err)
	}
	if len(sw2) != 1 || sw2[0].Name != "Firefox" {
		t.Errorf("software after replace = %v, want [Firefox]", sw2)
	}
}

// agent_version персистится из инвентаря, а пустая версия от старого агента НЕ
// затирает уже известную (COALESCE(NULLIF(...))).
func TestUpsertInventory_PersistsAgentVersion(t *testing.T) {
	db := newDB(t)
	fp := fmt.Sprintf("fp-ver-%s", uniq(t))
	if err := db.UpsertDeviceHeartbeat(context.Background(), storageHeartbeatData(fp, "ver-host", "ver-host", "192.0.2.2")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	deviceID, _ := db.GetDeviceIDByFingerprint(context.Background(), fp)

	if err := db.UpsertInventory(context.Background(), storageInventoryDataV(fp, "ver-host", "macos", "14.0", "2.3.0", nil)); err != nil {
		t.Fatalf("UpsertInventory: %v", err)
	}
	got, _, err := db.GetDevice(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got.AgentVersion != "2.3.0" {
		t.Errorf("agent_version = %q, want 2.3.0", got.AgentVersion)
	}

	// старый агент (пустая версия) — известную версию не трогаем
	if err := db.UpsertInventory(context.Background(), storageInventoryDataV(fp, "ver-host", "macos", "14.1", "", nil)); err != nil {
		t.Fatalf("UpsertInventory (empty version): %v", err)
	}
	got2, _, err := db.GetDevice(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got2.AgentVersion != "2.3.0" {
		t.Errorf("agent_version after empty report = %q, want kept 2.3.0", got2.AgentVersion)
	}
}

// Поиск по устройствам должен ловить ЛЮБОЙ собираемый агентом атрибут по подстроке:
// хвост серийника, кусок IP, MAC с разделителями и без. Это ровно то, как человек ищет.
func TestListEnrolledDevices_Search(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)

	fp := "fp-search-" + suffix
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(fp, "cn-"+suffix, "cn-"+suffix, "10.44.7.219")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat: %v", err)
	}
	if err := db.UpsertInventory(ctx, storage.InventoryData{
		CertFingerprint: fp,
		Hostname:        "buh-ws-" + suffix,
		OS:              "Windows",
		OSVersion:       "11",
		CPU:             "Intel Core i7-9700",
		IPAddress:       "10.44.7.219",
		MACAddress:      "A4:BB:6D:1F:0E:22",
		SerialNumber:    "QX7K3ZM8N4",
	}); err != nil {
		t.Fatalf("UpsertInventory: %v", err)
	}

	// Контрольное устройство, которое не должно попадать в выдачу.
	otherFP := "fp-other-" + suffix
	if err := db.UpsertDeviceHeartbeat(ctx, storageHeartbeatData(otherFP, "cn2-"+suffix, "cn2-"+suffix, "192.168.1.5")); err != nil {
		t.Fatalf("UpsertDeviceHeartbeat other: %v", err)
	}

	found := func(query string) bool {
		t.Helper()
		devices, err := db.ListEnrolledDevices(ctx, query, "")
		if err != nil {
			t.Fatalf("ListEnrolledDevices(%q): %v", query, err)
		}
		for _, d := range devices {
			if d.SerialNumber == "QX7K3ZM8N4" {
				return true
			}
		}
		return false
	}

	hits := []struct{ name, query string }{
		{"хвост серийника", "zm8n4"},
		{"серийник целиком другим регистром", "qx7k3zm8n4"},
		{"кусок IP", "44.7.2"},
		{"MAC как в базе", "a4:bb:6d"},
		{"MAC без разделителей", "a4bb6d1f0e22"},
		{"MAC через дефисы", "a4-bb-6d-1f-0e-22"},
		{"часть hostname", "buh-ws"},
		{"модель CPU", "i7-9700"},
		{"версия ОС", "Windows"},
	}
	for _, tc := range hits {
		if !found(tc.query) {
			t.Errorf("%s: запрос %q не нашёл устройство", tc.name, tc.query)
		}
	}

	misses := []struct{ name, query string }{
		{"чужой серийник", "ZZZZZZ"},
		{"LIKE-джокер не должен матчить всё", "%"},
		{"подчёркивание не должно матчить любой символ", "_"},
	}
	for _, tc := range misses {
		if found(tc.query) {
			t.Errorf("%s: запрос %q не должен был найти устройство", tc.name, tc.query)
		}
	}

	// Пустой запрос = весь парк.
	all, err := db.ListEnrolledDevices(ctx, "", "")
	if err != nil {
		t.Fatalf("ListEnrolledDevices(\"\"): %v", err)
	}
	if len(all) < 2 {
		t.Errorf("пустой запрос вернул %d устройств, ожидали минимум 2", len(all))
	}
}

// Фильтр по группе — отдельный параметр, а не часть поиска: имя группы не хранится в
// колонках устройства, подстрокой его не поймать.
func TestListEnrolledDevices_GroupFilter(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)

	group, err := db.CreateDeviceGroup(ctx, "grp-filter-"+suffix, "#aabbcc")
	if err != nil {
		t.Fatalf("CreateDeviceGroup: %v", err)
	}
	empty, err := db.CreateDeviceGroup(ctx, "grp-filter-empty-"+suffix, "")
	if err != nil {
		t.Fatalf("CreateDeviceGroup empty: %v", err)
	}

	member := activeDevice(t, db, "fmember-"+suffix, "Windows 11")
	activeDevice(t, db, "foutsider-"+suffix, "Windows 11")
	if err := db.AddDeviceToGroup(ctx, member, group.ID); err != nil {
		t.Fatalf("AddDeviceToGroup: %v", err)
	}

	// Пустой groupID — весь парк, включая устройство вне группы.
	all, err := db.ListEnrolledDevices(ctx, "", "")
	if err != nil {
		t.Fatalf("ListEnrolledDevices(all): %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("парк = %d устройств, ожидали минимум 2", len(all))
	}

	// Реальная группа — только её члены.
	inGroup, err := db.ListEnrolledDevices(ctx, "", group.ID)
	if err != nil {
		t.Fatalf("ListEnrolledDevices(group): %v", err)
	}
	if len(inGroup) != 1 || inGroup[0].ID != member {
		t.Fatalf("фильтр по группе вернул %d устройств (%+v), ожидали одно: %s", len(inGroup), inGroup, member)
	}

	// Группа без членов — пусто, а не «весь парк».
	if got, err := db.ListEnrolledDevices(ctx, "", empty.ID); err != nil {
		t.Fatalf("ListEnrolledDevices(empty group): %v", err)
	} else if len(got) != 0 {
		t.Errorf("пустая группа вернула %d устройств, want 0", len(got))
	}

	// Кривой UUID из URL сравнивается как group_id::text: пустая выдача, а не 22P02 → 500.
	if got, err := db.ListEnrolledDevices(ctx, "", "не-uuid-вовсе"); err != nil {
		t.Errorf("кривой group_id: err = %v, want nil", err)
	} else if len(got) != 0 {
		t.Errorf("кривой group_id вернул %d устройств, want 0", len(got))
	}
}

// Цвет группы едет вместе с устройством: иначе фронт ради рамки в списке тянул бы
// /device-groups вторым запросом и сопоставлял вручную.
func TestListEnrolledDevices_AttachesGroups(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)

	// Имена подобраны так, чтобы алфавитный порядок отличался от порядка вставки:
	// Groups сортируется по имени группы, а не по тому, куда устройство добавили раньше.
	zeta, err := db.CreateDeviceGroup(ctx, "zeta-"+suffix, "#0000ff")
	if err != nil {
		t.Fatalf("CreateDeviceGroup zeta: %v", err)
	}
	alpha, err := db.CreateDeviceGroup(ctx, "alpha-"+suffix, "#ff0000")
	if err != nil {
		t.Fatalf("CreateDeviceGroup alpha: %v", err)
	}

	both := activeDevice(t, db, "gboth-"+suffix, "Windows 11")
	lonely := activeDevice(t, db, "glonely-"+suffix, "Windows 11")
	for _, g := range []string{zeta.ID, alpha.ID} {
		if err := db.AddDeviceToGroup(ctx, both, g); err != nil {
			t.Fatalf("AddDeviceToGroup %s: %v", g, err)
		}
	}

	devices, err := db.ListEnrolledDevices(ctx, "", "")
	if err != nil {
		t.Fatalf("ListEnrolledDevices: %v", err)
	}
	byID := map[string]storage.Device{}
	for _, d := range devices {
		byID[d.ID] = d
	}

	got := byID[both].Groups
	if len(got) != 2 {
		t.Fatalf("устройство в двух группах получило %d ссылок: %+v", len(got), got)
	}
	if got[0].ID != alpha.ID || got[0].Name != alpha.Name || got[0].Color != "#ff0000" {
		t.Errorf("первая группа = %+v, ожидали alpha (%s, #ff0000)", got[0], alpha.Name)
	}
	if got[1].ID != zeta.ID || got[1].Color != "#0000ff" {
		t.Errorf("вторая группа = %+v, ожидали zeta (%s, #0000ff)", got[1], zeta.Name)
	}

	// Устройство без групп — пустой, но НЕ nil слайс: в JSON должно уехать [], не null.
	if groups := byID[lonely].Groups; groups == nil || len(groups) != 0 {
		t.Errorf("устройство без групп: Groups = %#v, want непустой указатель на пустой слайс", groups)
	}
}

// GetDevice отдаёт группы так же, как список: карточка устройства красит бейджи тем же
// цветом, что и строка в таблице.
func TestGetDevice_AttachesGroups(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	suffix := uniq(t)

	group, err := db.CreateDeviceGroup(ctx, "grp-card-"+suffix, "#00ff00")
	if err != nil {
		t.Fatalf("CreateDeviceGroup: %v", err)
	}
	dev := mustCreateDevice(t, db, "card-host-"+suffix, "windows")
	if err := db.AddDeviceToGroup(ctx, dev.ID, group.ID); err != nil {
		t.Fatalf("AddDeviceToGroup: %v", err)
	}

	got, _, err := db.GetDevice(ctx, dev.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if len(got.Groups) != 1 {
		t.Fatalf("Groups = %+v, ожидали одну группу", got.Groups)
	}
	if got.Groups[0].ID != group.ID || got.Groups[0].Name != group.Name || got.Groups[0].Color != "#00ff00" {
		t.Errorf("Groups[0] = %+v, want {%s %s #00ff00}", got.Groups[0], group.ID, group.Name)
	}

	// Устройство без групп — [] , не null.
	solo := mustCreateDevice(t, db, "solo-host-"+suffix, "windows")
	gotSolo, _, err := db.GetDevice(ctx, solo.ID)
	if err != nil {
		t.Fatalf("GetDevice solo: %v", err)
	}
	if gotSolo.Groups == nil || len(gotSolo.Groups) != 0 {
		t.Errorf("устройство без групп: Groups = %#v, want []", gotSolo.Groups)
	}
}
