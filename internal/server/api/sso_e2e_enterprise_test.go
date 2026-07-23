//go:build enterprise

package api_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Floodww/RoutineOps/internal/server/api"
)

// mockIdP — минимальный OIDC IdP на httptest: discovery + JWKS + token. Возвращает id_token,
// который выставит текущий тест (через *current).
type mockIdP struct {
	srv     *httptest.Server
	issuer  string
	priv    *rsa.PrivateKey
	current *string // id_token, который вернёт /token
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())
	jwks := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"test","alg":"RS256","use":"sig","n":%q,"e":%q}]}`, n, e)

	m := &mockIdP{priv: priv, current: new(string)}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
			m.issuer, m.issuer+"/authorize", m.issuer+"/token", m.issuer+"/jwks")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, jwks)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at","token_type":"Bearer","id_token":%q}`, *m.current)
	})
	m.srv = httptest.NewServer(mux)
	m.issuer = m.srv.URL
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockIdP) idToken(t *testing.T, sub, email, nonce string, emailVerified bool, groups []string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":            m.issuer,
		"aud":            "test-client",
		"sub":            sub,
		"exp":            time.Now().Add(time.Hour).Unix(),
		"iat":            time.Now().Unix(),
		"nonce":          nonce,
		"email":          email,
		"email_verified": emailVerified,
		"name":           "Test User",
	}
	if groups != nil {
		claims["groups"] = groups
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test"
	s, err := tok.SignedString(m.priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// beginFlow гоняет provider.Login и возвращает (state, nonce, flow-куку).
func beginFlow(t *testing.T, p api.SSOProvider) (string, string, *http.Cookie) {
	t.Helper()
	lr := httptest.NewRequest(http.MethodGet, "https://app.local/api/v1/auth/sso/login", nil)
	lw := httptest.NewRecorder()
	p.Login(lw, lr)
	if lw.Code != http.StatusFound {
		t.Fatalf("login: got %d, want 302; body %s", lw.Code, lw.Body)
	}
	u, err := url.Parse(lw.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state, nonce := u.Query().Get("state"), u.Query().Get("nonce")
	if state == "" || nonce == "" {
		t.Fatalf("login authz url missing state/nonce: %s", lw.Header().Get("Location"))
	}
	var flow *http.Cookie
	for _, c := range lw.Result().Cookies() {
		if strings.Contains(c.Name, "sso_flow") {
			flow = c
		}
	}
	if flow == nil {
		t.Fatal("login did not set flow cookie")
	}
	return state, nonce, flow
}

func doCallback(t *testing.T, p api.SSOProvider, state string, flow *http.Cookie) (*api.SSOResult, error) {
	t.Helper()
	cr := httptest.NewRequest(http.MethodGet, "https://app.local/api/v1/auth/sso/callback?code=authcode&state="+state, nil)
	cr.AddCookie(flow)
	cw := httptest.NewRecorder()
	return p.Callback(cw, cr)
}

func TestSSOCallbackEndToEnd(t *testing.T) {
	idp := newMockIdP(t)
	t.Setenv("SSO_ISSUER", idp.issuer)
	t.Setenv("SSO_CLIENT_ID", "test-client")
	t.Setenv("SSO_CLIENT_SECRET", "secret")
	t.Setenv("SSO_ROLE_CLAIM", "groups")
	t.Setenv("SSO_ADMIN_VALUES", "routineops-admins")

	db := newTestDB(t)
	ctx := context.Background()
	// mgr не нужен для прямого вызова Callback (Enabled() его не касается).
	p := api.NewOIDCProvider(db, nil, "https://app.local", false)

	// (1) Happy path: JIT-провижининг, роль it_admin из groups-claim.
	state, nonce, flow := beginFlow(t, p)
	*idp.current = idp.idToken(t, "subject-1", "alice@example.com", nonce, true, []string{"routineops-admins"})
	res, err := doCallback(t, p, state, flow)
	if err != nil || res == nil {
		t.Fatalf("happy callback: err=%v res=%v", err, res)
	}
	if res.Email != "alice@example.com" || res.Role != "it_admin" {
		t.Fatalf("happy result: email=%s role=%s", res.Email, res.Role)
	}
	u, _ := db.GetUserByOIDCIdentity(ctx, idp.issuer, "subject-1")
	if u == nil || u.AuthSource != "oidc" {
		t.Fatalf("provisioned user: %+v", u)
	}

	// (2) Replay: тот же state/flow уже израсходован → отказ.
	*idp.current = idp.idToken(t, "subject-1", "alice@example.com", nonce, true, nil)
	if _, err := doCallback(t, p, state, flow); err == nil {
		t.Fatal("replay того же challenge должен отвергаться")
	}

	// (3) Nonce mismatch: свежий flow, но id_token с ЧУЖИМ nonce → отказ (явная сверка nonce).
	state2, _, flow2 := beginFlow(t, p)
	*idp.current = idp.idToken(t, "subject-2", "bob@example.com", "WRONG-NONCE", true, nil)
	if _, err := doCallback(t, p, state2, flow2); err == nil {
		t.Fatal("id_token с неверным nonce должен отвергаться")
	}

	// (4) email_verified=false → отказ (fail-closed).
	state3, nonce3, flow3 := beginFlow(t, p)
	*idp.current = idp.idToken(t, "subject-3", "carol@example.com", nonce3, false, nil)
	if _, err := doCallback(t, p, state3, flow3); err == nil {
		t.Fatal("email_verified=false должен отвергаться")
	}

	// (5) email_conflict: email совпадает с существующим ЛОКАЛЬНЫМ юзером → отказ авто-линка.
	seedUser(t, db, "local-collision@example.com", "pass123", "it_admin")
	state4, nonce4, flow4 := beginFlow(t, p)
	*idp.current = idp.idToken(t, "subject-4", "local-collision@example.com", nonce4, true, nil)
	if _, err := doCallback(t, p, state4, flow4); err == nil {
		t.Fatal("коллизия email с локальным аккаунтом должна отвергаться")
	}

	// (6) Повторный вход существующего SSO-юзера (subject-1): матч по (iss,sub), не дубль;
	// роль пересчитывается — теперь без admin-группы → viewer (downgrade).
	state5, nonce5, flow5 := beginFlow(t, p)
	*idp.current = idp.idToken(t, "subject-1", "alice@example.com", nonce5, true, []string{"some-other-group"})
	res6, err := doCallback(t, p, state5, flow5)
	if err != nil || res6 == nil {
		t.Fatalf("re-login: err=%v", err)
	}
	if res6.UserID != u.ID {
		t.Fatalf("re-login создал дубль: %s != %s", res6.UserID, u.ID)
	}
	if res6.Role != "viewer" {
		t.Fatalf("роль должна пересчитаться в viewer (нет admin-группы), got %s", res6.Role)
	}
}

// SSO_DEFAULT_ROLE=it_admin запрещён: JIT-юзер без admin-claim получает viewer (кламп),
// а не it_admin (инвариант «it_admin по умолчанию НИКОГДА»).
func TestSSODefaultRoleClampsAdmin(t *testing.T) {
	idp := newMockIdP(t)
	t.Setenv("SSO_ISSUER", idp.issuer)
	t.Setenv("SSO_CLIENT_ID", "test-client")
	t.Setenv("SSO_CLIENT_SECRET", "secret")
	t.Setenv("SSO_DEFAULT_ROLE", "it_admin") // должно клампиться в viewer
	// SSO_ROLE_CLAIM не задан → initialRole возвращает DefaultRole.

	db := newTestDB(t)
	p := api.NewOIDCProvider(db, nil, "https://app.local", false)
	state, nonce, flow := beginFlow(t, p)
	*idp.current = idp.idToken(t, "sub-clamp", "clamp@example.com", nonce, true, nil)
	res, err := doCallback(t, p, state, flow)
	if err != nil || res == nil {
		t.Fatalf("callback: err=%v", err)
	}
	if res.Role != "viewer" {
		t.Fatalf("SSO_DEFAULT_ROLE=it_admin должен клампиться в viewer, got %s", res.Role)
	}
}
