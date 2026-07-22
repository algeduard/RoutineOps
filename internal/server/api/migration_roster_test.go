package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

type rosterListResponse struct {
	Summary struct {
		Total   int `json:"total"`
		Arrived int `json:"arrived"`
		Pending int `json:"pending"`
	} `json:"summary"`
	Entries []storage.MigrationRosterEntry `json:"entries"`
}

func importBody(batch, source string, rows []map[string]string) []byte {
	b, _ := json.Marshal(map[string]any{"batch_label": batch, "source_mdm": source, "rows": rows})
	return b
}

func entriesForBatch(entries []storage.MigrationRosterEntry, batch string) []storage.MigrationRosterEntry {
	var out []storage.MigrationRosterEntry
	for _, e := range entries {
		if e.BatchLabel == batch {
			out = append(out, e)
		}
	}
	return out
}

func TestMigrationRosterImportAndList(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	tok := authToken(t, rtr, db)
	batch := "api-batch-" + t.Name()

	body := importBody(batch, "Intune", []map[string]string{
		{"hostname": "PC-1", "serial_number": "SN-1", "assigned_user": "alice@corp"},
		{"hostname": "PC-2", "serial_number": "SN-2"},
	})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/migration-roster/import", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("import status = %d, body %s", w.Code, w.Body)
	}
	var imp struct {
		Inserted int `json:"inserted"`
		Received int `json:"received"`
	}
	json.NewDecoder(w.Body).Decode(&imp)
	if imp.Inserted != 2 || imp.Received != 2 {
		t.Fatalf("import result = %+v, want inserted=2 received=2", imp)
	}

	w = authedDo(t, rtr, http.MethodGet, "/api/v1/migration-roster", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var list rosterListResponse
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Summary.Arrived+list.Summary.Pending != list.Summary.Total {
		t.Fatalf("summary inconsistent: %+v", list.Summary)
	}
	mine := entriesForBatch(list.Entries, batch)
	if len(mine) != 2 {
		t.Fatalf("entries for batch = %d, want 2", len(mine))
	}
	// Никого не заводили под эти строки — обе pending.
	for _, e := range mine {
		if e.MatchedDeviceID != "" {
			t.Errorf("entry %s unexpectedly matched %s", e.Hostname, e.MatchedDeviceID)
		}
	}
}

// Заливка ростера — мутация, только it_admin (viewer → 403).
func TestMigrationRosterImport_ForbiddenForViewer(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	body := importBody("x", "", []map[string]string{{"hostname": "h"}})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/migration-roster/import", body, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer import status = %d, want 403", w.Code)
	}
}

func TestMigrationRosterImport_EmptyRows(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	tok := authToken(t, rtr, db)
	body := importBody("empty", "", []map[string]string{})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/migration-roster/import", body, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty import status = %d, want 400", w.Code)
	}
}

func TestMigrationRosterDelete(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	tok := authToken(t, rtr, db)
	batch := "api-del-" + t.Name()

	body := importBody(batch, "", []map[string]string{{"hostname": "d1"}, {"hostname": "d2"}})
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/migration-roster/import", body, tok); w.Code != http.StatusOK {
		t.Fatalf("import: %d", w.Code)
	}

	w := authedDo(t, rtr, http.MethodDelete, "/api/v1/migration-roster?batch="+batch, nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w.Code)
	}
	var del struct {
		Deleted int64 `json:"deleted"`
	}
	json.NewDecoder(w.Body).Decode(&del)
	if del.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2", del.Deleted)
	}
}

// DELETE без ?all и без ?batch — 400, чтобы пустой запрос не снёс весь ростер.
func TestMigrationRosterDelete_RequiresTarget(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	tok := authToken(t, rtr, db)
	w := authedDo(t, rtr, http.MethodDelete, "/api/v1/migration-roster", nil, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete without target = %d, want 400", w.Code)
	}
}

// Карточка устройства подтягивает метаданные из ростера по матчу hostname.
func TestDeviceMigrationInfo(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	tok := authToken(t, rtr, db)
	ctx := context.Background()

	host := "card-host-" + t.Name()
	dev, err := db.CreatePendingDevice(ctx, host, "windows")
	if err != nil {
		t.Fatalf("CreatePendingDevice: %v", err)
	}

	batch := "api-card-" + t.Name()
	body := importBody(batch, "Jamf", []map[string]string{
		{"hostname": host, "assigned_user": "carol@corp", "asset_tag": "TAG-9"},
	})
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/migration-roster/import", body, tok); w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}

	w := authedDo(t, rtr, http.MethodGet, fmt.Sprintf("/api/v1/devices/%s/migration-info", dev.ID), nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("migration-info status = %d", w.Code)
	}
	var entry *storage.MigrationRosterEntry
	if err := json.NewDecoder(w.Body).Decode(&entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entry == nil {
		t.Fatal("expected migration entry for device, got null")
	}
	if entry.AssignedUser != "carol@corp" || entry.AssetTag != "TAG-9" || entry.SourceMDM != "Jamf" {
		t.Fatalf("wrong entry: %+v", entry)
	}
	if entry.MatchedDeviceID != dev.ID {
		t.Fatalf("matched_device_id = %q, want %s", entry.MatchedDeviceID, dev.ID)
	}

	// Устройство не из импорта → null.
	other, _ := db.CreatePendingDevice(ctx, "no-roster-"+t.Name(), "linux")
	w = authedDo(t, rtr, http.MethodGet, fmt.Sprintf("/api/v1/devices/%s/migration-info", other.ID), nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("migration-info(other) status = %d", w.Code)
	}
	body2 := w.Body.String()
	if body2 != "null\n" && body2 != "null" {
		t.Fatalf("expected null for device not in roster, got %q", body2)
	}
}
