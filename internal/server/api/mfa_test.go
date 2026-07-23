package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// genTOTP — тест-only генерация 6-значного TOTP (копия RFC 6238, чтобы api_test не тянул
// unexported hotp из пакета api).
func genTOTP(secret []byte, now time.Time) string {
	counter := uint64(now.Unix() / 30)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off]&0x7f) << 24) | (uint32(sum[off+1]) << 16) | (uint32(sum[off+2]) << 8) | uint32(sum[off+3])
	return fmt.Sprintf("%06d", bin%1_000_000)
}

func mfaTestKey() string { return base64.StdEncoding.EncodeToString(make([]byte, 32)) }

func bearerFromLogin(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "token" && c.Value != "" {
			return "Bearer " + c.Value
		}
	}
	t.Fatalf("no token cookie in response: %d %s", w.Code, w.Body)
	return ""
}

func hasTokenCookie(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == "token" && c.Value != "" {
			return true
		}
	}
	return false
}

func doLoginMFA(t *testing.T, rtr http.Handler, mfaToken, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"mfa_token": mfaToken, "code": code})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/mfa", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)
	return w
}

// enrollAndConfirmMFA прогоняет enroll+confirm через API и возвращает секрет и recovery-коды.
func enrollAndConfirmMFA(t *testing.T, rtr http.Handler, token string) (secret []byte, recovery []string) {
	t.Helper()
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/me/mfa/enroll", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("enroll: %d %s", w.Code, w.Body)
	}
	var enr struct {
		SecretBase32 string `json:"secret_base32"`
		OtpauthURI   string `json:"otpauth_uri"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &enr); err != nil {
		t.Fatal(err)
	}
	sec, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enr.SecretBase32)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"code": genTOTP(sec, time.Now())})
	w = authedDo(t, rtr, http.MethodPost, "/api/v1/me/mfa/confirm", body, token)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	var conf struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conf); err != nil {
		t.Fatal(err)
	}
	if len(conf.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes = %d, want 10", len(conf.RecoveryCodes))
	}
	return sec, conf.RecoveryCodes
}

func TestMFAFullLoginFlow(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	email := "mfaflow_" + t.Name() + "@test.com"
	seedUser(t, db, email, "pass123", "it_admin")

	// Логин без MFA — один шаг, кука ставится.
	w := doLogin(t, rtr, email, "pass123")
	token := bearerFromLogin(t, w)

	secret, _ := enrollAndConfirmMFA(t, rtr, token)

	// Теперь шаг-1 не выдаёт сессию, а просит второй фактор.
	w = doLogin(t, rtr, email, "pass123")
	if w.Code != http.StatusOK {
		t.Fatalf("login step1: %d %s", w.Code, w.Body)
	}
	if hasTokenCookie(w) {
		t.Fatal("шаг-1 не должен ставить сессионную куку при включённой MFA")
	}
	var st struct {
		Status   string `json:"status"`
		MFAToken string `json:"mfa_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.Status != "mfa_required" || st.MFAToken == "" {
		t.Fatalf("step1 status=%q token empty=%v", st.Status, st.MFAToken == "")
	}

	// Шаг-2 с валидным TOTP из следующего окна → сессия. Следующее окно (now+30) нужно,
	// т.к. confirm выставил last_step на текущий counter (replay-guard): реальный логин
	// происходит позже enrollment, здесь моделируем это +30с (VerifyTOTP примет как +1 дрейф).
	w = doLoginMFA(t, rtr, st.MFAToken, genTOTP(secret, time.Now().Add(30*time.Second)))
	if w.Code != http.StatusOK || !hasTokenCookie(w) {
		t.Fatalf("step2: %d %s cookie=%v", w.Code, w.Body, hasTokenCookie(w))
	}
}

