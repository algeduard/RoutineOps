package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Базовый шов SAML 2.0 SSO (без build-тега, компилируется в обеих сборках) — РЯДОМ с OIDC-швом
// (sso.go). Конкретная SAML-реализация — saml_enterprise.go (//go:build enterprise) на
// github.com/crewjam/saml, ставится в h.saml через WithSAML из enterpriseSetup. В open-core
// h.saml == nil → публичные /api/v1/auth/saml/* отдают 404/выкл, а SAML-либа В FREE-СБОРКУ НЕ
// ЛИНКУЕТСЯ (весь crewjam/saml-код под enterprise-тегом).
//
// Минт сессии — в ОДНОМ месте (issueToken), как у OIDC: провайдер валидирует SAML-ответ и
// возвращает SSOResult (тип общий с OIDC, sso.go), а базовый ACS-wrapper выдаёт JWT-cookie и
// редиректит. Ошибки — те же фиксированные ErrSSO*/ssoErrorCode из sso.go (общий ?sso_error=enum).

// SAMLProvider — контракт SAML Service Provider. Enabled(): сконфигурирован И лицензия покрывает
// FeatureSSO (SAML — вариант SSO, отдельной константы нет). Metadata: SP-метаданные для регистрации
// в IdP. Login: 302 (AuthnRequest, HTTP-Redirect binding) на IdP. ACS: принимает POST SAMLResponse,
// crewjam/saml валидирует подпись/условия/audience/recipient/InResponseTo и возвращает SSOResult
// ЛИБО одну из ErrSSO* (враппер маппит в фикс-enum).
type SAMLProvider interface {
	Enabled() bool
	Metadata(w http.ResponseWriter, r *http.Request)
	Login(w http.ResponseWriter, r *http.Request)
	ACS(w http.ResponseWriter, r *http.Request) (*SSOResult, error)
}

// WithSAML ставит SAML-провайдер (field-setter, как WithSSO/WithSCIM — игнорирует роутер;
// late-binding до первых запросов). Открытая сборка эту опцию не передаёт → h.saml nil.
func WithSAML(p SAMLProvider) RouterOption {
	return func(h *Handler, _ chi.Router) { h.saml = p }
}

// samlMetadata — публичный GET /api/v1/auth/saml/metadata: SP EntityDescriptor (XML) для
// регистрации Service Provider в IdP (EntityID, ACS URL). h.saml==nil/выкл → 404.
func (h *Handler) samlMetadata(w http.ResponseWriter, r *http.Request) {
	if h.saml == nil || !h.saml.Enabled() {
		http.NotFound(w, r)
		return
	}
	h.saml.Metadata(w, r)
}

// samlLogin — публичный GET /api/v1/auth/saml/login: делегирует провайдеру (302 AuthnRequest на IdP).
func (h *Handler) samlLogin(w http.ResponseWriter, r *http.Request) {
	if h.saml == nil || !h.saml.Enabled() {
		http.NotFound(w, r)
		return
	}
	h.saml.Login(w, r)
}

// samlACS — публичный POST /api/v1/auth/saml/acs (Assertion Consumer Service): провайдер валидирует
// SAMLResponse, wrapper минтит сессию и редиректит. Любая ошибка → 302 /login?sso_error=<enum>
// (fail-closed, не 500, не отражённый user-input) — тот же контракт, что у OIDC-callback.
func (h *Handler) samlACS(w http.ResponseWriter, r *http.Request) {
	if h.saml == nil || !h.saml.Enabled() {
		http.NotFound(w, r)
		return
	}
	res, err := h.saml.ACS(w, r)
	if err != nil {
		slog.Warn("saml acs failed", "err", err)
		http.Redirect(w, r, h.publicWebURL+"/login?sso_error="+ssoErrorCode(err), http.StatusFound)
		return
	}
	if err := h.issueToken(w, res.UserID, res.Email, res.Role); err != nil {
		slog.Error("saml acs: issue token failed", "err", err)
		http.Redirect(w, r, h.publicWebURL+"/login?sso_error=verify_failed", http.StatusFound)
		return
	}
	h.audit(r.Context(), res.UserID, res.Email, "sso_login", "user", res.UserID, map[string]any{"method": "saml"})
	// Сессия в httpOnly-куке (JS её не читает) — лендим на /login?sso=1, как OIDC-callback: там
	// страница пробивает /me, ставит клиентский маркер сессии и уходит в app.
	http.Redirect(w, r, h.publicWebURL+"/login?sso=1", http.StatusFound)
}

// samlStatus — публичный GET /api/v1/auth/saml/status: страница логина (неаутентифицирована,
// /capabilities ей недоступен) узнаёт, показывать ли кнопку «Войти через SAML». Отдельно от
// /auth/sso/status, чтобы OIDC и SAML можно было включать независимо.
func (h *Handler) samlStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.saml != nil && h.saml.Enabled()})
}
