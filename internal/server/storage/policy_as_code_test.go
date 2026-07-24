package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// cleanPolicyState изолирует тест на общей БД: сносит декларации и ГЛОБАЛЬНЫЕ software-правила
// (device/group-scoped не трогаем — их создают другие тесты). Реконсиляция policy-as-code
// работает по глобальным правилам, поэтому им нужен известный чистый бэйзлайн.
func cleanPolicyState(t *testing.T, db *storage.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Pool().Exec(ctx, `DELETE FROM policy_declaration`); err != nil {
		t.Fatalf("clean policy_declaration: %v", err)
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM software_policy_rules WHERE device_id IS NULL AND group_id IS NULL`); err != nil {
		t.Fatalf("clean global software_policy_rules: %v", err)
	}
}

func globalRuleCount(t *testing.T, db *storage.DB) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM software_policy_rules WHERE device_id IS NULL AND group_id IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count global rules: %v", err)
	}
	return n
}

// E2E реконсиляции: apply 2 правил на чистый бэйзлайн создаёт 2; ручное 3-е даёт drift
// to_delete=1; повторный apply той же декларации идемпотентен (снимает лишнее, затем 0 изменений).
func TestApplyPolicyDeclarationReconcileAndDrift(t *testing.T) {
	db := newDB(t)
	cleanPolicyState(t, db)
	ctx := context.Background()

	decl := []storage.DesiredPolicyRule{
		{SoftwareName: "utorrent", RuleType: "forbidden", Platforms: []string{"Windows"}},
		{SoftwareName: "slack", RuleType: "allowed"}, // все платформы
	}

	// 1) На пустой бэйзлайн — создаётся 2, удаляется 0.
	created, deleted, err := db.ApplyPolicyDeclaration(ctx, decl, "admin@test.com")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if created != 2 || deleted != 0 {
		t.Fatalf("first apply: created=%d deleted=%d, want 2/0", created, deleted)
	}
	if got := globalRuleCount(t, db); got != 2 {
		t.Fatalf("global rules after apply = %d, want 2", got)
	}

	// Сохранённая декларация — 2 правила, applied_by протянут.
	saved, err := db.GetPolicyDeclaration(ctx)
	if err != nil || saved == nil {
		t.Fatalf("get declaration: saved=%v err=%v", saved, err)
	}
	if saved.RuleCount != 2 || len(saved.Content) != 2 || saved.AppliedBy != "admin@test.com" {
		t.Fatalf("saved declaration mismatch: %+v", saved)
	}

	// Сразу после apply дрейфа нет.
	drift, err := db.PolicyDriftAgainstSaved(ctx)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift.InSync != 2 || len(drift.ToCreate) != 0 || len(drift.ToDelete) != 0 {
		t.Fatalf("drift after apply: in_sync=%d create=%d delete=%d, want 2/0/0", drift.InSync, len(drift.ToCreate), len(drift.ToDelete))
	}

	// 2) Кто-то добавил 3-е глобальное правило в обход декларации → дрейф to_delete=1.
	if _, err := db.CreatePolicyRule(ctx, "steam", "forbidden", nil, nil); err != nil {
		t.Fatalf("manual CreatePolicyRule: %v", err)
	}
	drift, err = db.PolicyDriftAgainstSaved(ctx)
	if err != nil {
		t.Fatalf("drift after manual: %v", err)
	}
	if drift.InSync != 2 || len(drift.ToCreate) != 0 || len(drift.ToDelete) != 1 {
		t.Fatalf("drift after manual add: in_sync=%d create=%d delete=%d, want 2/0/1", drift.InSync, len(drift.ToCreate), len(drift.ToDelete))
	}
	if drift.ToDelete[0].SoftwareName != "steam" {
		t.Fatalf("to_delete[0] = %+v, want steam", drift.ToDelete[0])
	}

	// 3) Повторный apply той же декларации снимает лишнее (deleted=1, created=0) и приводит БД к декларации.
	created, deleted, err = db.ApplyPolicyDeclaration(ctx, decl, "admin@test.com")
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if created != 0 || deleted != 1 {
		t.Fatalf("re-apply: created=%d deleted=%d, want 0/1", created, deleted)
	}
	if got := globalRuleCount(t, db); got != 2 {
		t.Fatalf("global rules after re-apply = %d, want 2", got)
	}

	// 4) Идемпотентность: ещё один apply той же декларации — 0 изменений, дрейфа нет.
	created, deleted, err = db.ApplyPolicyDeclaration(ctx, decl, "admin@test.com")
	if err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	if created != 0 || deleted != 0 {
		t.Fatalf("idempotent apply: created=%d deleted=%d, want 0/0", created, deleted)
	}
	drift, err = db.PolicyDriftAgainstSaved(ctx)
	if err != nil {
		t.Fatalf("final drift: %v", err)
	}
	if drift.InSync != 2 || len(drift.ToCreate) != 0 || len(drift.ToDelete) != 0 {
		t.Fatalf("final drift: in_sync=%d create=%d delete=%d, want 2/0/0", drift.InSync, len(drift.ToCreate), len(drift.ToDelete))
	}
}

// Без сохранённой декларации GetPolicyDeclaration возвращает nil, а дрейф — пустой (source-of-
// truth не задан, «удалить всё живое» не показываем).
func TestPolicyDeclarationAbsent(t *testing.T) {
	db := newDB(t)
	cleanPolicyState(t, db)
	ctx := context.Background()

	// Живое глобальное правило есть, но декларации нет.
	if _, err := db.CreatePolicyRule(ctx, "some-app-"+uniq(t), "forbidden", nil, nil); err != nil {
		t.Fatalf("CreatePolicyRule: %v", err)
	}
	saved, err := db.GetPolicyDeclaration(ctx)
	if err != nil {
		t.Fatalf("get declaration: %v", err)
	}
	if saved != nil {
		t.Fatalf("declaration = %+v, want nil", saved)
	}
	drift, err := db.PolicyDriftAgainstSaved(ctx)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift.InSync != 0 || len(drift.ToCreate) != 0 || len(drift.ToDelete) != 0 {
		t.Fatalf("drift with no declaration: %+v, want empty", drift)
	}
}

// Пустая декларация (явное намерение) реконсилит парк в ноль — сносит все глобальные правила.
func TestApplyEmptyDeclarationClearsGlobalRules(t *testing.T) {
	db := newDB(t)
	cleanPolicyState(t, db)
	ctx := context.Background()

	if _, err := db.CreatePolicyRule(ctx, "app-a-"+uniq(t), "forbidden", nil, nil); err != nil {
		t.Fatalf("CreatePolicyRule a: %v", err)
	}
	if _, err := db.CreatePolicyRule(ctx, "app-b-"+uniq(t), "allowed", nil, nil); err != nil {
		t.Fatalf("CreatePolicyRule b: %v", err)
	}
	created, deleted, err := db.ApplyPolicyDeclaration(ctx, nil, "admin@test.com")
	if err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if created != 0 || deleted != 2 {
		t.Fatalf("apply empty: created=%d deleted=%d, want 0/2", created, deleted)
	}
	if got := globalRuleCount(t, db); got != 0 {
		t.Fatalf("global rules after empty apply = %d, want 0", got)
	}
}

// Device-scoped правило НЕ реконсилится декларацией: apply глобальной декларации его не трогает.
func TestApplyPolicyDeclarationIgnoresScopedRules(t *testing.T) {
	db := newDB(t)
	cleanPolicyState(t, db)
	ctx := context.Background()

	dev := mustCreateActiveDevice(t, db, "pac-host-"+uniq(t), "windows")
	if _, err := db.CreatePolicyRule(ctx, "scoped-app", "forbidden", &dev.ID, nil); err != nil {
		t.Fatalf("device-scoped CreatePolicyRule: %v", err)
	}

	// Пустая декларация: должна снести только глобальные (их нет), device-scoped остаётся.
	if _, _, err := db.ApplyPolicyDeclaration(ctx, nil, "admin@test.com"); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	var n int
	if err := db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM software_policy_rules WHERE device_id = $1`, dev.ID).Scan(&n); err != nil {
		t.Fatalf("count scoped: %v", err)
	}
	if n != 1 {
		t.Fatalf("device-scoped rules after empty apply = %d, want 1 (untouched)", n)
	}
}
