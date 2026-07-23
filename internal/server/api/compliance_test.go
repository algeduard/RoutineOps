//go:build enterprise

package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func complianceRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithAdminRoutes(api.ComplianceRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

func TestComplianceReportLicensed(t *testing.T) {
	mgr := licensedManager(t, nil) // пустой список фич = вся редакция
	rtr, db := complianceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/compliance/report", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("licensed report = %d, body %s", w.Code, w.Body)
	}
	var rep storage.ComplianceReport
	if err := json.NewDecoder(w.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Score < 0 || rep.Score > 100 {
		t.Fatalf("score = %d вне 0..100", rep.Score)
	}
	if len(rep.Checks) == 0 {
		t.Fatal("ожидали непустой список проверок")
	}
	for _, c := range rep.Checks {
		if c.ID == "" || c.Category == "" || c.Status == "" {
			t.Fatalf("проверка без обязательных полей: %+v", c)
		}
	}
}

// Без активной лицензии на фичу — 402, отчёт не считаем.
func TestComplianceReportRequiresLicense(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	mgr := license.NewManager(pub, 0, "") // лицензия не применена
	rtr, db := complianceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/compliance/report", nil, tok)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("без лицензии = %d, want 402", w.Code)
	}
}

// Отчёт соответствия — только it_admin (viewer → 403).
func TestComplianceReportRequiresAdmin(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := complianceRouter(t, mgr)
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/compliance/report", nil, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d, want 403", w.Code)
	}
}

// CSV-экспорт: text/csv, заголовок колонок и завершающая строка overall со скором.
func TestComplianceReportCSVExport(t *testing.T) {
	mgr := licensedManager(t, nil)
	rtr, db := complianceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/compliance/report?format=csv", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("csv export = %d, body %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, ожидали text/csv", ct)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "id,title,category,status,passed,total,detail") {
		t.Fatalf("нет CSV-шапки, тело: %q", body[:min(80, len(body))])
	}
	if !strings.Contains(body, "overall,") {
		t.Fatalf("нет итоговой строки overall в CSV")
	}
}

// /capabilities отражает активную лицензию compliance.
func TestComplianceCapability(t *testing.T) {
	mgr := licensedManager(t, []string{license.FeatureCompliance})
	rtr, db := complianceRouter(t, mgr)
	tok := authToken(t, rtr, db)

	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d", w.Code)
	}
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeatureCompliance] {
		t.Fatalf("ожидали compliance=true, got %+v", caps)
	}
}
