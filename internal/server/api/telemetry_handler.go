package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/go-chi/chi/v5"
)

// Телеметрия устройств (REST). Чтение — read-only (viewer+): в этом MDM весь парк
// виден всем ролям (как /devices, /devices/{id}/tasks), поэтому per-device
// ownership-скоуп не нужен. Мутация privacy-флага — только it_admin + аудит.

// getDeviceMetrics отдаёт историю метрик ресурсов, даунсэмпленную под диапазон:
// range=1h (по умолчанию, 1-мин корзины) | range=24h (10-мин корзины).
func (h *Handler) getDeviceMetrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	since, bucket := metricsRange(r.URL.Query().Get("range"))
	rows, err := h.db.GetResourceMetrics(r.Context(), id, since, bucket)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []storage.ResourceMetricRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// metricsRange переводит параметр range в (since, bucketSeconds). Число корзин
// держим ~60–150, чтобы SVG-график оставался лёгким независимо от частоты сэмплов.
func metricsRange(rng string) (time.Time, int) {
	now := time.Now()
	switch rng {
	case "24h":
		return now.Add(-24 * time.Hour), 600 // ~144 точек
	default:
		return now.Add(-1 * time.Hour), 60 // ~60 точек
	}
}

// getDeviceMetricsLatest отдаёт последний сэмпл (живое значение в карточке).
// null, если метрик ещё нет.
func (h *Handler) getDeviceMetricsLatest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := h.db.GetLatestResourceMetric(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, row) // row может быть nil → JSON null
}

type appUsageResponse struct {
	AppUsageEnabled bool                       `json:"app_usage_enabled"`
	Apps            []storage.AppUsageRow      `json:"apps"`
	Days            []storage.DailyActivityRow `json:"days"`
}

// getDeviceAppUsage отдаёт отчёт активности приложений: топ приложений по времени и
// активность по дням за диапазон (range=7d по умолчанию | 30d). Плюс текущее
// состояние privacy-флага — чтобы UI показал тумблер и объяснил пустой отчёт.
func (h *Handler) getDeviceAppUsage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	days := 7
	if r.URL.Query().Get("range") == "30d" {
		days = 30
	}
	// Включаем сегодняшний день: since = сегодня-(days-1).
	since := time.Now().AddDate(0, 0, -(days - 1))

	apps, activity, err := h.db.GetAppUsage(r.Context(), id, since)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	enabled, _, err := h.db.GetAppUsageEnabled(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if apps == nil {
		apps = []storage.AppUsageRow{}
	}
	if activity == nil {
		activity = []storage.DailyActivityRow{}
	}
	writeJSON(w, http.StatusOK, appUsageResponse{AppUsageEnabled: enabled, Apps: apps, Days: activity})
}

type telemetryConfigResponse struct {
	AppUsageEnabled bool `json:"app_usage_enabled"`
}

// getDeviceTelemetryConfig отдаёт текущее состояние privacy-флага сбора аналитики.
func (h *Handler) getDeviceTelemetryConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	enabled, found, err := h.db.GetAppUsageEnabled(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, telemetryConfigResponse{AppUsageEnabled: enabled})
}

type setTelemetryConfigRequest struct {
	// Указатель, чтобы отличить отсутствие поля от явного false.
	AppUsageEnabled *bool `json:"app_usage_enabled"`
}

// setDeviceTelemetryConfig включает/выключает сбор аналитики приложений для
// устройства (privacy/consent). it_admin + аудит: включение слежки прослеживается.
func (h *Handler) setDeviceTelemetryConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req setTelemetryConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.AppUsageEnabled == nil {
		http.Error(w, "app_usage_enabled is required", http.StatusBadRequest)
		return
	}
	found, err := h.db.SetAppUsageEnabled(r.Context(), id, *req.AppUsageEnabled)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	h.audit(r.Context(), claims.UserID, claims.Email, "set_telemetry_config", "device", id,
		map[string]bool{"app_usage_enabled": *req.AppUsageEnabled})
	writeJSON(w, http.StatusOK, telemetryConfigResponse{AppUsageEnabled: *req.AppUsageEnabled})
}
