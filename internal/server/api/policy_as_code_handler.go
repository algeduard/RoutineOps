//go:build enterprise

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// PolicyAsCodeRoutes монтирует /policy-as-code/* — декларативные ГЛОБАЛЬНЫЕ software-политики
// (GitOps) + детект дрейфа, enterprise-фича. Админ хранит желаемый набор правил как JSON
// source-of-truth, apply реконсилит живые software_policy_rules (создаёт недостающие, удаляет
// лишние — ТОЛЬКО глобальные, device/group-scoped не трогаются), drift показывает расхождение
// без применения. Гейт по лицензии (mgr.Has → 402) в каждом хендлере. Ставится через
// WithAdminRoutes (it_admin): apply МАССОВО мутирует политики. В open-core роутов нет (404).
//
// GitOps-подход: декларация коммитится в git на стороне деплойера и заливается сюда POST-ом
// (можно из CI) — сервер приводит живое состояние к декларации и фиксирует историю применений
// в policy_declaration (applied_by/applied_at + счётчики created/deleted) для аудита.
func PolicyAsCodeRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		// GET /policy-as-code — текущая сохранённая декларация + вычисленный дрейф.
		r.Get("/policy-as-code", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeaturePolicyAsCode) {
				http.Error(w, "policy-as-code requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			decl, err := h.db.GetPolicyDeclaration(req.Context())
			if err != nil {
				slog.Error("get policy declaration", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			drift, err := h.db.PolicyDriftAgainstSaved(req.Context())
			if err != nil {
				slog.Error("policy drift", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"declaration": decl, // null, если ещё ни разу не применяли
				"drift":       drift,
			})
		})

		// GET /policy-as-code/drift — детект дрейфа БЕЗ применения.
		r.Get("/policy-as-code/drift", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeaturePolicyAsCode) {
				http.Error(w, "policy-as-code requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			drift, err := h.db.PolicyDriftAgainstSaved(req.Context())
			if err != nil {
				slog.Error("policy drift", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, drift)
		})

		// POST /policy-as-code/apply — принять декларацию, провалидировать, сохранить как
		// source-of-truth и реконсилить живые глобальные правила (в транзакции).
		r.Post("/policy-as-code/apply", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeaturePolicyAsCode) {
				http.Error(w, "policy-as-code requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			var body struct {
				Rules        []storage.DesiredPolicyRule `json:"rules"`
				ConfirmEmpty bool                        `json:"confirm_empty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}

			// Защита от случайного сноса всех правил: пустая декларация реконсилит парк в ноль
			// (удалит ВСЕ глобальные правила), поэтому требует явного намерения confirm_empty.
			if len(body.Rules) == 0 && !body.ConfirmEmpty {
				http.Error(w, "empty declaration would delete all global rules; set confirm_empty to proceed", http.StatusBadRequest)
				return
			}

			// Валидация/нормализация КАЖДОГО правила — те же проверки, что у createPolicy:
			// sanitizeSoftwareName, rule_type allowed|forbidden, валидные платформы (дедуп,
			// все три → без фильтра). Ошибка в любом правиле отвергает всю декларацию.
			clean := make([]storage.DesiredPolicyRule, 0, len(body.Rules))
			for i, raw := range body.Rules {
				name, ok := sanitizeSoftwareName(raw.SoftwareName)
				if !ok {
					http.Error(w, "rule "+strconv.Itoa(i)+": software_name is empty or contains invalid characters", http.StatusBadRequest)
					return
				}
				if raw.RuleType != "allowed" && raw.RuleType != "forbidden" {
					http.Error(w, "rule "+strconv.Itoa(i)+": rule_type must be 'allowed' or 'forbidden'", http.StatusBadRequest)
					return
				}
				platforms, perr := normalizeDesiredPlatforms(raw.Platforms)
				if perr != "" {
					http.Error(w, "rule "+strconv.Itoa(i)+": "+perr, http.StatusBadRequest)
					return
				}
				clean = append(clean, storage.DesiredPolicyRule{SoftwareName: name, RuleType: raw.RuleType, Platforms: platforms})
			}

			userID, email, _ := Actor(req.Context())
			created, deleted, err := h.db.ApplyPolicyDeclaration(req.Context(), clean, email)
			if err != nil {
				slog.Error("apply policy declaration", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.audit(req.Context(), userID, email, "policy_declaration_applied", "policy_declaration", "",
				map[string]int{"rules": len(clean), "created": created, "deleted": deleted})
			writeJSON(w, http.StatusOK, map[string]int{"rules": len(clean), "created": created, "deleted": deleted})
		})
	}
}

// normalizeDesiredPlatforms валидирует и нормализует платформы одного правила декларации —
// зеркалит логику createPolicy: значения из {macOS,Windows,Linux}, дедуп, все три = без
// фильтра (nil). Возвращает (платформы, ""); при ошибке — (nil, сообщение).
func normalizeDesiredPlatforms(in []string) ([]string, string) {
	validPlatform := map[string]bool{"macOS": true, "Windows": true, "Linux": true}
	seen := map[string]bool{}
	var platforms []string
	for _, p := range in {
		if !validPlatform[p] {
			return nil, "invalid platform: " + p
		}
		if !seen[p] {
			seen[p] = true
			platforms = append(platforms, p)
		}
	}
	if len(platforms) >= 3 {
		platforms = nil // все три уникальные платформы = без фильтра
	}
	return platforms, ""
}
