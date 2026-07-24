//go:build enterprise

package alertrouting

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
	"github.com/Floodww/RoutineOps/internal/server/testutil"
)

var sharedDSN string

func TestMain(m *testing.M) {
	dsn, cleanup := testutil.NewDSNWithCleanup()
	sharedDSN = dsn
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Connect(context.Background(), sharedDSN)
	if err != nil {
		t.Fatalf("storage.Connect: %v", err)
	}
	t.Cleanup(db.Close)
	// Изоляция тестов внутри пакета: правила и алерты копятся в общей temp-БД и иначе
	// текли бы из теста в тест (лишние доставки). CASCADE с devices чистит и alerts.
	if _, err := db.Pool().Exec(context.Background(),
		`TRUNCATE devices, alerts, alert_routing_rules RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

func mustDevice(t *testing.T, db *storage.DB) string {
	t.Helper()
	d, err := db.CreatePendingDevice(context.Background(), "rt-host-"+t.Name(), "windows")
	if err != nil {
		t.Fatalf("CreatePendingDevice: %v", err)
	}
	return d.ID
}

// tgCapture — потокобезопасный перехват адресной telegram-доставки.
type tgCapture struct {
	mu    sync.Mutex
	calls []struct{ chatID, text string }
}

func (c *tgCapture) send(_ context.Context, chatID, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, struct{ chatID, text string }{chatID, text})
	return nil
}

func (c *tgCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func newTestRouter(db *storage.DB, licensed bool, tg TelegramSendFunc) *Router {
	r := NewRouter(db, func() bool { return licensed }, tg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.lagSeconds = 0 // в тесте обрабатываем только что созданные алерты
	return r
}

// TestRouteNewDeliversByChannel: critical-алерт уходит и в telegram (min warning), и на
// webhook (min critical); после обработки помечается routed.
func TestRouteNewDeliversByChannel(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	dev := mustDevice(t, db)

	var gotWebhook struct {
		Alert struct {
			Severity  string `json:"severity"`
			AlertType string `json:"alert_type"`
		} `json:"alert"`
		Escalation bool `json:"escalation"`
	}
	webhookHit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &gotWebhook)
		webhookHit <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := db.CreateAlertRoutingRule(ctx, "warning", "telegram", "-100777", true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlertRoutingRule(ctx, "critical", "webhook", srv.URL, true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlert(ctx, dev, "forbidden_software", `{"p":"bad.exe"}`, ""); err != nil {
		t.Fatal(err)
	}

	tg := &tgCapture{}
	newTestRouter(db, true, tg.send).tick()

	if tg.count() != 1 {
		t.Fatalf("telegram доставок = %d, want 1", tg.count())
	}
	if tg.calls[0].chatID != "-100777" {
		t.Fatalf("telegram chat_id = %q, want -100777", tg.calls[0].chatID)
	}
	select {
	case <-webhookHit:
	default:
		t.Fatal("webhook не получил доставку")
	}
	if gotWebhook.Alert.Severity != "critical" || gotWebhook.Alert.AlertType != "forbidden_software" || gotWebhook.Escalation {
		t.Fatalf("webhook payload неверен: %+v", gotWebhook)
	}
	// Алерт помечен обработанным.
	if pending, _ := db.ListUnroutedAlerts(ctx, 0, 100); len(pending) != 0 {
		t.Fatalf("ожидали 0 необработанных, got %d", len(pending))
	}
}

// TestSeverityThresholdFilters: warning-алерт не доходит до critical-правила, но доходит до
// warning-правила.
func TestSeverityThresholdFilters(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	dev := mustDevice(t, db)

	criticalHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		criticalHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := db.CreateAlertRoutingRule(ctx, "critical", "webhook", srv.URL, true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlertRoutingRule(ctx, "warning", "telegram", "-100888", true, 0); err != nil {
		t.Fatal(err)
	}
	// unauthorized_settings_change → severity warning.
	if _, err := db.CreateAlert(ctx, dev, "unauthorized_settings_change", `{"k":"v"}`, ""); err != nil {
		t.Fatal(err)
	}

	tg := &tgCapture{}
	newTestRouter(db, true, tg.send).tick()

	if criticalHit {
		t.Fatal("warning-алерт не должен доходить до critical-правила")
	}
	if tg.count() != 1 {
		t.Fatalf("telegram доставок = %d, want 1 (warning-правило)", tg.count())
	}
}

// TestUnlicensedNoDelivery: без лицензии тик пустой — ничего не доставляется и не помечается.
func TestUnlicensedNoDelivery(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	dev := mustDevice(t, db)
	if _, err := db.CreateAlertRoutingRule(ctx, "info", "telegram", "-100999", true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlert(ctx, dev, "forbidden_software", `{"p":"x"}`, ""); err != nil {
		t.Fatal(err)
	}

	tg := &tgCapture{}
	newTestRouter(db, false, tg.send).tick()

	if tg.count() != 0 {
		t.Fatalf("без лицензии доставок = %d, want 0", tg.count())
	}
	if pending, _ := db.ListUnroutedAlerts(ctx, 0, 100); len(pending) == 0 {
		t.Fatal("без лицензии алерт не должен помечаться обработанным")
	}
}

// TestEscalationReDelivers: routed непринятый critical старше порога эскалируется повторно;
// второй тик подряд не спамит (анти-спам по last_escalated_at).
func TestEscalationReDelivers(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	dev := mustDevice(t, db)
	if _, err := db.CreateAlertRoutingRule(ctx, "warning", "telegram", "-100111", true, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlert(ctx, dev, "forbidden_software", `{"p":"esc.exe"}`, ""); err != nil {
		t.Fatal(err)
	}
	// Делаем алерт «старым» и уже routed, чтобы routeNew его не трогал, а escalate — увидел.
	if _, err := db.Pool().Exec(ctx,
		`UPDATE alerts SET created_at = now() - interval '10 minutes', routed_at = now() WHERE device_id = $1`, dev); err != nil {
		t.Fatal(err)
	}

	tg := &tgCapture{}
	r := newTestRouter(db, true, tg.send)
	r.tick()
	if tg.count() != 1 {
		t.Fatalf("первая эскалация: доставок = %d, want 1", tg.count())
	}
	// Второй тик сразу — анти-спам (last_escalated_at только что проставлен, порог 1 мин).
	r.tick()
	if tg.count() != 1 {
		t.Fatalf("повторный тик не должен спамить: доставок = %d, want 1", tg.count())
	}
}

// Device-контролируемые поля (hostname/AlertType/Details) должны быть HTML-экранированы:
// иначе битый HTML → Telegram 400 → тихая потеря алерта, либо инъекция разметки/фишинга.
func TestFormatTelegramEscapesDeviceFields(t *testing.T) {
	a := storage.RoutableAlert{
		Severity:       "critical",
		AlertType:      "forbidden_software",
		DeviceHostname: "host<b>x",
		Details:        `<a href="http://evil">click</a> A<B & C`,
	}
	msg := formatTelegram(a, false)
	if strings.Contains(msg, "<a href") || strings.Contains(msg, "<b>x") {
		t.Fatalf("неэкранированная device-разметка в сообщении: %s", msg)
	}
	if !strings.Contains(msg, "&lt;a href") || !strings.Contains(msg, "A&lt;B") {
		t.Fatalf("device-поля должны быть экранированы: %s", msg)
	}
	if !strings.Contains(msg, "<code>") || !strings.Contains(msg, "<b>") {
		t.Fatalf("свои теги (<code>/<b>) должны сохраниться: %s", msg)
	}
}
