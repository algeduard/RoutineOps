package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Без провайдера (open-core / enterprise без конфига+лицензии) публичные SAML-роуты:
// metadata/login → 404 (GET), acs → 404 (POST), status → 200 {enabled:false}.
func TestSAMLRoutesDisabledWithoutProvider(t *testing.T) {
	rtr, _ := newRouterWithDB(t) // без WithSAML → h.saml == nil
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/auth/saml/metadata"},
		{http.MethodGet, "/api/v1/auth/saml/login"},
		{http.MethodPost, "/api/v1/auth/saml/acs"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		rtr.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: got %d, want 404", tc.method, tc.path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/status", nil)
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var s struct {
		Enabled bool `json:"enabled"`
	}
	json.Unmarshal(w.Body.Bytes(), &s)
	if s.Enabled {
		t.Error("enabled должен быть false без провайдера")
	}
}
