package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// MFA enforce-by-policy — БАЗОВАЯ фича (не за лицензией), надстройка над добровольным TOTP
// (mfa.go). it_admin задаёт организационную политику «требовать 2FA» для роли, а гейт
// (requireMFAEnrollment) принуждает попавшего под неё юзера без MFA включить её, блокируя
// прочие authed-действия. Настройка живёт в org_settings (singleton, миграция 054).
//
// Значения политики: '' (выкл) | 'it_admin' | 'all'. Дефолт '' — поведение как до фичи.

// validMFAPolicyRole — допустимые значения политики. Пустая строка = выкл (валидна: так
// политику ОТКЛЮЧАЮТ).
func validMFAPolicyRole(role string) bool {
	return role == "" || role == "it_admin" || role == "all"
}

// mfaPolicyApplies — попадает ли юзер с ролью userRole под политику policyRole.
//
//	'all'      → под политику попадают все;
//	'it_admin' → только it_admin;
//	'' / иное  → политика выключена, не попадает никто.
func mfaPolicyApplies(policyRole, userRole string) bool {
	switch policyRole {
	case "all":
		return true
	case "it_admin":
		return userRole == "it_admin"
	default:
		return false
	}
}

// mfaGateAllowlisted — пути, которые гейт НЕ блокирует даже для юзера под политикой без MFA.
// Это АНТИ-ЛОКАУТ: без них юзер не смог бы выполнить само требование (включить MFA) и был бы
// заперт. Держим ровно то, что нужно фронту для энроллмента:
//   - GET  /me                — идентичность/роль (useMe), плюс сигнал mfa_required для баннера;
//   - GET  /me/mfa            — статус MFA (секция включения);
//   - POST /me/mfa/enroll     — генерация секрета;
//   - POST /me/mfa/confirm    — подтверждение кода и включение MFA.
//
// /auth/logout и статика в этот гейт не попадают вовсе (они смонтированы ВНЕ authed-группы
// /api/v1), поэтому выход и загрузка SPA всегда доступны.
func mfaGateAllowlisted(path string) bool {
	switch path {
	case "/api/v1/me",
		"/api/v1/me/mfa",
		"/api/v1/me/mfa/enroll",
		"/api/v1/me/mfa/confirm":
		return true
	}
	return false
}

// mfaEnrollmentRequired — обязан ли юзер (userID, userRole) включить MFA прямо сейчас: политика
// требует MFA для его роли, а totp_enabled=false. Общий источник истины для гейта и для сигнала
// mfa_required в /me.
func (h *Handler) mfaEnrollmentRequired(ctx context.Context, userID, userRole string) (bool, error) {
	policyRole, err := h.db.GetMFARequiredRole(ctx)
	if err != nil {
		return false, err
	}
	if !mfaPolicyApplies(policyRole, userRole) {
		return false, nil
	}
	enabled, _, _, _, err := h.db.GetUserMFA(ctx, userID)
	if err != nil {
		return false, err
	}
	return !enabled, nil
}

// requireMFAEnrollment — гейт принуждения MFA. Стоит в /api/v1 сразу за jwtMiddleware, поэтому
// claims в контексте уже есть. Для человека, попавшего под политику без включённой MFA,
// БЛОКИРУЕТ все authed-действия кроме allowlist'а (см. mfaGateAllowlisted), отдавая 403 с
// машиночитаемым сигналом (заголовок X-MFA-Required: 1 + тело {"error":"mfa_required"}), чтобы
// фронт увёл юзера на включение MFA. Fail-closed на ошибке БД (как соседние проверки миддлвара).
//
// Сервисные токены пропускаются: у автоматизации нет человека/второго фактора, а политика — про
// принуждение ЛЮДЕЙ; личные MFA-ручки для токенов и так закрыты requireHuman.
func (h *Handler) requireMFAEnrollment(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := r.Context().Value(claimsKey).(*jwtClaims)
		if claims.TokenID != "" {
			next.ServeHTTP(w, r)
			return
		}
		// Allowlist проверяем ДО БД: пути включения MFA не должны зависеть ни от политики, ни
		// от доступности запроса к настройкам (иначе сбой закрыл бы единственный выход).
		if mfaGateAllowlisted(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		required, err := h.mfaEnrollmentRequired(r.Context(), claims.UserID, claims.Role)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if required {
			w.Header().Set("X-MFA-Required", "1")
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "mfa_required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type mfaPolicyBody struct {
	MFARequiredRole string `json:"mfa_required_role"` // "" | "it_admin" | "all"
}

// getMFAPolicy — GET /settings/mfa-policy (it_admin): текущая политика принуждения MFA.
func (h *Handler) getMFAPolicy(w http.ResponseWriter, r *http.Request) {
	role, err := h.db.GetMFARequiredRole(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, mfaPolicyBody{MFARequiredRole: role})
}

// setMFAPolicy — PUT /settings/mfa-policy (it_admin, requireHuman): меняет политику принуждения
// MFA. requireHuman: это стойкая организационная настройка безопасности — менять её должен
// человек, а не сервисный токен (в духе гардов на выпускающих/повышающих права ручках). Смена
// аудируется (mfa_policy_changed).
func (h *Handler) setMFAPolicy(w http.ResponseWriter, r *http.Request) {
	var req mfaPolicyBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	role := strings.TrimSpace(req.MFARequiredRole)
	if !validMFAPolicyRole(role) {
		http.Error(w, "mfa_required_role must be '', 'it_admin', or 'all'", http.StatusBadRequest)
		return
	}
	if err := h.db.SetMFARequiredRole(r.Context(), role); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	h.audit(r.Context(), claims.UserID, claims.Email, "mfa_policy_changed", "org_settings", "",
		map[string]string{"mfa_required_role": role})
	writeJSON(w, http.StatusOK, mfaPolicyBody{MFARequiredRole: role})
}
