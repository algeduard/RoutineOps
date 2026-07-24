//go:build enterprise

package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func policyAsCodeRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	// Изоляция на общей БД: реконсиляция работает по ГЛОБАЛЬНЫМ software-правилам, им нужен
	// чистый бэйзлайн; декларации тоже сносим (их пишет только эта фича).
	ctx := context.Background()
	if _, err := db.Pool().Exec(ctx, `DELETE FROM policy_declaration`); err != nil {
		t.Fatalf("clean policy_declaration: %v", err)
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM software_policy_rules WHERE device_id IS NULL AND group_id IS NULL`); err != nil {
		t.Fatalf("clean global rules: %v", err)
	}
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.PolicyAsCodeRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

type driftResp struct {
	ToCreate []storage.DesiredPolicyRule `json:"to_create"`
	ToDelete []storage.DesiredPolicyRule `json:"to_delete"`
	InSync   int                         `json:"in_sync"`
}

type applyResp struct {
	Rules   int `json:"rules"`
	Created int `json:"created"`
	Deleted int `json:"deleted"`
}

// E2E за лицензией: apply 2 правил → 2 создано; ручное 3-е → drift to_delete=1; повторный
// apply той же декларации → идемпотентно (снимает лишнее, затем 0 изменений).
func TestPolicyAsCodeApplyDriftIdempotent(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := policyAsCodeRouter(t, mgr)
	tok := authToken(t, rtr, db)

	apply := map[string]any{"rules": []map[string]any{
		{"software_name": "utorrent", "rule_type": "forbidden", "platforms": []string{"Windows"}},
		{"software_name": "slack", "rule_type": "allowed"},
	}}
	body, _ := json.Marshal(apply)
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/policy-as-code/apply", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("POST apply = %d, body %s", w.Code, w.Body)
	}
	var res applyResp
	json.NewDecoder(w.Body).Decode(&res)
	if res.Created != 2 || res.Deleted != 0 || res.Rules != 2 {
		t.Fatalf("apply resp = %+v, want rules=2 created=2 deleted=0", res)
	}

	// GET /policy-as-code — декларация + дрейф (сразу без дрейфа).
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/policy-as-code", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /policy-as-code = %d", w.Code)
	}
	var full struct {
		Declaration *struct {
			Content   []storage.DesiredPolicyRule `json:"content"`
			AppliedBy string                      `json:"applied_by"`
		} `json:"declaration"`
		Drift driftResp `json:"drift"`
	}
	json.NewDecoder(w.Body).Decode(&full)
	if full.Declaration == nil || len(full.Declaration.Content) != 2 {
		t.Fatalf("declaration missing or wrong size: %+v", full.Declaration)
	}
	if full.Drift.InSync != 2 || len(full.Drift.ToDelete) != 0 {
		t.Fatalf("drift after apply: %+v, want in_sync=2 no deletes", full.Drift)
	}

	// Кто-то добавил 3-е глобальное правило мимо декларации.
	if _, err := db.CreatePolicyRule(context.Background(), "steam", "forbidden", nil, nil); err != nil {
		t.Fatalf("manual CreatePolicyRule: %v", err)
	}
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/policy-as-code/drift", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET drift = %d", w.Code)
	}
	var d driftResp
	json.NewDecoder(w.Body).Decode(&d)
	if d.InSync != 2 || len(d.ToCreate) != 0 || len(d.ToDelete) != 1 {
		t.Fatalf("drift after manual add: %+v, want in_sync=2 create=0 delete=1", d)
	}
	if d.ToDelete[0].SoftwareName != "steam" {
		t.Fatalf("to_delete[0] = %+v, want steam", d.ToDelete[0])
	}

	// Повторный apply той же декларации — снимает лишнее (deleted=1).
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/policy-as-code/apply", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("re-apply = %d", w.Code)
	}
	json.NewDecoder(w.Body).Decode(&res)
	if res.Created != 0 || res.Deleted != 1 {
		t.Fatalf("re-apply resp = %+v, want created=0 deleted=1", res)
	}

	// Идемпотентность: ещё раз — 0 изменений.
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/policy-as-code/apply", body, tok)
	json.NewDecoder(w.Body).Decode(&res)
	if res.Created != 0 || res.Deleted != 0 {
		t.Fatalf("idempotent apply resp = %+v, want created=0 deleted=0", res)
	}
}

// Пустая декларация без confirm_empty — 400 (защита от случайного сноса всех правил).
func TestPolicyAsCodeEmptyRequiresConfirm(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := policyAsCodeRouter(t, mgr)
	tok := authToken(t, rtr, db)

	if _, err := db.CreatePolicyRule(context.Background(), "keep-app", "forbidden", nil, nil); err != nil {
		t.Fatalf("CreatePolicyRule: %v", err)
	}

	// Без confirm_empty → 400, правило на месте.
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/policy-as-code/apply", []byte(`{"rules":[]}`), tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty apply w/o confirm = %d, want 400", w.Code)
	}
	// С confirm_empty → сносит.
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/policy-as-code/apply", []byte(`{"rules":[],"confirm_empty":true}`), tok)
	if w.Code != http.StatusOK {
		t.Fatalf("empty apply w/ confirm = %d, body %s", w.Code, w.Body)
	}
	var res applyResp
	json.NewDecoder(w.Body).Decode(&res)
	if res.Deleted != 1 {
		t.Fatalf("confirmed empty apply deleted=%d, want 1", res.Deleted)
	}
}

// Невалидное правило (плохой rule_type) отвергает всю декларацию — 400.
func TestPolicyAsCodeApplyValidatesRules(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := policyAsCodeRouter(t, mgr)
	tok := authToken(t, rtr, db)

	bad := `{"rules":[{"software_name":"ok","rule_type":"bogus"}]}`
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/policy-as-code/apply", []byte(bad), tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad rule_type = %d, want 400", w.Code)
	}

	badName := `{"rules":[{"software_name":"  ","rule_type":"forbidden"}]}`
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/policy-as-code/apply", []byte(badName), tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty software_name = %d, want 400", w.Code)
	}
}

// Без активной лицензии на фичу — 402 на всех роутах.
func TestPolicyAsCodeRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := policyAsCodeRouter(t, mgr)
	tok := authToken(t, rtr, db)

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/policy-as-code"},
		{http.MethodGet, "/api/v1/policy-as-code/drift"},
		{http.MethodPost, "/api/v1/policy-as-code/apply"},
	} {
		w := authedDo(t, rtr, tc.method, tc.path, []byte(`{"rules":[]}`), tok)
		if w.Code != http.StatusPaymentRequired {
			t.Errorf("%s %s без лицензии = %d, want 402", tc.method, tc.path, w.Code)
		}
	}
}

// Роуты — только it_admin (viewer → 403).
func TestPolicyAsCodeRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := policyAsCodeRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/policy-as-code", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /policy-as-code = %d, want 403", w.Code)
	}
}

// /capabilities отражает лицензию на policy-as-code.
func TestPolicyAsCodeCapability(t *testing.T) {
	mgr := licensedManager(t, []string{license.FeaturePolicyAsCode})
	rtr, db := policyAsCodeRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /capabilities = %d", w.Code)
	}
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeaturePolicyAsCode] {
		t.Fatalf("ожидали policy_as_code=true, got %+v", caps)
	}
}
