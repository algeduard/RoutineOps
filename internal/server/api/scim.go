package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Базовый шов SCIM 2.0 provisioning (без build-тега, компилируется в обеих сборках). Конкретная
// реализация — scim_enterprise.go (//go:build enterprise), ставится в h.scim через WithSCIM из
// enterpriseSetup. В open-core h.scim == nil → публичный /scim/v2/* отдаёт 404.
//
// Почему делегат, а не WithRoutes/WithAdminRoutes: SCIM-клиент (Okta/Azure AD) аутентифицируется
// СВОИМ bearer-токеном (Authorization: Bearer <scim_token>), НЕ админским JWT-cookie. Поэтому
// эндпоинты обязаны быть ПУБЛИЧНЫМИ (вне r.Route("/api/v1", jwtMiddleware)) — ровно как публичные
// /auth/sso/* сделаны через late-bound поле h.sso. Управление самим токеном (генерация/ротация)
// — отдельные АДМИН-ручки за JWT+it_admin (scim_handler.go, SCIMRoutes через WithAdminRoutes).

// SCIMProvider — реализация публичных SCIM 2.0 эндпоинтов (/scim/v2/*). Провайдер сам проверяет
// лицензию (FeatureSCIM → 402) и bearer-токен (→ 401), затем делает CRUD над users.
type SCIMProvider interface {
	http.Handler
}

// WithSCIM ставит SCIM-провайдер (field-setter, как WithSSO — игнорирует роутер; late-binding
// до первых запросов). Открытая сборка эту опцию не передаёт → h.scim nil → /scim/v2/* 404.
func WithSCIM(p SCIMProvider) RouterOption {
	return func(h *Handler, _ chi.Router) { h.scim = p }
}

// scimHandler — публичный /scim/v2/* делегат. h.scim == nil (open-core / фича не собрана) → 404,
// как ssoLogin при h.sso == nil. Иначе провайдер сам решает статусы (402/401/CRUD).
func (h *Handler) scimHandler(w http.ResponseWriter, r *http.Request) {
	if h.scim == nil {
		http.NotFound(w, r)
		return
	}
	h.scim.ServeHTTP(w, r)
}
