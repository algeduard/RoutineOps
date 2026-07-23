package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Базовый шов SSO/OIDC (без build-тега, компилируется в обеих сборках). Конкретная
// OIDC-реализация — sso_enterprise.go (//go:build enterprise), ставится в h.sso через
// WithSSO из enterpriseSetup. В open-core h.sso == nil → публичные роуты отдают 404/выкл.
//
// Минт сессии остаётся в ОДНОМ месте (issueToken): провайдер лишь валидирует OIDC и
// возвращает SSOResult, а базовый callback-wrapper выдаёт JWT-cookie и редиректит.

// SSOResult — итог успешной OIDC-аутентификации: на какого RoutineOps-юзера логинить.
type SSOResult struct {
	UserID string
	Email  string
	Role   string
}

// SSOProvider — контракт OIDC-провайдера. Enabled(): сконфигурирован И лицензия покрывает
// FeatureSSO. Login: 302 на IdP (PKCE+state+nonce). Callback: валидирует ответ IdP и
// возвращает SSOResult ЛИБО одну из sso-ошибок ниже (враппер маппит её в фикс-enum).
type SSOProvider interface {
	Enabled() bool
	Login(w http.ResponseWriter, r *http.Request)
	Callback(w http.ResponseWriter, r *http.Request) (*SSOResult, error)
}

// Фиксированные sso-ошибки → фикс-enum в ?sso_error=. Ни один user-input в URL не попадает.
var (
	ErrSSOEmailConflict   = errors.New("email_conflict")
	ErrSSOEmailUnverified = errors.New("email_unverified")
	ErrSSONoAccount       = errors.New("no_account")
	ErrSSOIdPUnavailable  = errors.New("idp_unavailable")
	ErrSSOVerifyFailed    = errors.New("verify_failed")
)

func ssoErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrSSOEmailConflict):
		return "email_conflict"
	case errors.Is(err, ErrSSOEmailUnverified):
		return "email_unverified"
	case errors.Is(err, ErrSSONoAccount):
		return "no_account"
	case errors.Is(err, ErrSSOIdPUnavailable):
		return "idp_unavailable"
	default:
		// Любая иная (в т.ч. внутренняя) ошибка → generic, чтобы не течь деталями.
		return "verify_failed"
	}
}

// WithSSO ставит OIDC-провайдер (field-setter, как WithLockModePolicy — игнорирует роутер;
// late-binding до первых запросов). Открытая сборка эту опцию не передаёт → h.sso nil.
func WithSSO(p SSOProvider) RouterOption {
	return func(h *Handler, _ chi.Router) { h.sso = p }
}

// ssoLogin — публичный GET /api/v1/auth/sso/login: делегирует провайдеру (302 на IdP).
func (h *Handler) ssoLogin(w http.ResponseWriter, r *http.Request) {
	if h.sso == nil || !h.sso.Enabled() {
		http.NotFound(w, r)
		return
	}
	h.sso.Login(w, r)
}

// ssoCallback — публичный GET /api/v1/auth/sso/callback: провайдер валидирует OIDC-ответ,
// wrapper минтит сессию и редиректит. Любая ошибка → 302 /login?sso_error=<enum> (fail-closed,
// не 500, не отражённый user-input).
func (h *Handler) ssoCallback(w http.ResponseWriter, r *http.Request) {
	if h.sso == nil || !h.sso.Enabled() {
		http.NotFound(w, r)
		return
	}
	res, err := h.sso.Callback(w, r)
	if err != nil {
		slog.Warn("sso callback failed", "err", err)
		http.Redirect(w, r, h.publicWebURL+"/login?sso_error="+ssoErrorCode(err), http.StatusFound)
		return
	}
	if err := h.issueToken(w, res.UserID, res.Email, res.Role); err != nil {
		slog.Error("sso callback: issue token failed", "err", err)
		http.Redirect(w, r, h.publicWebURL+"/login?sso_error=verify_failed", http.StatusFound)
		return
	}
	h.audit(r.Context(), res.UserID, res.Email, "sso_login", "user", res.UserID, map[string]any{"method": "oidc"})
	// Сессия в httpOnly-куке. Веб не знает о ней (кука не читается JS), поэтому лендим на
	// /login?sso=1 — там страница пробит /me, ставит клиентский маркер сессии и уходит в app.
	http.Redirect(w, r, h.publicWebURL+"/login?sso=1", http.StatusFound)
}

// ssoStatus — публичный GET /api/v1/auth/sso/status: страница логина (неаутентифицирована,
// /capabilities ей недоступен) узнаёт, показывать ли кнопку SSO.
func (h *Handler) ssoStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.sso != nil && h.sso.Enabled()})
}
