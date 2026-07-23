package storage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Email занят другим аккаунтом (гонка) → CreateSSOUser отдаёт ErrSSOEmailTaken, а НЕ (nil,nil)
// (иначе Callback разыменовал бы nil и упал). Разные (iss,sub), но одинаковый email.
func TestCreateSSOUserEmailCollision(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	email := fmt.Sprintf("collide-%s@test.com", uniq(t))
	mustCreateUser(t, db, email) // существующий локальный аккаунт с этим email
	u, err := db.CreateSSOUser(ctx, "SSO", email, "viewer", "https://idp.example", "sub-collide")
	if u != nil {
		t.Fatalf("ожидали nil-юзера при коллизии email, got %+v", u)
	}
	if !errors.Is(err, storage.ErrSSOEmailTaken) {
		t.Fatalf("want ErrSSOEmailTaken, got %v", err)
	}
}

func TestSSOUserProvisioning(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	iss := "https://idp.example.com/realms/" + uniq(t)
	sub := "subject-" + uniq(t)
	email := fmt.Sprintf("sso-%s@test.com", uniq(t))

	// Нет такого — nil.
	if u, err := db.GetUserByOIDCIdentity(ctx, iss, sub); err != nil || u != nil {
		t.Fatalf("pre-create lookup: u=%v err=%v", u, err)
	}

	u, err := db.CreateSSOUser(ctx, "SSO User", email, "viewer", iss, sub)
	if err != nil || u == nil {
		t.Fatalf("CreateSSOUser: %v", err)
	}
	if u.AuthSource != "oidc" || u.OIDCIssuer == nil || *u.OIDCIssuer != iss || u.OIDCSubject == nil || *u.OIDCSubject != sub {
		t.Fatalf("sso fields: source=%s iss=%v sub=%v", u.AuthSource, u.OIDCIssuer, u.OIDCSubject)
	}
	if u.PasswordHash == "" {
		t.Fatal("ожидали unusable bcrypt-хеш, не пусто")
	}

	// Матч по (iss,sub).
	got, err := db.GetUserByOIDCIdentity(ctx, iss, sub)
	if err != nil || got == nil || got.ID != u.ID {
		t.Fatalf("lookup by identity: got=%v err=%v", got, err)
	}
	// GetUserByEmail тоже отдаёт auth_source.
	byEmail, _ := db.GetUserByEmail(ctx, email)
	if byEmail == nil || byEmail.AuthSource != "oidc" {
		t.Fatalf("GetUserByEmail auth_source: %v", byEmail)
	}

	// Повторный JIT той же (iss,sub) → идемпотентно тот же юзер (гонка через UNIQUE).
	again, err := db.CreateSSOUser(ctx, "SSO User", email+"x", "it_admin", iss, sub)
	if err != nil || again == nil || again.ID != u.ID {
		t.Fatalf("re-create should return same user: again=%v err=%v", again, err)
	}

	// UpdateUserRole.
	if err := db.UpdateUserRole(ctx, u.ID, "it_admin"); err != nil {
		t.Fatal(err)
	}
	if r, _ := db.GetUserByID(ctx, u.ID); r == nil || r.Role != "it_admin" {
		t.Fatalf("role after update: %v", r)
	}
}

func TestSSOFlowSingleUse(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	state := "state-" + uniq(t)

	if err := db.InsertSSOFlow(ctx, state, "nonce-abc", "verifier-xyz"); err != nil {
		t.Fatal(err)
	}
	nonce, verifier, ok, err := db.ConsumeSSOFlow(ctx, state)
	if err != nil || !ok || nonce != "nonce-abc" || verifier != "verifier-xyz" {
		t.Fatalf("consume: nonce=%s verifier=%s ok=%v err=%v", nonce, verifier, ok, err)
	}
	// Второй consume того же state → ok=false (single-use, строка удалена).
	if _, _, ok, _ := db.ConsumeSSOFlow(ctx, state); ok {
		t.Fatal("повторный consume не должен проходить")
	}
	// Неизвестный state → ok=false.
	if _, _, ok, _ := db.ConsumeSSOFlow(ctx, "nope"); ok {
		t.Fatal("неизвестный state не должен проходить")
	}
}

func TestSSOFlowExpiry(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	state := "exp-" + uniq(t)
	if err := db.InsertSSOFlow(ctx, state, "n", "v"); err != nil {
		t.Fatal(err)
	}
	// Состарим строку напрямую (за пределы TTL).
	if _, err := db.Pool().Exec(ctx,
		`UPDATE sso_auth_flows SET created_at = now() - interval '20 minutes' WHERE state = $1`, state); err != nil {
		t.Fatal(err)
	}
	// Consume протухшей → ok=false (и строка удаляется).
	if _, _, ok, _ := db.ConsumeSSOFlow(ctx, state); ok {
		t.Fatal("протухший flow не должен потребляться")
	}
	// DeleteExpiredSSOFlows на другой протухшей строке.
	state2 := "exp2-" + uniq(t)
	if err := db.InsertSSOFlow(ctx, state2, "n", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE sso_auth_flows SET created_at = now() - interval '20 minutes' WHERE state = $1`, state2); err != nil {
		t.Fatal(err)
	}
	if n, err := db.DeleteExpiredSSOFlows(ctx); err != nil || n < 1 {
		t.Fatalf("DeleteExpiredSSOFlows removed %d err=%v", n, err)
	}
}
