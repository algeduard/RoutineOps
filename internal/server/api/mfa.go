package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// TOTP MFA — БАЗОВАЯ фича (не за лицензией). Логин двухшаговый: шаг-1 (пароль) в auth.go
// при включённой MFA отдаёт challenge; шаг-2 (loginMFA здесь) проверяет TOTP/recovery и
// минтит сессию. Крипта секрета — storage/mfa_crypto.go; алгоритм TOTP — totp.go.

const (
	mfaChallengeTTL   = 5 * time.Minute
	recoveryCodeCount = 10
	recoveryCodeBytes = 15 // 120 бит энтропии — offline-перебор unsalted sha256 инфизибелен
)

// hashHex — sha256 строки в hex. В БД лежит только хеш challenge-токена и recovery-кода,
// поэтому дамп БД не даёт рабочего секрета.
func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newChallengeToken — 32 случайных байта в hex (plaintext challenge, отдаётся клиенту).
func newChallengeToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// normalizeMFACode — общая нормализация ввода TOTP и recovery: trim, убрать дефисы/пробелы,
// upper. TOTP после неё = 6 цифр; recovery = base32 без разделителей.
func normalizeMFACode(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return strings.ToUpper(s)
}

func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// generateRecoveryCodes → (display, hashes): display — коды с дефисами для показа ОДИН раз;
// hashes — sha256(нормализованный код) для хранения.
func generateRecoveryCodes() (display, hashes []string, err error) {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for i := 0; i < recoveryCodeCount; i++ {
		b := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		raw := enc.EncodeToString(b) // 24 символа, uppercase base32
		display = append(display, groupCode(raw))
		hashes = append(hashes, hashHex(raw)) // normalize(raw)==raw
	}
	return display, hashes, nil
}

// groupCode разбивает строку на группы по 4 через дефис (читаемость).
func groupCode(s string) string {
	var parts []string
	for i := 0; i < len(s); i += 4 {
		end := i + 4
		if end > len(s) {
			end = len(s)
		}
		parts = append(parts, s[i:end])
	}
	return strings.Join(parts, "-")
}

// isMFAKeyErr — ошибки доступности enc-ключа (не «неверный код»).
func isMFAKeyErr(err error) bool {
	return errors.Is(err, storage.ErrMFAKeyMissing) || errors.Is(err, storage.ErrMFAKeyInvalid)
}

// validateMFAFactor проверяет ВАЛИДНОСТЬ второго фактора для известного userID, НЕ расходуя
// его (не гасит recovery-код, не двигает last_step). Расход — отдельным шагом вызывающего
// уже ПОСЛЕ атомарного клейма challenge (чтобы проигравший гонку/просроченный запрос не сжёг
// одноразовый фактор). Возвращает:
//   - method: "totp" | "recovery";
//   - matchedCounter: для TOTP — совпавший counter (нужен, чтобы потом сдвинуть last_step);
//   - ok: фактор валиден;
//   - decryptFailed: ТОЛЬКО если прислан TOTP-код, но секрет не расшифровать (ключ потерян/
//     ротирован/повреждён) — вызывающий предлагает recovery, не считая это неверным кодом;
//   - err: транзиентная ошибка (БД/parse) — вызывающий отдаёт 500, НЕ советует recovery.
//
// Раздельные decryptFailed и err (а не всё в decryptFailed) не дают при сбое БД ложно
// советовать потратить одноразовый recovery-код.
func (h *Handler) validateMFAFactor(ctx context.Context, userID, rawCode string) (method string, matchedCounter int64, ok, decryptFailed bool, err error) {
	code := normalizeMFACode(rawCode)
	if isSixDigits(code) {
		uid, perr := uuid.Parse(userID)
		if perr != nil {
			return "totp", 0, false, false, perr
		}
		enabled, secretEnc, lastStep, _, gerr := h.db.GetUserMFA(ctx, userID)
		if gerr != nil {
			return "totp", 0, false, false, gerr
		}
		if !enabled || secretEnc == nil {
			// MFA отключили между шагами / нет секрета — обычный отказ фактора, НЕ decrypt-fail.
			return "totp", 0, false, false, nil
		}
		secret, derr := storage.DecryptTOTPSecret(uid, secretEnc)
		if derr != nil {
			slog.Error("mfa: decrypt totp secret failed", "user", userID, "err", derr)
			return "totp", 0, false, true, nil
		}
		matched, vok := VerifyTOTP(secret, code, time.Now())
		if !vok || matched <= lastStep {
			// Неверный код ИЛИ replay (counter не больше last_step).
			return "totp", 0, false, false, nil
		}
		return "totp", matched, true, false, nil
	}
	exists, cerr := h.db.RecoveryCodeExists(ctx, userID, hashHex(code))
	if cerr != nil {
		return "recovery", 0, false, false, cerr
	}
	return "recovery", 0, exists, false, nil
}

