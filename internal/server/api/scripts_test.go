package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// createScript — хелпер: создаёт скрипт через API и возвращает его id.
func createScript(t *testing.T, rtr http.Handler, tok, name, platform string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "platform": platform, "content": "echo hi"})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/scripts", body, tok)
	if w.Code != http.StatusCreated {
		t.Fatalf("createScript %s: %d %s", name, w.Code, w.Body)
	}
	var s map[string]any
	json.NewDecoder(w.Body).Decode(&s)
	return s["id"].(string)
}

func TestCreateScript_HappyPath_Returns201(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	for _, platform := range []string{"linux", "macOS", "Windows"} {
		body, _ := json.Marshal(map[string]string{
			"name": "script-happy-" + platform, "platform": platform, "content": "echo hello",
		})
		w := authedDo(t, rtr, http.MethodPost, "/api/v1/scripts", body, tok)
		if w.Code != http.StatusCreated {
			t.Fatalf("platform %s: got %d, want 201; body: %s", platform, w.Code, w.Body)
		}
		var s map[string]any
		json.NewDecoder(w.Body).Decode(&s)
		if s["platform"] != platform {
			t.Errorf("platform = %q, want %q", s["platform"], platform)
		}
		if id, _ := s["id"].(string); id == "" {
			t.Error("expected non-empty script id")
		}
	}
}

func TestCreateScript_EmptyFields_Returns400(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	cases := map[string]map[string]string{
		"no name":     {"name": "", "platform": "linux", "content": "echo hi"},
		"no platform": {"name": "x", "platform": "", "content": "echo hi"},
		"no content":  {"name": "x", "platform": "linux", "content": ""},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(payload)
			w := authedDo(t, rtr, http.MethodPost, "/api/v1/scripts", body, tok)
			if w.Code != http.StatusBadRequest {
				t.Errorf("got %d, want 400; body: %s", w.Code, w.Body)
			}
		})
	}
}

func TestCreateScript_InvalidPlatform_Returns400(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]string{"name": "x", "platform": "freebsd", "content": "echo hi"})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/scripts", body, tok)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400; body: %s", w.Code, w.Body)
	}
}

func TestUpdateScript_Returns200(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	id := createScript(t, rtr, tok, "script-to-update", "linux")

	body, _ := json.Marshal(map[string]string{"name": "renamed", "platform": "Windows", "content": "Write-Host hi"})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/scripts/"+id, body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", w.Code, w.Body)
	}
	var s map[string]any
	json.NewDecoder(w.Body).Decode(&s)
	if s["name"] != "renamed" || s["platform"] != "Windows" {
		t.Errorf("update not applied: name=%q platform=%q", s["name"], s["platform"])
	}
}

func TestUpdateScript_NotFound_Returns404(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]string{"name": "x", "platform": "linux", "content": "echo hi"})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/scripts/00000000-0000-0000-0000-000000000000", body, tok)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404; body: %s", w.Code, w.Body)
	}
}

func TestDeleteScript_Returns204(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	id := createScript(t, rtr, tok, "script-to-delete", "macOS")

	w := authedDo(t, rtr, http.MethodDelete, "/api/v1/scripts/"+id, nil, tok)
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204; body: %s", w.Code, w.Body)
	}

	// после удаления GET должен вернуть 404
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/scripts/"+id, nil, tok)
	if w.Code != http.StatusNotFound {
		t.Errorf("after delete GET got %d, want 404", w.Code)
	}
}

func TestListScripts_Empty_Returns200(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/scripts", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", w.Code, w.Body)
	}
	var scripts []any
	if err := json.NewDecoder(w.Body).Decode(&scripts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if scripts == nil {
		t.Error("expected non-nil array (even when empty)")
	}
}

func TestListScripts_WithData_Returns200(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	id := createScript(t, rtr, tok, "script-in-list", "linux")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/scripts", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", w.Code, w.Body)
	}
	var scripts []map[string]any
	json.NewDecoder(w.Body).Decode(&scripts)

	var found bool
	for _, s := range scripts {
		if s["id"] == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created script %s not present in list (%d scripts)", id, len(scripts))
	}
}

func TestScripts_Unauthenticated_Returns401(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)

	body, _ := json.Marshal(map[string]string{"name": "x", "platform": "linux", "content": "echo hi"})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/scripts", body, "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}
