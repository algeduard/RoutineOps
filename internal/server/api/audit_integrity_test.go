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

func auditIntegrityRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false, api.WithAdminRoutes(api.AuditIntegrityRoutes(mgr)))
	return rtr, db
}

func TestAuditIntegrityLicensed(t *testing.T) {
	t.Setenv("ROUTINEOPS_AUDIT_HMAC_KEY", "api-audit-key")
	mgr := licensedManager(t, nil)
	rtr, db := auditIntegrityRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/audit-log/verify", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d, body %s", w.Code, w.Body)
	}
	var r struct {
		Configured bool `json:"configured"`
		Tampered   bool `json:"tampered"`
	}
	json.NewDecoder(w.Body).Decode(&r)
	if !r.Configured {
		t.Fatalf("ожидали configured=true (ключ задан): %+v", r)
	}
}

func TestAuditIntegrityRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := auditIntegrityRouter(t, mgr)
	tok := authToken(t, rtr, db)
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/audit-log/verify", nil, tok)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("без лицензии = %d, want 402", w.Code)
	}
}

func TestAuditIntegrityRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := auditIntegrityRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/audit-log/verify", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d, want 403", w.Code)
	}
}