// loginMFA — ШАГ-2 логина (публичный, за httprate). Проверяет TOTP/recovery против
// challenge шага-1, при успехе минтит сессию.
func (h *Handler) loginMFA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.MFAToken == "" || req.Code == "" {
		http.Error(w, "mfa_token and code are required", http.StatusBadRequest)
		return
	}

	userID, ok, err := h.db.LookupMFAChallenge(r.Context(), hashHex(req.MFAToken))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "invalid or expired challenge", http.StatusUnauthorized)
		return
	}
	user, err := h.db.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Тот же per-account лимитер, что и на шаге-1 (общий acctKey).
	acctKey := strings.ToLower(user.Email)
	if locked, _ := h.loginLimiter.locked(acctKey, time.Now()); locked {
		http.Error(w, "too many failed login attempts, try again later", http.StatusTooManyRequests)
		return
	}

	// Сначала ВАЛИДИРУЕМ фактор БЕЗ расхода, затем атомарно клеймим challenge, и лишь потом
	// гасим фактор. Порядок важен: расход фактора после клейма → проигравший гонку за challenge
	// или просроченный запрос не сожжёт одноразовый recovery-код / не сдвинет last_step зря.
	method, matched, vok, decryptFailed, verr := h.validateMFAFactor(r.Context(), userID, req.Code)
	if verr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if decryptFailed {
		// Секрет не расшифровать (ключ потерян/ротирован) — challenge НЕ расходуем, юзер
		// уходит на recovery-код.
		http.Error(w, "MFA temporarily unavailable — use a recovery code", http.StatusUnauthorized)
		return
	}
	if !vok {
		h.loginLimiter.fail(acctKey, time.Now())
		h.audit(r.Context(), user.ID, user.Email, "mfa_login_failed", "user", user.ID, nil)
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}

	// Фактор валиден → атомарно клеймим challenge (одноразовость). Гонка/просрочка → 401,
	// фактор ещё НЕ израсходован.
	if _, cok, cerr := h.db.MarkMFAChallengeConsumed(r.Context(), hashHex(req.MFAToken)); cerr != nil || !cok {
		http.Error(w, "invalid or expired challenge", http.StatusUnauthorized)
		return
	}

	// Challenge наш — теперь атомарно гасим фактор. Провал здесь (конкурентный тот же код на
	// ДРУГОМ challenge) → 401; сожжён лишь дешёвый challenge, не фактор.
	if method == "totp" {
		advanced, aerr := h.db.AdvanceTOTPLastStep(r.Context(), userID, matched)
		if aerr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !advanced {
			http.Error(w, "invalid code", http.StatusUnauthorized)
			return
		}
	} else {
		consumed, cerr := h.db.ConsumeRecoveryCode(r.Context(), userID, hashHex(normalizeMFACode(req.Code)))
		if cerr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !consumed {
			http.Error(w, "invalid code", http.StatusUnauthorized)
			return
		}
	}

	h.loginLimiter.success(acctKey)
	if err := h.issueToken(w, user.ID, user.Email, user.Role); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.audit(r.Context(), user.ID, user.Email, "login", "user", user.ID, map[string]any{"mfa": true, "method": method})
	if method == "recovery" {
		remaining, _ := h.db.CountRecoveryCodes(r.Context(), userID)
		h.audit(r.Context(), user.ID, user.Email, "recovery_used", "user", user.ID, map[string]any{"remaining": remaining})
	}
	writeJSON(w, http.StatusOK, loginResponse{Status: "ok"})
}

