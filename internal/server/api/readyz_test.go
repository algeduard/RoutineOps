package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReadyz_LiveDB_Returns200 — при живой БД readiness-проба отдаёт 200 и
// database:ok. asynqClient в тестовом роутере nil → Redis-проба пропускается
// (её отсутствие не должно валить готовность). Публичный роут: без токена.
func TestReadyz_LiveDB_Returns200(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)

	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("readyz: got %d, want 200; body: %s", w.Code, w.Body)
	}
	var resp struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "ready" {
		t.Errorf("status = %q, want %q", resp.Status, "ready")
	}
	if resp.Checks["database"] != "ok" {
		t.Errorf("checks.database = %q, want %q", resp.Checks["database"], "ok")
	}
	// Redis-клиента в тесте нет → проба не должна фигурировать в ответе.
	if _, present := resp.Checks["redis"]; present {
		t.Errorf("checks.redis present unexpectedly (asynqClient is nil): %v", resp.Checks)
	}
}

// TestHealthz_StillLiveness — /healthz остаётся простой liveness-пробой (200 ok)
// и readiness-фича его не сломала.
func TestHealthz_StillLiveness(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)

	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("healthz: got %d, want 200; body: %s", w.Code, w.Body)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}
}
