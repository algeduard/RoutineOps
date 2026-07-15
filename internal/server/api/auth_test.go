package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func seedUser(t *testing.T, db *storage.DB, email, password, role string) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	_, err := db.CreateUser(context.Background(), "Test User", email, string(hash), role)
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
}

func doLogin(t *testing.T, rtr http.Handler, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)
	return w
}

func TestLogin_ValidCredentials_Returns200(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	seedUser(t, db, "admin@test.com", "secret123", "admin")

	w := doLogin(t, rtr, "admin@test.com", "secret123")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", w.Code, w.Body)
	}
	// Токен теперь только в httpOnly cookie, не в теле ответа (12bc96f).
	var tokenCookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == "token" {
			tokenCookie = c.Value
		}
	}
	if tokenCookie == "" {
		t.Error("expected non-empty token cookie")
	}
}

func TestLogin_WrongPassword_Returns401(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	seedUser(t, db, "admin2@test.com", "secret123", "admin")

	w := doLogin(t, rtr, "admin2@test.com", "wrong")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestLogin_UnknownEmail_Returns401(t *testing.T) {
	rtr, _ := newRouterWithDB(t)
	w := doLogin(t, rtr, "nobody@test.com", "pass")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestLogin_EmptyFields_Returns400(t *testing.T) {
	w := doLogin(t, newRouter(t), "", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestForgotPassword_ExistingUser_Returns200(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	seedUser(t, db, "forgot@test.com", "secret123", "admin")

	body, _ := json.Marshal(map[string]string{"email": "forgot@test.com"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestForgotPassword_UnknownEmail_Returns200(t *testing.T) {
	rtr, _ := newRouterWithDB(t)

	body, _ := json.Marshal(map[string]string{"email": "unknown@test.com"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestForgotPassword_MissingEmail_Returns400(t *testing.T) {
	rtr, _ := newRouterWithDB(t)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestResetPassword_ValidToken_Returns200(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	seedUser(t, db, "reset@test.com", "secret123", "admin")
	user, _ := db.GetUserByEmail(context.Background(), "reset@test.com")

	db.CreatePasswordResetToken(context.Background(), user.ID, "valid-token")

	body, _ := json.Marshal(map[string]string{
		"token":    "valid-token",
		"password": "Newpass1!",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestResetPassword_ExpiredToken_Returns400(t *testing.T) {
	rtr, _ := newRouterWithDB(t)

	body, _ := json.Marshal(map[string]string{
		"token":    "expired-token",
		"password": "Newpass1!",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestResetPassword_TokenAlreadyUsed_Returns400(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	seedUser(t, db, "reset2@test.com", "secret123", "admin")
	user, _ := db.GetUserByEmail(context.Background(), "reset2@test.com")

	db.CreatePasswordResetToken(context.Background(), user.ID, "used-token")

	body, _ := json.Marshal(map[string]string{
		"token":    "used-token",
		"password": "Newpass1!",
	})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(body))
	w2 := httptest.NewRecorder()
	rtr.ServeHTTP(w2, r2)

	if w2.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 on second use", w2.Code)
	}
}

func TestResetPassword_LowComplexity_Returns400(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	seedUser(t, db, "reset-cplx@test.com", "secret123", "admin")
	user, _ := db.GetUserByEmail(context.Background(), "reset-cplx@test.com")

	db.CreatePasswordResetToken(context.Background(), user.ID, "cplx-token")

	body, _ := json.Marshal(map[string]string{
		"token":    "cplx-token",
		"password": "alllowercase", // ≥8 символов, но только 1 класс — отклоняется (M-6)
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 for low-complexity password", w.Code)
	}
}

func TestResetPassword_ShortPassword_Returns400(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	seedUser(t, db, "reset3@test.com", "secret123", "admin")
	user, _ := db.GetUserByEmail(context.Background(), "reset3@test.com")

	db.CreatePasswordResetToken(context.Background(), user.ID, "short-token")

	body, _ := json.Marshal(map[string]string{
		"token":    "short-token",
		"password": "short", // < 8
	})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}
