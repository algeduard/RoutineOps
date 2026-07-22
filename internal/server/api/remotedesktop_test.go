package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/registry"
	"github.com/Floodww/RoutineOps/internal/server/remotedesktop"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// getDeviceRDUnattended читает флаг rd_unattended из карточки устройства (GET
// /devices/{id}) — тем же путём, что и веб, чтобы тумблер показал актуальное состояние.
func getDeviceRDUnattended(t *testing.T, rtr http.Handler, tok, id string) bool {
	t.Helper()
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/devices/"+id, nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /devices/%s: %d %s", id, w.Code, w.Body)
	}
	var resp struct {
		Device struct {
			RDUnattended bool `json:"rd_unattended"`
		} `json:"device"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	return resp.Device.RDUnattended
}

// Основной путь: it_admin (человек) включает unattended, карточка это отражает,
// действие пишется в аудит; затем выключает.
func TestSetRDUnattended_SetGetAndAudit(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	tok := authToken(t, rtr, db)
	dev, err := db.CreatePendingDevice(context.Background(), "host-rd-"+t.Name(), "windows")
	if err != nil {
		t.Fatalf("CreatePendingDevice: %v", err)
	}

	// Дефолт: выключено.
	if getDeviceRDUnattended(t, rtr, tok, dev.ID) {
		t.Fatalf("rd_unattended по умолчанию = true, ожидалось false")
	}

	// Включаем.
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/devices/"+dev.ID+"/rd-unattended",
		[]byte(`{"unattended":true}`), tok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT rd-unattended true: %d %s", w.Code, w.Body)
	}
	var resp struct {
		Unattended bool `json:"unattended"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.Unattended {
		t.Fatalf("ответ: unattended=%v err=%v", resp.Unattended, err)
	}
	if !getDeviceRDUnattended(t, rtr, tok, dev.ID) {
		t.Fatalf("карточка не отразила включение unattended")
	}

	// Аудит: включение прослеживается.
	entries, err := db.ListAuditLog(context.Background(), "set_rd_unattended", 10)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.TargetID == dev.ID {
			found = true
			var d map[string]bool
			if json.Unmarshal(e.Details, &d) == nil && d["unattended"] {
				break
			}
			t.Fatalf("аудит без корректного details: %s", string(e.Details))
		}
	}
	if !found {
		t.Fatalf("нет записи аудита set_rd_unattended для устройства %s", dev.ID)
	}

	// Выключаем обратно.
	w = authedDo(t, rtr, http.MethodPut, "/api/v1/devices/"+dev.ID+"/rd-unattended",
		[]byte(`{"unattended":false}`), tok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT rd-unattended false: %d %s", w.Code, w.Body)
	}
	if getDeviceRDUnattended(t, rtr, tok, dev.ID) {
		t.Fatalf("карточка не отразила выключение unattended")
	}
}

// RBAC: viewer не может включать unattended (requireRole it_admin).
func TestSetRDUnattended_RequiresItAdmin(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	admin := authToken(t, rtr, db)
	dev, _ := db.CreatePendingDevice(context.Background(), "host-rd-"+t.Name(), "windows")

	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/devices/"+dev.ID+"/rd-unattended",
		[]byte(`{"unattended":true}`), viewer)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer: got %d, want 403", w.Code)
	}
	// И политика не изменилась.
	if getDeviceRDUnattended(t, rtr, admin, dev.ID) {
		t.Fatalf("viewer смог включить unattended")
	}
}

// requireHuman: сервисный токен (даже с ролью it_admin) НЕ может включать unattended —
// снятие consent-гейта должно быть решением человека, не автоматизации.
func TestSetRDUnattended_RejectsServiceToken(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	admin := authToken(t, rtr, db)
	dev, _ := db.CreatePendingDevice(context.Background(), "host-rd-"+t.Name(), "windows")

	secret, _ := createToken(t, rtr, admin, "ci", "it_admin", 0)
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/devices/"+dev.ID+"/rd-unattended",
		[]byte(`{"unattended":true}`), "Bearer "+secret)
	if w.Code != http.StatusForbidden {
		t.Fatalf("сервисный it_admin-токен: got %d, want 403 (requireHuman)", w.Code)
	}
	if getDeviceRDUnattended(t, rtr, admin, dev.ID) {
		t.Fatalf("сервисный токен смог включить unattended в обход requireHuman")
	}
}