func TestMFAChallengeIsNotSession(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	email := "mfans_" + t.Name() + "@test.com"
	seedUser(t, db, email, "pass123", "it_admin")
	token := bearerFromLogin(t, doLogin(t, rtr, email, "pass123"))
	enrollAndConfirmMFA(t, rtr, token)

	w := doLogin(t, rtr, email, "pass123")
	var st struct {
		MFAToken string `json:"mfa_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &st)

	// challenge-токен в куке "token" не должен пройти jwtMiddleware.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: "token", Value: st.MFAToken})
	rec := httptest.NewRecorder()
	rtr.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("challenge-as-session: got %d, want 401", rec.Code)
	}
}

func TestMFAWrongCodePreservesChallengeThenRecovery(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	email := "mfarec_" + t.Name() + "@test.com"
	seedUser(t, db, email, "pass123", "it_admin")
	token := bearerFromLogin(t, doLogin(t, rtr, email, "pass123"))
	_, recovery := enrollAndConfirmMFA(t, rtr, token)

	w := doLogin(t, rtr, email, "pass123")
	var st struct {
		MFAToken string `json:"mfa_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &st)

	// Неверный код → 401, challenge НЕ израсходован.
	if w := doLoginMFA(t, rtr, st.MFAToken, "000000"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: got %d, want 401", w.Code)
	}
	// Тот же challenge + recovery-код → успех (challenge пережил неверную попытку).
	w = doLoginMFA(t, rtr, st.MFAToken, recovery[0])
	if w.Code != http.StatusOK || !hasTokenCookie(w) {
		t.Fatalf("recovery login: %d %s", w.Code, w.Body)
	}
	// Тот же recovery-код повторно (новый challenge) → отвергнут (одноразовость).
	w = doLogin(t, rtr, email, "pass123")
	json.Unmarshal(w.Body.Bytes(), &st)
	if w := doLoginMFA(t, rtr, st.MFAToken, recovery[0]); w.Code == http.StatusOK {
		t.Fatal("повторный recovery-код не должен проходить")
	}
}

func TestMFAChallengeSingleUse(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	email := "mfasu_" + t.Name() + "@test.com"
	seedUser(t, db, email, "pass123", "it_admin")
	token := bearerFromLogin(t, doLogin(t, rtr, email, "pass123"))
	secret, _ := enrollAndConfirmMFA(t, rtr, token)

	w := doLogin(t, rtr, email, "pass123")
	var st struct {
		MFAToken string `json:"mfa_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &st)

	if w := doLoginMFA(t, rtr, st.MFAToken, genTOTP(secret, time.Now().Add(30*time.Second))); w.Code != http.StatusOK {
		t.Fatalf("first step2: %d %s", w.Code, w.Body)
	}
	// Повтор того же challenge → 401 (LookupMFAChallenge не найдёт израсходованный —
	// отвергается ДО проверки кода, поэтому значение кода тут неважно).
	if w := doLoginMFA(t, rtr, st.MFAToken, genTOTP(secret, time.Now().Add(30*time.Second))); w.Code != http.StatusUnauthorized {
		t.Fatalf("challenge reuse: got %d, want 401", w.Code)
	}
}

func TestMFAKeyMissingDegradesToRecovery(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	email := "mfakey_" + t.Name() + "@test.com"
	seedUser(t, db, email, "pass123", "it_admin")
	token := bearerFromLogin(t, doLogin(t, rtr, email, "pass123"))
	secret, recovery := enrollAndConfirmMFA(t, rtr, token)

	// Ключ пропал (потеря/ротация).
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", "")

	w := doLogin(t, rtr, email, "pass123")
	if w.Code != http.StatusOK {
		t.Fatalf("login step1 без ключа должен работать (challenge крипты не требует): %d %s", w.Code, w.Body)
	}
	var st struct {
		MFAToken string `json:"mfa_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &st)

	// TOTP без ключа не проверить → 401 «use recovery», challenge не израсходован.
	if w := doLoginMFA(t, rtr, st.MFAToken, genTOTP(secret, time.Now())); w.Code != http.StatusUnauthorized {
		t.Fatalf("totp without key: got %d, want 401", w.Code)
	}
	// Recovery-код работает без ключа (sha256, крипты не требует).
	w = doLoginMFA(t, rtr, st.MFAToken, recovery[0])
	if w.Code != http.StatusOK || !hasTokenCookie(w) {
		t.Fatalf("recovery without key: %d %s", w.Code, w.Body)
	}
}

func TestMFAAdminReset(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)

	// Жертва B включает MFA.
	victimEmail := "mfavictim_" + t.Name() + "@test.com"
	seedUser(t, db, victimEmail, "pass123", "viewer")
	victimTok := bearerFromLogin(t, doLogin(t, rtr, victimEmail, "pass123"))
	enrollAndConfirmMFA(t, rtr, victimTok)
	victim, _ := db.GetUserByEmail(context.Background(), victimEmail)

	// Админ A сбрасывает MFA жертве.
	adminTok := tokenForRole(t, rtr, db, "it_admin", "mfaadmin_")
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/users/"+victim.ID+"/mfa/reset", nil, adminTok)
	if w.Code != http.StatusOK {
		t.Fatalf("admin reset: %d %s", w.Code, w.Body)
	}
	// Теперь жертва входит одним шагом (MFA снята).
	if w := doLogin(t, rtr, victimEmail, "pass123"); !hasTokenCookie(w) {
		t.Fatalf("after reset victim should log in one-step: %d %s", w.Code, w.Body)
	}

	// viewer не может делать admin-reset.
	viewerTok := tokenForRole(t, rtr, db, "viewer", "mfaviewer_")
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/users/"+victim.ID+"/mfa/reset", nil, viewerTok); w.Code != http.StatusForbidden {
		t.Fatalf("viewer admin-reset: got %d, want 403", w.Code)
	}
}

// Админ НЕ может сбросить СВОЮ MFA через admin-путь (обход пароля+кода из /me/mfa/disable).
func TestMFAAdminResetSelfForbidden(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", mfaTestKey())
	rtr, db := newRouterWithDB(t)
	email := "mfaself_" + t.Name() + "@test.com"
	seedUser(t, db, email, "pass123", "it_admin")
	tok := bearerFromLogin(t, doLogin(t, rtr, email, "pass123"))
	self, err := db.GetUserByEmail(context.Background(), email)
	if err != nil || self == nil {
		t.Fatalf("get self: %v", err)
	}
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/users/"+self.ID+"/mfa/reset", nil, tok); w.Code != http.StatusForbidden {
		t.Fatalf("self admin-reset: got %d, want 403", w.Code)
	}
}
