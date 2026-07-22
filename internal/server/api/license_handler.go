//go:build enterprise

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/go-chi/chi/v5"
)

// LicenseRoutes монтирует /license: GET — статус энтайтлмента, POST — применить/
// деактивировать лицензию. Ставится enterprise-оверлеем через WithAdminRoutes (гейт
// it_admin). В open-core роута нет вовсе → 404 (см. license_absent_test.go): по этому
// коду UI отличает «недоступно в этой редакции» от настоящего сбоя. Применение мгновенно,
// без рестарта.
func LicenseRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/license", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, mgr.Status())
		})

		r.Post("/license", func(w http.ResponseWriter, req *http.Request) {
			var body struct {
				License            string `json:"license"`
				ActivationPassword string `json:"activation_password"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			claims := req.Context().Value(claimsKey).(*jwtClaims)

			// Пустой license = деактивация до Free (кнопка «Деактивировать» шлёт "").
			if strings.TrimSpace(body.License) == "" {
				st := mgr.Deactivate()
				h.audit(req.Context(), claims.UserID, claims.Email, "deactivate_license", "license", "", nil)
				writeJSON(w, http.StatusOK, st)
				return
			}

			st, err := mgr.Apply(body.License, body.ActivationPassword)
			if err != nil {
				// Текст отдаём наружу: интерцептор веба показывает его как есть, и админу
				// нужно понимать причину (не тот ключ / не тот пароль / битый файл).
				http.Error(w, licenseRejectMsg(err), http.StatusBadRequest)
				return
			}
			h.audit(req.Context(), claims.UserID, claims.Email, "apply_license", "license", "",
				map[string]any{"licensee": st.Licensee, "edition": st.Edition, "valid": st.Valid})
			writeJSON(w, http.StatusOK, st)
		})
	}
}

func licenseRejectMsg(err error) string {
	switch {
	case errors.Is(err, license.ErrSignature):
		return "license rejected: invalid signature (wrong key or corrupted file)"
	case errors.Is(err, license.ErrPassword):
		return "license rejected: wrong activation password"
	case errors.Is(err, license.ErrMalformed):
		return "license rejected: malformed key"
	default:
		return "license rejected"
	}
}
