package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// setSerial проставляет серийник существующему устройству (инвентарь в тестах не гоняем).
func setSerial(t *testing.T, db *storage.DB, deviceID, serial string) {
	t.Helper()
	if _, err := db.Pool().Exec(context.Background(),
		`UPDATE devices SET serial_number = $1 WHERE id = $2`, serial, deviceID); err != nil {
		t.Fatalf("setSerial: %v", err)
	}
}

// rosterForBatch — записи ростера только нужной партии (тест-БД общая между тестами,
// ListMigrationRoster отдаёт весь ростер).
func rosterForBatch(t *testing.T, db *storage.DB, batch string) []storage.MigrationRosterEntry {
	t.Helper()
	all, err := db.ListMigrationRoster(context.Background())
	if err != nil {
		t.Fatalf("ListMigrationRoster: %v", err)
	}
	var out []storage.MigrationRosterEntry
	for _, e := range all {
		if e.BatchLabel == batch {
			out = append(out, e)
		}
	}
	return out
}

func entryByHost(entries []storage.MigrationRosterEntry, host string) *storage.MigrationRosterEntry {
	for i := range entries {
		if entries[i].Hostname == host {
			return &entries[i]
		}
	}
	return nil
}

func TestImportAndMatchMigrationRoster(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	batch := "batch-" + uniq(t)

	// Устройство, которое сматчится по серийнику.
	devSerial := mustCreateDevice(t, db, "host-serial-"+uniq(t), "windows")
	serial := "SN-" + uniq(t)
	setSerial(t, db, devSerial.ID, serial)

	// Устройство, которое сматчится по hostname (строка ростера без серийника).
	hostOnly := "host-only-" + uniq(t)
	devHost := mustCreateDevice(t, db, hostOnly, "windows")

	rows := []storage.MigrationRosterRow{
		{Hostname: "whatever", SerialNumber: serial, AssignedUser: "alice@corp"}, // матч по serial
		{Hostname: hostOnly}, // матч по hostname
		{Hostname: "ghost-" + uniq(t), SerialNumber: "SN-missing-" + uniq(t)}, // не приехал
	}
	inserted, err := db.ImportMigrationRoster(ctx, batch, "Intune", "admin@corp", rows)
	if err != nil {
		t.Fatalf("ImportMigrationRoster: %v", err)
	}
	if inserted != 3 {
		t.Fatalf("inserted = %d, want 3", inserted)
	}

	entries := rosterForBatch(t, db, batch)
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	// Матч по серийнику: hostname в ростере ("whatever") другой, но serial совпал.
	bySerial := entryByHost(entries, "whatever")
	if bySerial == nil || bySerial.MatchedDeviceID != devSerial.ID {
		t.Fatalf("serial-row matched = %+v, want device %s", bySerial, devSerial.ID)
	}
	if bySerial.AssignedUser != "alice@corp" {
		t.Errorf("assigned_user not preserved: %q", bySerial.AssignedUser)
	}

	// Матч по hostname (в строке нет серийника).
	byHost := entryByHost(entries, hostOnly)
	if byHost == nil || byHost.MatchedDeviceID != devHost.ID {
		t.Fatalf("hostname-row matched = %+v, want device %s", byHost, devHost.ID)
	}

	// Не приехавшая строка.
	arrived := 0
	for _, e := range entries {
		if e.MatchedDeviceID != "" {
			arrived++
		}
	}
	if arrived != 2 {
		t.Fatalf("arrived = %d, want 2 (one row must stay pending)", arrived)
	}
}

// Серийник — сильный ключ: если он в строке есть, hostname для матча не используется.
func TestMigrationRosterSerialPreferredOverHostname(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	batch := "batch-" + uniq(t)

	sameHost := "shared-host-" + uniq(t)
	rowSerial := "SN-row-" + uniq(t)

	// Устройство с ТЕМ ЖЕ hostname, но без нужного серийника — матчиться НЕ должно,
	// потому что строка ростера несёт серийник, а он у устройства другой.
	decoy := mustCreateDevice(t, db, sameHost, "windows")
	setSerial(t, db, decoy.ID, "SN-other-"+uniq(t))

	rows := []storage.MigrationRosterRow{{Hostname: sameHost, SerialNumber: rowSerial}}
	if _, err := db.ImportMigrationRoster(ctx, batch, "", "admin@corp", rows); err != nil {
		t.Fatalf("ImportMigrationRoster: %v", err)
	}

	entries := rosterForBatch(t, db, batch)
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}
	if entries[0].MatchedDeviceID != "" {
		t.Fatalf("matched decoy by hostname despite serial mismatch: %s", entries[0].MatchedDeviceID)
	}

	// Теперь заводим устройство с правильным серийником (другой hostname) — оно сматчится.
	real := mustCreateDevice(t, db, "elsewhere-"+uniq(t), "windows")
	setSerial(t, db, real.ID, rowSerial)

	entries = rosterForBatch(t, db, batch)
	if entries[0].MatchedDeviceID != real.ID {
		t.Fatalf("matched = %q, want %s (by serial)", entries[0].MatchedDeviceID, real.ID)
	}
}

// Повторная заливка того же CSV не задваивает строки (идемпотентность).
func TestMigrationRosterDedup(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	batch := "batch-" + uniq(t)
	rows := []storage.MigrationRosterRow{
		{Hostname: "dup-a-" + uniq(t), SerialNumber: "SN-a"},
		{Hostname: "dup-b-" + uniq(t), SerialNumber: "SN-b"},
	}
	n1, err := db.ImportMigrationRoster(ctx, batch, "", "admin@corp", rows)
	if err != nil || n1 != 2 {
		t.Fatalf("first import: n=%d err=%v", n1, err)
	}
	n2, err := db.ImportMigrationRoster(ctx, batch, "", "admin@corp", rows)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("re-import inserted %d, want 0 (idempotent)", n2)
	}
	if got := len(rosterForBatch(t, db, batch)); got != 2 {
		t.Fatalf("roster size = %d after re-import, want 2", got)
	}
}

