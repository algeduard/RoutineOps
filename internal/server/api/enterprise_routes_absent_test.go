package api_test

import (
	"net/http"
	"testing"
)

// В open-core сборке enterprise-роуты /capabilities и /devices/{id}/software/remove не
// смонтированы (NewRouter без опций) → 404. Как и /license (см. license_absent_test.go),
// код 404 позволяет UI отличить «недоступно в этой редакции» от сбоя: капабилити-хук
// на 404 трактует enterprise-фичи как выключенные, а не как «сломался сервер». Тест без
// build-тега: держит инвариант и в open-core, и в enterprise-сборке (там newRouterWithDB
// тоже без опций — роуты появляются лишь при явном WithRoutes/WithAdminRoutes).
func TestEnterpriseRoutesAbsentWithoutOptions(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	token := authToken(t, rtr, db)

	for _, tc := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, "/api/v1/capabilities", nil},
		{http.MethodPost, "/api/v1/devices/00000000-0000-0000-0000-000000000000/software/remove", []byte(`{"name":"x"}`)},
		{http.MethodGet, "/api/v1/siem/config", nil},
		{http.MethodGet, "/api/v1/audit-log/verify", nil},
		{http.MethodGet, "/api/v1/cve/findings", nil},
		{http.MethodGet, "/api/v1/tenants", nil},
	} {
		w := authedDo(t, rtr, tc.method, tc.path, tc.body, token)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 (роут не смонтирован без enterprise-опций)", tc.method, tc.path, w.Code)
		}
	}
}
