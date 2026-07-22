package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

type telemetryConfigResp struct {
	AppUsageEnabled     bool `json:"app_usage_enabled"`
	CaptureWindowTitles bool `json:"capture_window_titles"`
	CaptureURLs         bool `json:"capture_urls"`
}

// TestTelemetryConfig_CaptureURLsDefaultOffAuditedToggle: capture_urls по умолчанию
// ВЫКЛЮЧЕН, включается через it_admin-гейт PUT /telemetry-config и это ФИКСИРУЕТСЯ в
// аудите. Зеркалит гарантии capture_window_titles (privacy: включение слежки за URL
// прослеживается).
func TestTelemetryConfig_CaptureURLsDefaultOffAuditedToggle(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	ctx := context.Background()

	dev, err := db.CreatePendingDevice(ctx, "url-cfg-host", "Windows")
	if err != nil {
		t.Fatalf("CreatePendingDevice: %v", err)
	}
	token := authToken(t, rtr, db) // it_admin

	// GET: дефолт — capture_urls ВЫКЛЮЧЕН.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/devices/"+dev.ID+"/telemetry-config", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET config: %d %s", w.Code, w.Body)
	}
	var got telemetryConfigResp
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CaptureURLs {
		t.Fatal("capture_urls по умолчанию должен быть ВЫКЛЮЧЕН")
	}

	// PUT capture_urls=true (it_admin).
	body, _ := json.Marshal(map[string]bool{"capture_urls": true})
	w = authedDo(t, rtr, http.MethodPut, "/api/v1/devices/"+dev.ID+"/telemetry-config", body, token)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal put: %v", err)
	}
	if !got.CaptureURLs {
		t.Fatal("после PUT capture_urls должен быть ВКЛЮЧЁН")
	}

	// Персистентность: повторный GET показывает true.
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/devices/"+dev.ID+"/telemetry-config", nil, token)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.CaptureURLs {
		t.Fatal("capture_urls должен персиститься")
	}

	// Аудит: включение слежки за URL зафиксировано (action set_telemetry_config,
	// details.capture_urls=true). Зеркалит требование к capture_window_titles.
	entries, err := db.ListAuditLog(ctx, "set_telemetry_config", 50)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	foundAudit := false
	for _, e := range entries {
		if e.TargetID != dev.ID {
			continue
		}
		var details map[string]bool
		if err := json.Unmarshal(e.Details, &details); err != nil {
			continue
		}
		if details["capture_urls"] {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatal("включение capture_urls должно попасть в аудит с details.capture_urls=true")
	}
}

// TestTelemetryConfig_CaptureURLsRequiresAdmin: включение capture_urls запрещено роли
// без it_admin (privacy-гейт на мутацию телеметрии).
func TestTelemetryConfig_CaptureURLsRequiresAdmin(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	ctx := context.Background()

	dev, err := db.CreatePendingDevice(ctx, "url-cfg-host2", "Windows")
	if err != nil {
		t.Fatalf("CreatePendingDevice: %v", err)
	}
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")

	body, _ := json.Marshal(map[string]bool{"capture_urls": true})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/devices/"+dev.ID+"/telemetry-config", body, viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("PUT capture_urls as viewer: %d %s, want 403", w.Code, w.Body)
	}
	// Флаг не изменился.
	if enabled, _, _ := db.GetCaptureURLs(ctx, dev.ID); enabled {
		t.Fatal("capture_urls не должен включиться из-под не-admin")
	}
}
