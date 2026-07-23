//go:build enterprise

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Конкретная OIDC-реализация SSOProvider (enterprise). go-oidc делает discovery, кэш+ротацию
// JWKS и верификацию подписи/iss/aud/exp ID-токена. nonce go-oidc НЕ проверяет — сверяем сами.
// Discovery ленивый (первый запрос, не старт): недоступный на буте IdP не роняет сервер.

// ssoFlowCookieTTL — время жизни transient flow-куки; согласовано с TTL строки sso_auth_flows
// в storage (10 мин).
const ssoFlowCookieTTL = 10 * time.Minute

type ssoConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       []string
	RoleClaim    string          // пусто = роль SSO не пересчитывает
	AdminValues  map[string]bool // значения role-claim, дающие it_admin (allowlist)
	DefaultRole  string          // роль JIT-юзеров (валидирована против validRoles)
	AllowJIT     bool
}

type oidcProvider struct {
	db           *storage.DB
	mgr          *license.Manager
	publicWebURL string
	cookieSecure bool
	cfg          ssoConfig

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// NewOIDCProvider читает SSO_* env и возвращает провайдер (без сетевых обращений — discovery
// ленивый). Не сконфигурирован (нет SSO_ISSUER/CLIENT_ID/SECRET) → Enabled()=false.
func NewOIDCProvider(db *storage.DB, mgr *license.Manager, publicWebURL string, cookieSecure bool) SSOProvider {
	scopes := strings.Fields(strings.TrimSpace(os.Getenv("SSO_SCOPES")))
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	} else if !containsStr(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...) // openid обязателен для OIDC
	}
	admin := map[string]bool{}
	for _, v := range strings.Split(os.Getenv("SSO_ADMIN_VALUES"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			admin[v] = true
		}
	}
	defRole := strings.TrimSpace(os.Getenv("SSO_DEFAULT_ROLE"))
	if defRole == "it_admin" {
		// Инвариант «it_admin по умолчанию НИКОГДА»: дефолт применяется к юзерам, НЕ
		// доказавшим admin-группу, поэтому не может быть it_admin. it_admin достижим только
		// явным совпадением в SSO_ADMIN_VALUES (roleFromClaim). Клампим в viewer.
		slog.Warn("SSO_DEFAULT_ROLE=it_admin запрещён (it_admin только через SSO_ADMIN_VALUES) — использую viewer")
		defRole = "viewer"
	} else if !validRoles[defRole] {
		defRole = "viewer" // least privilege по умолчанию
	}
	allowJIT := true
	if v := strings.TrimSpace(os.Getenv("SSO_ALLOW_JIT")); v != "" {
		allowJIT = v == "true"
	}
	return &oidcProvider{
		db: db, mgr: mgr, publicWebURL: publicWebURL, cookieSecure: cookieSecure,
		cfg: ssoConfig{
			Issuer:       strings.TrimSpace(os.Getenv("SSO_ISSUER")),
			ClientID:     strings.TrimSpace(os.Getenv("SSO_CLIENT_ID")),
			ClientSecret: os.Getenv("SSO_CLIENT_SECRET"),
			Scopes:       scopes,
			RoleClaim:    strings.TrimSpace(os.Getenv("SSO_ROLE_CLAIM")),
			AdminValues:  admin,
			DefaultRole:  defRole,
			AllowJIT:     allowJIT,
		},
	}
}

func (p *oidcProvider) Enabled() bool {
	return p.cfg.Issuer != "" && p.cfg.ClientID != "" && p.cfg.ClientSecret != "" &&
		p.mgr.Has(license.FeatureSSO)
}

