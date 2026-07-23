//go:build enterprise

package api_test

import (
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

func alertRoutingRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.AlertRoutingRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

// Без активной лицензии — 402 на всех методах, БД не трогаем.
func TestAlertRoutingRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := alertRoutingRouter(t, mgr)
	tok := authToken(t, rtr, db)

	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/alert-routing-rules", nil, tok); w.Code != http.StatusPaymentRequired {
		t.Fatalf("GET без лицензии = %d, want 402", w.Code)
	}
	body, _ := json.Marshal(map[string]any{"min_severity": "critical", "channel": "webhook", "target": "https://h.example/x"})
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/alert-routing-rules", body, tok); w.Code != http.StatusPaymentRequired {
		t.Fatalf("POST без лицензии = %d, want 402", w.Code)
	}
}

func TestAlertRoutingCRUD(t *testing.T) {
	mgr := licensedManager(t, nil) // пустой список фич = вся редакция
	rtr, db := alertRoutingRouter(t, mgr)
	tok := authToken(t, rtr, db)

	body, _ := json.Marshal(map[string]any{
		"min_severity": "critical", "channel": "telegram", "target": "-1001234567",
		"enabled": true, "escalate_after_minutes": 15,
	})
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/alert-routing-rules", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("создание правила = %d, body %s", w.Code, w.Body)
	}
	var rule storage.AlertRoutingRule
	json.NewDecoder(w.Body).Decode(&rule)
	if rule.ID == "" || rule.MinSeverity != "critical" || rule.Channel != "telegram" || rule.EscalateAfterMinutes != 15 {
		t.Fatalf("правило создано неверно: %+v", rule)
	}

	w = authedDo(t, rtr, http.MethodGet, "/api/v1/alert-routing-rules", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("список = %d", w.Code)
	}
	var rules []storage.AlertRoutingRule
	json.NewDecoder(w.Body).Decode(&rules)
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("ожидали 1 правило, got %+v", rules)
	}

	w = authedDo(t, rtr, http.MethodDelete, "/api/v1/alert-routing-rules/"+rule.ID, nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("удаление = %d", w.Code)
	}
	// Повторное удаление → 404.
	if w := authedDo(t, rtr, http.MethodDelete, "/api/v1/alert-routing-rules/"+rule.ID, nil, tok); w.Code != http.StatusNotFound {
		t.Fatalf("повторное удаление = %d, want 404", w.Code)
	}
}

func TestAlertRoutingValidation(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := alertRoutingRouter(t, mgr)
	tok := authToken(t, rtr, db)

	cases := []map[string]any{
		{"min_severity": "bogus", "channel": "webhook", "target": "https://h.example/x"}, // severity
		{"min_severity": "warning", "channel": "smoke", "target": "x"},                   // channel
		{"min_severity": "warning", "channel": "webhook", "target": "not-a-url"},         // webhook target
		{"min_severity": "warning", "channel": "telegram", "target": "not-a-number"},     // telegram target
		{"min_severity": "warning", "channel": "webhook", "target": ""},                  // empty target
	}
	for i, c := range cases {
		body, _ := json.Marshal(c)
		w := authedDo(t, rtr, http.MethodPost, "/api/v1/alert-routing-rules", body, tok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("кейс %d: код = %d, want 400 (body %s)", i, w.Code, w.Body)
		}
	}
}

// Правила — только it_admin (viewer → 403).
func TestAlertRoutingRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := alertRoutingRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/alert-routing-rules", nil, viewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer GET = %d, want 403", w.Code)
	}
}

// /capabilities отражает активную лицензию на маршрутизацию.
func TestCapabilitiesReflectsAlertRouting(t *testing.T) {
	mgr := licensedManager(t, []string{license.FeatureAlertRouting})
	rtr, db := alertRoutingRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d", w.Code)
	}
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeatureAlertRouting] {
		t.Fatalf("ожидали alert_routing=true, got %+v", caps)
	}
}