// Валидация: пустое тело → 400; несуществующее устройство → 404.
func TestSetRDUnattended_Validation(t *testing.T) {
	rtr, db := newRouterWithDB(t)
	tok := authToken(t, rtr, db)
	dev, _ := db.CreatePendingDevice(context.Background(), "host-rd-"+t.Name(), "windows")

	// Отсутствует поле unattended → 400 (нельзя случайно выключить пустым JSON).
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/devices/"+dev.ID+"/rd-unattended",
		[]byte(`{}`), tok); w.Code != http.StatusBadRequest {
		t.Fatalf("пустое тело: got %d, want 400", w.Code)
	}
	// Несуществующее устройство → 404.
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/devices/00000000-0000-0000-0000-000000000000/rd-unattended",
		[]byte(`{"unattended":true}`), tok); w.Code != http.StatusNotFound {
		t.Fatalf("несуществующее устройство: got %d, want 404", w.Code)
	}
}

// Ядро безопасности: START-команда, отправляемая устройству при открытии сессии,
// несёт unattended РОВНО по opt-in-политике устройства. Именно этот флаг решает,
// пропустит ли агент запрос согласия — значит «согласие пропускается ТОЛЬКО когда
// включено» проверяется здесь, на серверной границе.
//
// START уходит через registry.Send ДО websocket.Accept, поэтому обычный (не-upgrade)
// авторизованный GET уже кладёт задачу в канал устройства (Accept затем падает на
// ResponseRecorder — не WebSocket-рукопожатие — и хендлер выходит). WS-клиент/таймаут
// для этой проверки не нужны.
func TestRemoteDesktopStart_ConsentSkippedOnlyWhenEnabled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy bool
	}{
		{"unattended_enabled", true},
		{"unattended_disabled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			reg := registry.New()
			bridge := remotedesktop.New()
			rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local",
				t.TempDir(), mailer.New("", "", "", "", "", false), false,
				api.WithRemoteDesktop(reg, bridge))

			ctx := context.Background()
			// Активное устройство с cert_cn (ключ registry / mTLS-идентичность).
			fp := "fp-rd-" + uniqAPI(t)
			cn := "cn-rd-" + uniqAPI(t)
			if err := db.UpsertDeviceHeartbeat(ctx, storage.HeartbeatData{
				CertFingerprint: fp, DeviceID: "rd-host", CertCN: cn, IPAddress: "192.0.2.9",
			}); err != nil {
				t.Fatalf("UpsertDeviceHeartbeat: %v", err)
			}
			id, err := db.GetDeviceIDByFingerprint(ctx, fp)
			if err != nil || id == "" {
				t.Fatalf("GetDeviceIDByFingerprint: %v", err)
			}
			if _, err := db.SetRDUnattended(ctx, id, tc.policy); err != nil {
				t.Fatalf("SetRDUnattended: %v", err)
			}

			// Фейковый подключённый агент: делает Connected(cn)=true и ловит START-задачу.
			taskCh, cancel := reg.Register(cn)
			defer cancel()

			tok := authToken(t, rtr, db)
			_ = authedDo(t, rtr, http.MethodGet, "/api/v1/devices/"+id+"/remote-desktop", nil, tok)

			select {
			case task := <-taskCh:
				rd := task.GetRemoteDesktop()
				if rd == nil {
					t.Fatalf("в задаче нет remote_desktop-команды")
				}
				if rd.GetUnattended() != tc.policy {
					t.Fatalf("START.Unattended = %v, ожидалось %v (по политике устройства)",
						rd.GetUnattended(), tc.policy)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("START-задача не доставлена устройству")
			}
		})
	}
}

// uniqAPI — уникальный суффикс для api_test (аналог storage-хелпера uniq).
func uniqAPI(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