// ensure лениво инициализирует discovery/verifier/oauth. При ошибке (IdP недоступен) НЕ
// кэширует — повторит на следующем запросе (recover, когда IdP поднимется).
func (p *oidcProvider) ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provider != nil {
		return nil
	}
	prov, err := oidc.NewProvider(ctx, p.cfg.Issuer)
	if err != nil {
		return err
	}
	p.provider = prov
	p.verifier = prov.Verifier(&oidc.Config{ClientID: p.cfg.ClientID})
	p.oauth = &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: p.cfg.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  p.publicWebURL + "/api/v1/auth/sso/callback", // фиксирован server-side
		Scopes:       p.cfg.Scopes,
	}
	return nil
}

// flowCookieName — __Host-префикс (Secure, Path=/, host-only) в проде; в dev (http, cookieSecure
// =false) браузер __Host- отверг бы, поэтому имя без префикса.
func (p *oidcProvider) flowCookieName() string {
	if p.cookieSecure {
		return "__Host-sso_flow"
	}
	return "sso_flow"
}

func (p *oidcProvider) redirectErr(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, p.publicWebURL+"/login?sso_error="+code, http.StatusFound)
}

func (p *oidcProvider) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := p.ensure(ctx); err != nil {
		slog.Warn("sso: discovery failed", "err", err)
		p.redirectErr(w, r, "idp_unavailable")
		return
	}
	state := randToken()
	nonce := randToken()
	verifier := oauth2.GenerateVerifier() // PKCE code_verifier
	if err := p.db.InsertSSOFlow(ctx, state, nonce, verifier); err != nil {
		slog.Error("sso: insert flow", "err", err)
		p.redirectErr(w, r, "verify_failed")
		return
	}
	// Transient-кука привязывает state к браузеру (CSRF). SameSite=Lax: callback — top-level
	// cross-site редирект от IdP, при Strict кука не отправилась бы.
	http.SetCookie(w, &http.Cookie{
		Name:     p.flowCookieName(),
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   p.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ssoFlowCookieTTL.Seconds()),
	})
	authURL := p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (p *oidcProvider) Callback(w http.ResponseWriter, r *http.Request) (*SSOResult, error) {
	ctx := r.Context()
	// Гасим flow-куку при любом исходе.
	defer http.SetCookie(w, &http.Cookie{
		Name: p.flowCookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: p.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	if err := p.ensure(ctx); err != nil {
		return nil, ErrSSOIdPUnavailable
	}

	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")
	if state == "" || code == "" {
		return nil, ErrSSOVerifyFailed
	}
	// CSRF: state из query обязан совпасть со state из flow-куки.
	ck, err := r.Cookie(p.flowCookieName())
	if err != nil || ck.Value == "" || ck.Value != state {
		return nil, ErrSSOVerifyFailed
	}
	// Single-use: забираем строку флоу (SELECT+DELETE) → nonce, pkce_verifier. Replay/гонка → !ok.
	nonce, verifier, ok, err := p.db.ConsumeSSOFlow(ctx, state)
	if err != nil || !ok {
		return nil, ErrSSOVerifyFailed
	}
	// Обмен кода на токены (PKCE code_verifier из строки флоу).
	tok, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, ErrSSOVerifyFailed
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return nil, ErrSSOVerifyFailed
	}
	// Верификация подписи (JWKS с ротацией) + iss + aud + exp.
	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, ErrSSOVerifyFailed
	}
	// ЯВНАЯ сверка nonce (go-oidc её не делает) — anti-replay.
	if idToken.Nonce == "" || idToken.Nonce != nonce {
		return nil, ErrSSOVerifyFailed
	}
	// iss точным равенством с конфигом (anti mix-up; verifier сверяет с discovery-issuer,
	// discovery шёл с cfg.Issuer — но фиксируем ещё раз явно).
	if idToken.Issuer != p.cfg.Issuer {
		return nil, ErrSSOVerifyFailed
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, ErrSSOVerifyFailed
	}
	// email_verified — СТРОГО bool, fail-closed (отсутствует/не true/строка → отказ).
	if ev, ok := claims["email_verified"].(bool); !ok || !ev {
		return nil, ErrSSOEmailUnverified
	}
	email, _ := claims["email"].(string)
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, ErrSSOVerifyFailed
	}
	name, _ := claims["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = email
	}

	issuer, subject := idToken.Issuer, idToken.Subject
	if subject == "" {
		return nil, ErrSSOVerifyFailed
	}

	// (а) матч по неизменяемой (issuer, subject).
	if existing, err := p.db.GetUserByOIDCIdentity(ctx, issuer, subject); err != nil {
		return nil, ErrSSOVerifyFailed
	} else if existing != nil {
		role := p.resolveRole(ctx, existing, claims)
		return &SSOResult{UserID: existing.ID, Email: existing.Email, Role: role}, nil
	}

	// (б) коллизия email с ЛЮБЫМ существующим аккаунтом → отказ авто-линка (закрывает
	// эскалацию через seed-admin email и takeover). Case-insensitive.
	if collide, err := p.db.GetUserByEmailCI(ctx, email); err != nil {
		return nil, ErrSSOVerifyFailed
	} else if collide != nil {
		return nil, ErrSSOEmailConflict
	}

	// (в) JIT-провижининг.
	if !p.cfg.AllowJIT {
		return nil, ErrSSONoAccount
	}
	created, err := p.db.CreateSSOUser(ctx, name, email, p.initialRole(claims), issuer, subject)
	if err != nil {
		if errors.Is(err, storage.ErrSSOEmailTaken) {
			// Гонка: email заняли параллельным созданием между проверкой и INSERT → коллизия.
			return nil, ErrSSOEmailConflict
		}
		return nil, ErrSSOVerifyFailed
	}
	if created == nil { // defense-in-depth: не разыменовываем nil
		return nil, ErrSSOVerifyFailed
	}
	return &SSOResult{UserID: created.ID, Email: created.Email, Role: created.Role}, nil
}

