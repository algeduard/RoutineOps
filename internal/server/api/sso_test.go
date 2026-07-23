package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Без провайдера (open-core / enterprise без конфига+лицензии) публичные SSO-роуты:
// login/callback → 404, status → 200 {enabled:false}.
func TestSSORoutesDisabledWithoutProvider(t *testing.T) {
	rtr, _ := newRouterWithDB(t) // без WithSSO → h.sso == nil
	for _, path := range []string{"/api/v1/auth/sso/login", "/api/v1/auth/sso/callback"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		rtr.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/status", nil)
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

// SSO-аккаунт (auth_source='oidc') не имеет локального пароля — вход по паролю запрещён.
func TestSSOUserCannotPasswordLogin(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	email := "ssoacct_" + t.Name() + "@test.com"
	if _, err := db.CreateSSOUser(context.Background(), "SSO", email, "viewer", "https://idp.example", "sub-login"); err != nil {
		t.Fatal(err)
	}
	w := doLogin(t, rtr, email, "anything-goes")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("oidc password login: got %d, want 401", w.Code)
	}
	if hasTokenCookie(w) {
		t.Fatal("SSO-аккаунт не должен получить сессию по паролю")
	}
}

// reset-password для SSO-аккаунта отвергается даже при наличии валидного reset-токена
// (defense-in-depth: forgot-password токен для oidc и не выпускает). Закрывает бэкдор
// «получить локальный пароль в обход IdP».
func TestSSOUserResetPasswordRejected(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	ctx := context.Background()
	email := "ssoreset_" + t.Name() + "@test.com"
	u, err := db.CreateSSOUser(ctx, "SSO", email, "viewer", "https://idp.example", "sub-reset")
	if err != nil {
		t.Fatal(err)
	}
	token := "resettok-" + t.Name()
	if err := db.CreatePasswordResetToken(ctx, u.ID, token); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"token": token, "password": "NewPass123!"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("reset oidc: got %d, want 400", w.Code)
	}
	// И после попытки аккаунт по-прежнему не логинится этим паролем.
	if lw := doLogin(t, rtr, email, "NewPass123!"); lw.Code == http.StatusOK {
		t.Fatal("SSO-аккаунт получил локальный пароль через reset — бэкдор!")
	}
}