// Фикс находки ревью: дедуп-ключ = ключ матча. Один serial с РАЗНЫМИ hostname в одной
// партии — это одна машина (её матчат по serial), поэтому вторая строка должна
// отбрасываться, а не задваивать устройство в прогрессе.
func TestMigrationRosterDedup_SameSerialDifferentHostname(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	batch := "batch-" + uniq(t)
	serial := "SN-rename-" + uniq(t)
	rows := []storage.MigrationRosterRow{
		{Hostname: "OLD-NAME-" + uniq(t), SerialNumber: serial},
		{Hostname: "NEW-NAME-" + uniq(t), SerialNumber: serial}, // тот же serial, другой hostname
	}
	inserted, err := db.ImportMigrationRoster(ctx, batch, "", "admin@corp", rows)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("inserted = %d, want 1 (same serial must dedup to one machine)", inserted)
	}
	if got := len(rosterForBatch(t, db, batch)); got != 1 {
		t.Fatalf("roster has %d rows for one serial, want 1", got)
	}
}

// Строки без hostname И без serial отбрасываются: матчить нечем.
func TestMigrationRosterSkipsEmptyRows(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	batch := "batch-" + uniq(t)
	rows := []storage.MigrationRosterRow{
		{Hostname: "keep-" + uniq(t)},
		{AssignedUser: "nobody@corp"}, // ни hostname, ни serial — мусор
		{Notes: "just a note"},
	}
	n, err := db.ImportMigrationRoster(ctx, batch, "", "admin@corp", rows)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted = %d, want 1 (two empty rows dropped)", n)
	}
}

func TestMigrationRosterForDevice(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	batch := "batch-" + uniq(t)

	host := "rev-" + uniq(t)
	dev := mustCreateDevice(t, db, host, "windows")

	if _, err := db.ImportMigrationRoster(ctx, batch, "Kandji", "admin@corp",
		[]storage.MigrationRosterRow{{Hostname: host, AssignedUser: "bob@corp", AssetTag: "ASSET-42"}}); err != nil {
		t.Fatalf("import: %v", err)
	}

	entry, err := db.MigrationRosterForDevice(ctx, dev.ID)
	if err != nil {
		t.Fatalf("MigrationRosterForDevice: %v", err)
	}
	if entry == nil {
		t.Fatal("expected a roster entry for the device, got nil")
	}
	if entry.AssignedUser != "bob@corp" || entry.AssetTag != "ASSET-42" || entry.SourceMDM != "Kandji" {
		t.Fatalf("wrong roster metadata: %+v", entry)
	}

	// Устройство не из импортированного парка — nil.
	other := mustCreateDevice(t, db, "not-in-roster-"+uniq(t), "linux")
	entry, err = db.MigrationRosterForDevice(ctx, other.ID)
	if err != nil {
		t.Fatalf("MigrationRosterForDevice(other): %v", err)
	}
	if entry != nil {
		t.Fatalf("expected nil for device not in roster, got %+v", entry)
	}
}

func TestDeleteMigrationRoster(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	batchA := "del-a-" + uniq(t)
	batchB := "del-b-" + uniq(t)

	_, _ = db.ImportMigrationRoster(ctx, batchA, "", "admin@corp",
		[]storage.MigrationRosterRow{{Hostname: "a1-" + uniq(t)}, {Hostname: "a2-" + uniq(t)}})
	_, _ = db.ImportMigrationRoster(ctx, batchB, "", "admin@corp",
		[]storage.MigrationRosterRow{{Hostname: "b1-" + uniq(t)}})

	deleted, err := db.DeleteMigrationRoster(ctx, batchA, false)
	if err != nil {
		t.Fatalf("DeleteMigrationRoster(batchA): %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	if got := len(rosterForBatch(t, db, batchA)); got != 0 {
		t.Fatalf("batchA still has %d rows", got)
	}
	if got := len(rosterForBatch(t, db, batchB)); got != 1 {
		t.Fatalf("batchB should be untouched, has %d", got)
	}
}

// Регресс: пустой batchLabel при all=false удаляет ТОЛЬКО безымянную партию, а не весь
// ростер (раньше пустая метка означала «снести всё» — прицельно удалить безымянную
// партию было нельзя, а случайный ?batch= сносил всё).
func TestDeleteMigrationRoster_EmptyBatchIsNotWipeAll(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	labeled := "keep-" + uniq(t)

	// Помеченная партия, которую трогать нельзя.
	if _, err := db.ImportMigrationRoster(ctx, labeled, "", "admin@corp",
		[]storage.MigrationRosterRow{{Hostname: "lab1-" + uniq(t)}, {Hostname: "lab2-" + uniq(t)}}); err != nil {
		t.Fatalf("import labeled: %v", err)
	}
	// Безымянная партия (batch_label = '').
	if _, err := db.ImportMigrationRoster(ctx, "", "", "admin@corp",
		[]storage.MigrationRosterRow{{Hostname: "anon-" + uniq(t)}}); err != nil {
		t.Fatalf("import anon: %v", err)
	}

	before := len(rosterForBatch(t, db, labeled))
	if _, err := db.DeleteMigrationRoster(ctx, "", false); err != nil {
		t.Fatalf("delete empty batch: %v", err)
	}
	if got := len(rosterForBatch(t, db, labeled)); got != before {
		t.Fatalf("labeled batch changed from %d to %d — empty-batch delete wiped too much", before, got)
	}
}
