package storage_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func TestAlertSeverityRankAndValidation(t *testing.T) {
	if storage.AlertSeverityRank("critical") <= storage.AlertSeverityRank("warning") ||
		storage.AlertSeverityRank("warning") <= storage.AlertSeverityRank("info") {
		t.Fatal("ранг должен расти info < warning < critical")
	}
	// Неизвестное значение трактуется как warning (как DEFAULT колонки).
	if storage.AlertSeverityRank("bogus") != storage.AlertSeverityRank("warning") {
		t.Fatal("неизвестная severity должна трактоваться как warning")
	}
	if !storage.ValidAlertSeverity("info") || !storage.ValidAlertSeverity("critical") || storage.ValidAlertSeverity("bogus") {
		t.Fatal("ValidAlertSeverity")
	}
	if !storage.ValidAlertChannel("telegram") || !storage.ValidAlertChannel("webhook") || storage.ValidAlertChannel("smoke") {
		t.Fatal("ValidAlertChannel")
	}
}

// TestCreateAlert_SetsSeverityByType: нарушение политики ПО → critical, прочее → warning.
func TestCreateAlert_SetsSeverityByType(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	d := mustCreateDevice(t, db, fmt.Sprintf("host-sev-%s", uniq(t)), "windows")

	if _, err := db.CreateAlert(ctx, d.ID, "forbidden_software", `{"p":"bad.exe"}`, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAlert(ctx, d.ID, "unauthorized_settings_change", `{"k":"v"}`, ""); err != nil {
		t.Fatal(err)
	}
	alerts, err := db.ListAlerts(ctx, d.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range alerts {
		got[a.AlertType] = a.Severity
	}
	if got["forbidden_software"] != "critical" {
		t.Fatalf("forbidden_software severity = %q, want critical", got["forbidden_software"])
	}
	if got["unauthorized_settings_change"] != "warning" {
		t.Fatalf("unauthorized_settings_change severity = %q, want warning", got["unauthorized_settings_change"])
	}
}

// TestUnreachableAlertDefaultSeverity: agent_unreachable из детектора получает DEFAULT warning.
func TestUnreachableAlertDefaultSeverity(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	d := mustCreateActiveDevice(t, db, fmt.Sprintf("host-unreach-%s", uniq(t)), "windows")
	// Сдвигаем last_seen_at в прошлое, чтобы детектор счёл устройство недоступным.
	if _, err := db.Pool().Exec(ctx, `UPDATE devices SET last_seen_at = now() - interval '1 hour' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DetectUnreachableDevices(ctx, 15, 0); err != nil {
		t.Fatal(err)
	}
	alerts, _ := db.ListAlerts(ctx, d.ID, 10)
	if len(alerts) == 0 {
		t.Fatal("ожидали alert agent_unreachable")
	}
	if alerts[0].Severity != "warning" {
		t.Fatalf("agent_unreachable severity = %q, want warning", alerts[0].Severity)
	}
}

func TestAlertRoutingRuleCRUD(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	rule, err := db.CreateAlertRoutingRule(ctx, "critical", "webhook", "https://hook.example/ingest", true, 30)
	if err != nil {
		t.Fatal(err)
	}
	if rule.ID == "" || rule.MinSeverity != "critical" || rule.Channel != "webhook" || !rule.Enabled || rule.EscalateAfterMinutes != 30 {
		t.Fatalf("созданное правило неверно: %+v", rule)
	}

	rules, err := db.ListAlertRoutingRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rules {
		if r.ID == rule.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("созданное правило не в списке")
	}

	ok, err := db.DeleteAlertRoutingRule(ctx, rule.ID)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	// Повторное удаление — found=false, без ошибки.
	if ok, err := db.DeleteAlertRoutingRule(ctx, rule.ID); err != nil || ok {
		t.Fatalf("повторное удаление: ok=%v err=%v", ok, err)
	}
}

// TestUnroutedAlertsCursor: новый алерт виден ListUnroutedAlerts (лаг 0), после MarkAlertRouted
// выпадает из выборки.
func TestUnroutedAlertsCursor(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	d := mustCreateDevice(t, db, fmt.Sprintf("host-unrouted-%s", uniq(t)), "macos")
	if _, err := db.CreateAlert(ctx, d.ID, "forbidden_software", fmt.Sprintf(`{"u":"%s"}`, uniq(t)), ""); err != nil {
		t.Fatal(err)
	}

	pending, err := db.ListUnroutedAlerts(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var target *storage.RoutableAlert
	for i := range pending {
		if pending[i].DeviceID == d.ID {
			target = &pending[i]
		}
	}
	if target == nil {
		t.Fatal("новый алерт не попал в необработанные")
	}
	if target.Severity != "critical" {
		t.Fatalf("severity = %q, want critical", target.Severity)
	}

	if err := db.MarkAlertRouted(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListUnroutedAlerts(ctx, 0, 100)
	for _, a := range after {
		if a.ID == target.ID {
			t.Fatal("помеченный обработанным алерт всё ещё в выборке")
		}
	}
}

// TestEscalatableAlerts: непринятый routed critical попадает в кандидаты эскалации; после
// ack — выпадает.
func TestEscalatableAlerts(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	d := mustCreateDevice(t, db, fmt.Sprintf("host-esc-%s", uniq(t)), "macos")
	if _, err := db.CreateAlert(ctx, d.ID, "forbidden_software", fmt.Sprintf(`{"e":"%s"}`, uniq(t)), ""); err != nil {
		t.Fatal(err)
	}
	// Пока алерт не routed — в кандидаты эскалации не попадает.
	pending, _ := db.ListUnroutedAlerts(ctx, 0, 100)
	var id string
	for _, a := range pending {
		if a.DeviceID == d.ID {
			id = a.ID
		}
	}
	if id == "" {
		t.Fatal("алерт не найден")
	}
	if cands, _ := db.ListEscalatableAlerts(ctx, 100); containsAlert(cands, id) {
		t.Fatal("не-routed алерт не должен быть кандидатом эскалации")
	}
	if err := db.MarkAlertRouted(ctx, id); err != nil {
		t.Fatal(err)
	}
	cands, err := db.ListEscalatableAlerts(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAlert(cands, id) {
		t.Fatal("routed непринятый critical должен быть кандидатом эскалации")
	}
	if err := db.MarkAlertEscalated(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := db.AcknowledgeAlert(ctx, id); err != nil {
		t.Fatal(err)
	}
	if cands, _ := db.ListEscalatableAlerts(ctx, 100); containsAlert(cands, id) {
		t.Fatal("принятый алерт не должен быть кандидатом эскалации")
	}
}

func containsAlert(alerts []storage.RoutableAlert, id string) bool {
	for _, a := range alerts {
		if a.ID == id {
			return true
		}
	}
	return false
}
