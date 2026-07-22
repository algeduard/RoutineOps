package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// uniqSuffix — уникальный суффикс, чтобы os/CN не пересекались между тестами в общей БД.
func uniqSuffix() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// it_admin переводит устройство на beta, канал виден в карточке; невалидное значение → 400;
// несуществующее устройство → 404.
func TestSetDeviceUpdateChannel(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	tok := authToken(t, rtr, db)

	deviceID, _ := createDevice(t, rtr, tok, "host-channel", "windows")

	// Дефолт в карточке — stable.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/devices/"+deviceID, nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("get device: %d %s", w.Code, w.Body)
	}
	var resp struct {
		Device struct {
			UpdateChannel string `json:"update_channel"`
		} `json:"device"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Device.UpdateChannel != "stable" {
		t.Fatalf("дефолтный канал = %q, ожидали stable", resp.Device.UpdateChannel)
	}

	// Перевод на beta.
	body, _ := json.Marshal(map[string]string{"channel": "beta"})
	w = authedDo(t, rtr, http.MethodPut, fmt.Sprintf("/api/v1/devices/%s/update-channel", deviceID), body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("set channel: %d %s", w.Code, w.Body)
	}
	var setResp map[string]string
	json.NewDecoder(w.Body).Decode(&setResp)
	if setResp["update_channel"] != "beta" {
		t.Fatalf("ответ = %q, ожидали beta", setResp["update_channel"])
	}

	// Карточка теперь показывает beta.
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/devices/"+deviceID, nil, tok)
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Device.UpdateChannel != "beta" {
		t.Fatalf("канал в карточке после перевода = %q, ожидали beta", resp.Device.UpdateChannel)
	}

	// Невалидный канал → 400.
	bad, _ := json.Marshal(map[string]string{"channel": "nightly"})
	w = authedDo(t, rtr, http.MethodPut, fmt.Sprintf("/api/v1/devices/%s/update-channel", deviceID), bad, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("невалидный канал: got %d, want 400; body: %s", w.Code, w.Body)
	}

	// Несуществующее устройство → 404.
	w = authedDo(t, rtr, http.MethodPut, "/api/v1/devices/00000000-0000-0000-0000-000000000000/update-channel", body, tok)
	if w.Code != http.StatusNotFound {
		t.Fatalf("несуществующее устройство: got %d, want 404; body: %s", w.Code, w.Body)
	}
}

// Смена канала — мутация it_admin: viewer-роль отбивается 403.
func TestSetDeviceUpdateChannel_ForbiddenForViewer(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	adminTok := authToken(t, rtr, db)
	viewerTok := tokenForRole(t, rtr, db, "viewer", "viewer_")

	deviceID, _ := createDevice(t, rtr, adminTok, "host-channel-403", "windows")

	body, _ := json.Marshal(map[string]string{"channel": "beta"})
	w := authedDo(t, rtr, http.MethodPut, fmt.Sprintf("/api/v1/devices/%s/update-channel", deviceID), body, viewerTok)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer: got %d, want 403; body: %s", w.Code, w.Body)
	}
}

// Полный путь гейтинга через публичный манифест-эндпоинт: агент beta-устройства
// (присылает свой CN в ?device=) получает beta-релиз; stable-устройство и запрос без
// device — только stable. Эндпоинт публичный (без auth).
func TestAgentVersion_ChannelGating(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db)
	ctx := context.Background()

	osName := "chanos" + uniqSuffix()
	arch := "amd64"
	if err := db.RegisterAgentRelease(ctx, osName, arch, "v2.0.0", "f_s", "sha_s", "sig", "msig", storage.ChannelStable); err != nil {
		t.Fatalf("register stable: %v", err)
	}
	if err := db.RegisterAgentRelease(ctx, osName, arch, "v2.1.0", "f_b", "sha_b", "sig", "msig", storage.ChannelBeta); err != nil {
		t.Fatalf("register beta: %v", err)
	}

	// beta-устройство заезжает через heartbeat (cert_cn = CN, который пришлёт агент).
	cn := "cn-beta-" + uniqSuffix()
	fp := "fp-" + cn
	if err := db.UpsertDeviceHeartbeat(ctx, storage.HeartbeatData{
		CertFingerprint: fp, DeviceID: cn, CertCN: cn, IPAddress: "192.0.2.30",
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	id, err := db.GetDeviceIDByFingerprint(ctx, fp)
	if err != nil || id == "" {
		t.Fatalf("device id: %q %v", id, err)
	}
	if _, err := db.SetDeviceUpdateChannel(ctx, id, storage.ChannelBeta); err != nil {
		t.Fatalf("set beta: %v", err)
	}

	manifestVersion := func(query string) string {
		t.Helper()
		w := authedDo(t, rtr, http.MethodGet, "/api/v1/agent/version?"+query, nil, "")
		if w.Code != http.StatusOK {
			t.Fatalf("manifest %q: %d %s", query, w.Code, w.Body)
		}
		var m map[string]string
		json.NewDecoder(w.Body).Decode(&m)
		return m["version"]
	}

	if v := manifestVersion(fmt.Sprintf("os=%s&arch=%s&device=%s", osName, arch, cn)); v != "v2.1.0" {
		t.Errorf("beta-устройство получило %q, ожидали v2.1.0", v)
	}
	if v := manifestVersion(fmt.Sprintf("os=%s&arch=%s", osName, arch)); v != "v2.0.0" {
		t.Errorf("запрос без device получил %q, ожидали stable v2.0.0", v)
	}
	if v := manifestVersion(fmt.Sprintf("os=%s&arch=%s&device=unknown-cn", osName, arch)); v != "v2.0.0" {
		t.Errorf("неизвестный device получил %q, ожидали stable v2.0.0", v)
	}
}
