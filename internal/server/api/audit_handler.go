package api

import (
	"net/http"
	"strconv"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func (h *Handler) listAuditLog(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := h.db.ListAuditLog(r.Context(), action, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []storage.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
