package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// org_settings — singleton, ОБЩИЙ для всех тестов пакета. Оставленная политика 'it_admin'/'all'
// залочила бы каждого последующего админа/viewer без MFA, поэтому любой тест, меняющий политику,
// обязан вернуть её в пусто (выкл) на cleanup.
func resetMFAPolicyOnCleanup(t *testing.T, db *storage.DB) {
	t.Helper()
	t.Cleanup(func() { _ = db.SetMFARequiredRole(context.Background(), "") })
}

func getMFAPolicyValue(t *testing.T, w *http.Response) string {
	t.Helper()
	var body struct {
		MFARequiredRole string `json:"mfa_required_role"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode mfa policy: %v", err)
	}
	return body.MFARequiredRole
}

func meMFARequired(t *testing.T, rtr http.Handler, token string) (status int, mfaRequired string) {
	t.Helper()
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/me", nil, token)
	if w.Code == http.StatusOK {
		var m map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode /me: %v", err)
		}
		return w.Code, m["mfa_required"]
	}
	return w.Code, ""
}

// Политика по умолчанию пусто (выкл) и валидация значения. Пока политика выключена — админ без
// MFA не гейтится, поэтому GET/PUT работают штатно.
func TestMFAPolicyDefaultAndValidation(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	resetMFAPolicyOnCleanup(t, db)
	adminTok := tokenForRole(t, rtr, db, "it_admin", "mfapoldef_")

	// Дефолт — политика выключена.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/settings/mfa-policy", nil, adminTok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET policy: %d %s", w.Code, w.Body)
	}
	if got := getMFAPolicyValue(t, w.Result()); got != "" {
		t.Fatalf("default policy = %q, want '' (off)", got)
	}

	// Недопустимое значение → 400 (политика не меняется).
	bad, _ := json.Marshal(map[string]string{"mfa_required_role": "superadmin"})
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/settings/mfa-policy", bad, adminTok); w.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid: got %d, want 400", w.Code)
	}
}

// Политика 'it_admin' + админ без MFA → мутирующая ручка 403 mfa_required, но allowlist
// (включение MFA, /me, /me/mfa) доступен.
func TestMFAPolicyGatesAdminWithoutMFA(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	resetMFAPolicyOnCleanup(t, db)
	adminTok := tokenForRole(t, rtr, db, "it_admin", "mfapolgate_")

	// Включаем политику через сам эндпоинт (политика ещё '' → админ не гейтится на этом PUT).
	put, _ := json.Marshal(map[string]string{"mfa_required_role": "it_admin"})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/settings/mfa-policy", put, adminTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT policy: %d %s", w.Code, w.Body)
	}
	if got := getMFAPolicyValue(t, w.Result()); got != "it_admin" {
		t.Fatalf("PUT echo = %q, want it_admin", got)
	}

	// Теперь мутирующее действие блокируется 403 с машиночитаемым сигналом.
	dev, _ := json.Marshal(map[string]string{"hostname": "gated-host", "os": "linux"})
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/devices", dev, adminTok)
	if w.Code != http.StatusForbidden {
		t.Fatalf("gated POST /devices: got %d, want 403", w.Code)
	}
	if w.Header().Get("X-MFA-Required") != "1" {
		t.Fatalf("missing X-MFA-Required header: %v", w.Header())
	}
	var e struct {
		Error string `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &e)
	if e.Error != "mfa_required" {
		t.Fatalf("error body = %q, want mfa_required", e.Error)
	}

	// Allowlist: включение MFA доступно (иначе локаут).
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/me/mfa/enroll", nil, adminTok); w.Code != http.StatusOK {
		t.Fatalf("enroll must stay accessible: %d %s", w.Code, w.Body)
	}
	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/me/mfa", nil, adminTok); w.Code != http.StatusOK {
		t.Fatalf("mfa status must stay accessible: %d %s", w.Code, w.Body)
	}

	// /me доступен и сигналит mfa_required=true.
	status, req := meMFARequired(t, rtr, adminTok)
	if status != http.StatusOK || req != "true" {
		t.Fatalf("/me: status=%d mfa_required=%q, want 200/true", status, req)
	}
}

