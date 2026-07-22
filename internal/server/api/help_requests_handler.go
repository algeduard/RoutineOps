package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// ====== Обращения за помощью (help requests) ======
// Список и скриншот — read-only (все роли, как алерты); закрытие — it_admin.

func (h *Handler) listHelpRequests(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	status := r.URL.Query().Get("status")
	if status != "" && status != "new" && status != "closed" {
		http.Error(w, "status must be 'new' or 'closed'", http.StatusBadRequest)
		return
	}
	rows, err := h.db.ListHelpRequests(r.Context(), deviceID, status)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []storage.HelpRequestRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) getHelpRequestScreenshot(w http.ResponseWriter, r *http.Request) {
	img, err := h.db.GetHelpRequestScreenshot(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(img) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Скриншот иммутабелен (обращение не редактируется) — браузеру можно кэшировать,
	// но только приватно: это содержимое чужого экрана.
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(img)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img)
}

type setHelpRequestStatusReq struct {
	Status string `json:"status"` // "closed" — закрыть, "new" — переоткрыть
}

func (h *Handler) setHelpRequestStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := r.Context().Value(claimsKey).(*jwtClaims)

	var req setHelpRequestStatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Status != "new" && req.Status != "closed" {
		http.Error(w, "status must be 'new' or 'closed'", http.StatusBadRequest)
		return
	}
	if err := h.db.SetHelpRequestStatus(r.Context(), id, req.Status, claims.UserID); err != nil {
		if errors.Is(err, storage.ErrHelpRequestNotFound) {
			http.Error(w, "not found or already in this status", http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": req.Status})
	auditAction := "close_help_request"
	if req.Status == "new" {
		auditAction = "reopen_help_request"
	}
	h.audit(r.Context(), claims.UserID, claims.Email, auditAction, "help_request", id, nil)
}