// mfaStatus — GET /me/mfa: статус для текущего юзера.
func (h *Handler) mfaStatus(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	enabled, _, _, confirmedAt, err := h.db.GetUserMFA(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	remaining := 0
	if enabled {
		remaining, _ = h.db.CountRecoveryCodes(r.Context(), claims.UserID)
	}
	resp := map[string]any{"enabled": enabled, "recovery_codes_remaining": remaining}
	if confirmedAt != nil {
		resp["confirmed_at"] = confirmedAt
	}
	writeJSON(w, http.StatusOK, resp)
}

// enrollMFA — POST /me/mfa/enroll: генерит секрет, возвращает otpauth URI + base32. MFA
// пока НЕ включается (totp_enabled=false до confirm).
func (h *Handler) enrollMFA(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	enabled, _, _, _, err := h.db.GetUserMFA(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if enabled {
		http.Error(w, "MFA already enabled, disable it first", http.StatusConflict)
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	secret, err := generateTOTPSecret()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	blob, err := storage.EncryptTOTPSecret(uid, secret)
	if err != nil {
		if isMFAKeyErr(err) {
			http.Error(w, "MFA encryption key not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.db.SetUserTOTPPending(r.Context(), claims.UserID, blob); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	b32 := totpSecretBase32(secret)
	writeJSON(w, http.StatusOK, map[string]string{
		"otpauth_uri":   otpauthURI(claims.Email, b32),
		"secret_base32": b32,
	})
}

// confirmMFA — POST /me/mfa/confirm: подтверждает pending-секрет кодом, включает MFA и
// выдаёт recovery-коды ОДИН раз.
func (h *Handler) confirmMFA(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	enabled, secretEnc, _, _, err := h.db.GetUserMFA(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if enabled {
		http.Error(w, "MFA already enabled", http.StatusConflict)
		return
	}
	if secretEnc == nil {
		http.Error(w, "no pending enrollment; call enroll first", http.StatusBadRequest)
		return
	}
	secret, err := storage.DecryptTOTPSecret(uid, secretEnc)
	if err != nil {
		// Pending-секрет зашифрован ключом, которого больше нет — подтвердить нельзя.
		http.Error(w, "MFA encryption key not configured", http.StatusServiceUnavailable)
		return
	}
	matched, ok := VerifyTOTP(secret, normalizeMFACode(req.Code), time.Now())
	if !ok {
		h.audit(r.Context(), claims.UserID, claims.Email, "mfa_login_failed", "user", claims.UserID, nil)
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}
	display, hashes, err := generateRecoveryCodes()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.db.ConfirmUserTOTP(r.Context(), claims.UserID, matched, hashes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.audit(r.Context(), claims.UserID, claims.Email, "mfa_enabled", "user", claims.UserID, nil)
	writeJSON(w, http.StatusOK, map[string][]string{"recovery_codes": display})
}

// disableMFA — POST /me/mfa/disable: снимает MFA, требуя пароль + второй фактор (TOTP или
// recovery). Оба фактора не дают злоумышленнику с одной угнанной сессией отключить MFA.
func (h *Handler) disableMFA(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	user, err := h.db.GetUserByID(r.Context(), claims.UserID)
	if err != nil || user == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	enabled, _, _, _, err := h.db.GetUserMFA(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !enabled {
		http.Error(w, "MFA is not enabled", http.StatusBadRequest)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		http.Error(w, "current password is incorrect", http.StatusUnauthorized)
		return
	}
	_, _, vok, decryptFailed, verr := h.validateMFAFactor(r.Context(), claims.UserID, req.Code)
	if verr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if decryptFailed {
		http.Error(w, "MFA temporarily unavailable — use a recovery code", http.StatusUnauthorized)
		return
	}
	if !vok {
		h.audit(r.Context(), claims.UserID, claims.Email, "mfa_login_failed", "user", claims.UserID, nil)
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}
	if err := h.db.DisableUserTOTP(r.Context(), claims.UserID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.audit(r.Context(), claims.UserID, claims.Email, "mfa_disabled", "user", claims.UserID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// regenerateRecoveryCodes — POST /me/mfa/recovery-codes: перевыпуск набора recovery-кодов,
// требует пароль + второй фактор. Возвращает новый набор ОДИН раз.
func (h *Handler) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	user, err := h.db.GetUserByID(r.Context(), claims.UserID)
	if err != nil || user == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	enabled, _, _, _, err := h.db.GetUserMFA(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !enabled {
		http.Error(w, "MFA is not enabled", http.StatusBadRequest)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		http.Error(w, "current password is incorrect", http.StatusUnauthorized)
		return
	}
	_, _, vok, decryptFailed, verr := h.validateMFAFactor(r.Context(), claims.UserID, req.Code)
	if verr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if decryptFailed {
		http.Error(w, "MFA temporarily unavailable — use a recovery code", http.StatusUnauthorized)
		return
	}
	if !vok {
		h.audit(r.Context(), claims.UserID, claims.Email, "mfa_login_failed", "user", claims.UserID, nil)
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}
	display, hashes, err := generateRecoveryCodes()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.db.ReplaceRecoveryCodes(r.Context(), claims.UserID, hashes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.audit(r.Context(), claims.UserID, claims.Email, "recovery_regenerated", "user", claims.UserID, nil)
	writeJSON(w, http.StatusOK, map[string][]string{"recovery_codes": display})
}

// adminResetMFA — POST /users/{id}/mfa/reset: it_admin снимает MFA залоченному юзеру БЕЗ
// его факторов (главный аварийный выход). Не трогает пароль и не выдаёт сессию — юзер
// входит по паролю и заново включает MFA. Всё под аудитом.
func (h *Handler) adminResetMFA(w http.ResponseWriter, r *http.Request) {
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	targetID := chi.URLParam(r, "id")
	// Сброс СВОЕЙ MFA через admin-путь запрещён: иначе it_admin снял бы себе второй фактор в
	// обход пароля+кода, которые требует /me/mfa/disable (защита от угнанной сессии). Штатный
	// admin-reset — только над ЧУЖОЙ залоченной учёткой; свою MFA снимают через disable.
	if targetID == claims.UserID {
		http.Error(w, "use /me/mfa/disable to disable your own MFA (requires password and code)", http.StatusForbidden)
		return
	}
	target, err := h.db.GetUserByID(r.Context(), targetID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if err := h.db.DisableUserTOTP(r.Context(), targetID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.audit(r.Context(), claims.UserID, claims.Email, "mfa_admin_reset", "user", targetID,
		map[string]any{"target_user_id": targetID, "target_email": target.Email})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