// После enroll+confirm (totp_enabled=true) гейт открывается — ручки снова работают.
func TestMFAPolicyReleasedAfterEnroll(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	resetMFAPolicyOnCleanup(t, db)
	adminTok := tokenForRole(t, rtr, db, "it_admin", "mfapolrel_")

	if err := db.SetMFARequiredRole(context.Background(), "it_admin"); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// Мутирующая ручка заблокирована ДО включения MFA.
	dev, _ := json.Marshal(map[string]string{"hostname": "rel-host", "os": "linux"})
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/devices", dev, adminTok); w.Code != http.StatusForbidden {
		t.Fatalf("before enroll: got %d, want 403", w.Code)
	}

	// enroll+confirm проходят через allowlist даже при активном гейте.
	enrollAndConfirmMFA(t, rtr, adminTok)

	// Гейт открылся: мутирующая ручка работает.
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/devices", dev, adminTok); w.Code != http.StatusCreated {
		t.Fatalf("after enroll: got %d, want 201; %s", w.Code, w.Body)
	}
	// /me больше не сигналит mfa_required.
	if status, req := meMFARequired(t, rtr, adminTok); status != http.StatusOK || req != "" {
		t.Fatalf("/me after enroll: status=%d mfa_required=%q, want 200/''", status, req)
	}
}

// Политика выключена (пусто) → всё работает без MFA.
func TestMFAPolicyDisabledAllowsNoMFA(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	adminTok := tokenForRole(t, rtr, db, "it_admin", "mfapoloff_")

	dev, _ := json.Marshal(map[string]string{"hostname": "off-host", "os": "linux"})
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/devices", dev, adminTok); w.Code != http.StatusCreated {
		t.Fatalf("policy off POST /devices: got %d, want 201; %s", w.Code, w.Body)
	}
	if status, req := meMFARequired(t, rtr, adminTok); status != http.StatusOK || req != "" {
		t.Fatalf("/me policy off: status=%d mfa_required=%q, want 200/''", status, req)
	}
}

// Политика 'it_admin' не затрагивает viewer.
func TestMFAPolicyItAdminDoesNotAffectViewer(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	resetMFAPolicyOnCleanup(t, db)
	if err := db.SetMFARequiredRole(context.Background(), "it_admin"); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	viewerTok := tokenForRole(t, rtr, db, "viewer", "mfapolvw_")

	// viewer читает штатно и не гейтится.
	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/devices", nil, viewerTok); w.Code != http.StatusOK {
		t.Fatalf("viewer GET /devices under it_admin policy: got %d, want 200", w.Code)
	}
	if status, req := meMFARequired(t, rtr, viewerTok); status != http.StatusOK || req != "" {
		t.Fatalf("viewer /me: status=%d mfa_required=%q, want 200/''", status, req)
	}
}

// Политика 'all' гейтит и viewer'а (без MFA); allowlist включения MFA доступен.
func TestMFAPolicyAllGatesViewer(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	resetMFAPolicyOnCleanup(t, db)
	if err := db.SetMFARequiredRole(context.Background(), "all"); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	viewerTok := tokenForRole(t, rtr, db, "viewer", "mfapolall_")

	// Под 'all' даже read-роут закрыт (блокируем все authed-роуты кроме allowlist).
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/devices", nil, viewerTok)
	if w.Code != http.StatusForbidden || w.Header().Get("X-MFA-Required") != "1" {
		t.Fatalf("viewer GET under 'all': got %d hdr=%q, want 403/1", w.Code, w.Header().Get("X-MFA-Required"))
	}
	// Allowlist доступен → viewer может включить MFA.
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/me/mfa/enroll", nil, viewerTok); w.Code != http.StatusOK {
		t.Fatalf("viewer enroll under 'all': %d %s", w.Code, w.Body)
	}
	if status, req := meMFARequired(t, rtr, viewerTok); status != http.StatusOK || req != "true" {
		t.Fatalf("viewer /me under 'all': status=%d mfa_required=%q, want 200/true", status, req)
	}
}

// Смену политики может делать только it_admin.
func TestMFAPolicySetRequiresAdmin(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	viewerTok := tokenForRole(t, rtr, db, "viewer", "mfapolrole_")
	put, _ := json.Marshal(map[string]string{"mfa_required_role": "it_admin"})
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/settings/mfa-policy", put, viewerTok); w.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT policy: got %d, want 403", w.Code)
	}
}
