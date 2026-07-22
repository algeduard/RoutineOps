package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// maxRosterRows — потолок строк за один импорт. Ростер справочный, но защищаемся от
// заливки многомегабайтного мусора: даже крупный парк не превышает этого порядка.
const maxRosterRows = 50000

// maxRosterBody — тело импорта (JSON-массив строк). ~8 МиБ хватает на 50k строк с полями.
const maxRosterBody = 8 << 20

type migrationImportRequest struct {
	BatchLabel string                       `json:"batch_label"`
	SourceMDM  string                       `json:"source_mdm"`
	Rows       []storage.MigrationRosterRow `json:"rows"`
}

// importMigrationRoster заливает партию ожидаемых устройств из старого MDM (it_admin).
// Ростер СПРАВОЧНЫЙ: он не создаёт устройств, не меняет статусов и не открывает доступ —
// поэтому requireHuman тут не нужен (в отличие от выпуска bulk-токена). Идемпотентно:
// повторная заливка того же CSV не задваивает строки (см. storage.ImportMigrationRoster).
func (h *Handler) importMigrationRoster(w http.ResponseWriter, r *http.Request) {
	var req migrationImportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRosterBody)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(req.Rows) == 0 {
		http.Error(w, "no rows to import", http.StatusBadRequest)
		return
	}
	if len(req.Rows) > maxRosterRows {
		http.Error(w, "too many rows in one import", http.StatusBadRequest)
		return
	}
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	inserted, err := h.db.ImportMigrationRoster(r.Context(), req.BatchLabel, req.SourceMDM, claims.Email, req.Rows)
	if err != nil {
		slog.Error("import migration roster", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.audit(r.Context(), claims.UserID, claims.Email, "import_migration_roster", "migration_roster", "",
		map[string]any{"batch_label": req.BatchLabel, "source_mdm": req.SourceMDM, "received": len(req.Rows), "inserted": inserted})
	writeJSON(w, http.StatusOK, map[string]int{"inserted": inserted, "received": len(req.Rows)})
}

// listMigrationRoster — весь ростер с матчем на чтении + сводка прогресса миграции.
// Read-only (весь парк виден всем ролям). Сводку считаем здесь, отдельного запроса нет:
// список и так проходится целиком.
func (h *Handler) listMigrationRoster(w http.ResponseWriter, r *http.Request) {
	entries, err := h.db.ListMigrationRoster(r.Context())
	if err != nil {
		slog.Error("list migration roster", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	arrived := 0
	for _, e := range entries {
		if e.MatchedDeviceID != "" {
			arrived++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": map[string]int{"total": len(entries), "arrived": arrived, "pending": len(entries) - arrived},
		"entries": entries,
	})
}

// deleteMigrationRoster чистит ростер (it_admin). ?all=true — весь ростер; ?batch=<метка> —
// одну партию (в т.ч. безымянную: ?batch= с пустым значением). Требуем ЯВНО указать что
// именно: без параметров — 400, чтобы случайный/пустой запрос не снёс весь ростер (снос
// прицельный, но данные восстанавливаются повторным импортом CSV; парк не трогается —
// гейтим ролью, не requireHuman).
func (h *Handler) deleteMigrationRoster(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	all := q.Get("all") == "true"
	if !all && !q.Has("batch") {
		http.Error(w, "specify ?all=true or ?batch=<label>", http.StatusBadRequest)
		return
	}
	batch := q.Get("batch")
	n, err := h.db.DeleteMigrationRoster(r.Context(), batch, all)
	if err != nil {
		slog.Error("delete migration roster", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	h.audit(r.Context(), claims.UserID, claims.Email, "delete_migration_roster", "migration_roster", "",
		map[string]any{"batch_label": batch, "all": all, "deleted": n})
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}

// getDeviceMigrationInfo отдаёт строку ростера, из которой пришло устройство (для
// карточки). null = устройство не из импортированного парка. Read-only.
func (h *Handler) getDeviceMigrationInfo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	entry, err := h.db.MigrationRosterForDevice(r.Context(), id)
	if err != nil {
		slog.Error("device migration info", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, entry) // nil → null: карточка просто не показывает блок
}