// resolveRole пересчитывает роль существующего SSO-юзера из claim. Fail-closed: claim задан,
// но в токене отсутствует/пуст → НЕ понижаем (оставляем текущую), пишем warn.
func (p *oidcProvider) resolveRole(ctx context.Context, u *storage.User, claims map[string]any) string {
	if p.cfg.RoleClaim == "" {
		return u.Role // SSO ролью не управляет
	}
	newRole, present := p.roleFromClaim(claims)
	if !present {
		slog.Warn("sso: role claim configured but absent in token; keeping current role", "user", u.ID)
		return u.Role
	}
	if newRole != u.Role {
		if err := p.db.UpdateUserRole(ctx, u.ID, newRole); err != nil {
			slog.Error("sso: update role failed", "user", u.ID, "err", err)
			return u.Role
		}
	}
	return newRole
}

// initialRole — роль JIT-юзера: из claim, иначе SSO_DEFAULT_ROLE.
func (p *oidcProvider) initialRole(claims map[string]any) string {
	if p.cfg.RoleClaim == "" {
		return p.cfg.DefaultRole
	}
	if role, present := p.roleFromClaim(claims); present {
		return role
	}
	return p.cfg.DefaultRole
}

// roleFromClaim: значение(я) role-claim ∈ AdminValues → it_admin, иначе viewer. present=false,
// если claim отсутствует/пуст (для fail-closed логики выше). it_admin по умолчанию НИКОГДА.
func (p *oidcProvider) roleFromClaim(claims map[string]any) (role string, present bool) {
	v, ok := claims[p.cfg.RoleClaim]
	if !ok {
		return "", false
	}
	values := claimStrings(v)
	if len(values) == 0 {
		return "", false
	}
	for _, val := range values {
		if p.cfg.AdminValues[val] {
			return "it_admin", true
		}
	}
	return "viewer", true
}

// claimStrings приводит значение claim (string | []any | []string) к []string.
func claimStrings(v any) []string {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{t}
	case []string:
		return t
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
