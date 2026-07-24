//go:build enterprise

package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/Floodww/RoutineOps/internal/server/worker"
	"github.com/go-chi/chi/v5"
)

// SoftwareRemovalRoutes монтирует POST /devices/{id}/software/remove — тихую деинсталляцию
// ПО (enterprise-фича «удаление ПО из интерфейса»). Гейт по лицензии: mgr.Has → 402, если
// фича не покрыта активной лицензией. В open-core роута нет вовсе (404). Ставится через
// WithAdminRoutes (it_admin): удаление ПО — чувствительное мутирующее действие.
func SoftwareRemovalRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Post("/devices/{id}/software/remove", func(w http.ResponseWriter, req *http.Request) {
			// Лицензионный гейт ПЕРВЫМ: без активной лицензии на эту фичу — 402, даже не
			// трогаем БД. Пустой Features = вся редакция (mgr.Has вернёт true).
			if !mgr.Has(license.FeatureSoftwareRemoval) {
				http.Error(w, "software removal requires an active Enterprise license", http.StatusPaymentRequired)
				return
			}
			id := chi.URLParam(req, "id")
			var body struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024)).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			name := strings.TrimSpace(body.Name)
			if name == "" {
				http.Error(w, "software name is required", http.StatusBadRequest)
				return
			}

			// Guard: устройство существует и active — иначе задача повиснет pending
			// (неактивное устройство не примет Connect). Тот же гейт, что у lock/decommission.
			st, err := h.db.GetDeviceStatusByID(req.Context(), id)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if st == "" {
				http.Error(w, "device not found", http.StatusNotFound)
				return
			}
			if st != "active" {
				http.Error(w, "device is not active", http.StatusConflict)
				return
			}

			task, err := h.db.CreateRemoveSoftwareTask(req.Context(), id, name, strings.TrimSpace(body.Version))
			if err != nil {
				if errors.Is(err, storage.ErrDeviceNotFound) {
					http.Error(w, "device not found", http.StatusNotFound)
					return
				}
				slog.Error("create remove-software task", "err", err)
				http.Error(w, "failed to create task", http.StatusInternalServerError)
				return
			}
			if err := worker.Enqueue(h.asynqClient, task.ID); err != nil {
				slog.Error("enqueue remove-software task", "task_id", task.ID, "err", err)
				http.Error(w, "enqueue failed", http.StatusInternalServerError)
				return
			}

			claims := req.Context().Value(claimsKey).(*jwtClaims)
			h.audit(req.Context(), claims.UserID, claims.Email, "remove_software", "device", id,
				map[string]string{"task_id": task.ID, "software": name, "version": body.Version})
			writeJSON(w, http.StatusOK, map[string]string{"task_id": task.ID})
		})
	}
}
