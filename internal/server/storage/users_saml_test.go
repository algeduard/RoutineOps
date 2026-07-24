package storage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Провижининг SAML-юзера по неизменяемой (issuer=EntityID, subject=NameID): JIT, идемпотентная
// гонка через партиал-UNIQUE users_saml_identity, auth_source='saml', unusable-пароль.
func TestSAMLUserProvisioning(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	iss := "https://idp.example.com/saml/" + uniq(t) // SAML IdP EntityID
	sub := "nameid-" + uniq(t)                       // NameID
	email := fmt.Sprintf("saml-%s@test.com", uniq(t))

	// Нет такого — nil.
	if u, err := db.GetUserBySAMLIdentity(ctx, iss, sub); err != nil || u != nil {
		t.Fatalf("pre-create lookup: u=%v err=%v", u, err)
	}

	u, err := db.CreateSAMLUser(ctx, "SAML User", email, "viewer", iss, sub)
	if err != nil || u == nil {
		t.Fatalf("CreateSAMLUser: %v", err)
	}
	if u.AuthSource != "saml" || u.OIDCIssuer == nil || *u.OIDCIssuer != iss || u.OIDCSubject == nil || *u.OIDCSubject != sub {
		t.Fatalf("saml fields: source=%s iss=%v sub=%v", u.AuthSource, u.OIDCIssuer, u.OIDCSubject)
	}
	if u.PasswordHash == "" {
		t.Fatal("ожидали unusable bcrypt-хеш, не пусто")
	}

	// Матч по (iss,sub). OIDC-lookup ту же пару НЕ находит (разные auth_source).
	got, err := db.GetUserBySAMLIdentity(ctx, iss, sub)
	if err != nil || got == nil || got.ID != u.ID {
		t.Fatalf("lookup by identity: got=%v err=%v", got, err)
	}
	if o, _ := db.GetUserByOIDCIdentity(ctx, iss, sub); o != nil {
		t.Fatal("GetUserByOIDCIdentity не должен находить SAML-юзера (изоляция по auth_source)")
	}

	// Повторный JIT той же (iss,sub) → идемпотентно тот же юзер (гонка через UNIQUE).
	again, err := db.CreateSAMLUser(ctx, "SAML User", email+"x", "it_admin", iss, sub)
	if err != nil || again == nil || again.ID != u.ID {
		t.Fatalf("re-create should return same user: again=%v err=%v", again, err)
	}
}

// Email занят ДРУГИМ аккаунтом → CreateSAMLUser отдаёт ErrSSOEmailTaken (не nil,nil): вызывающий
// трактует как отказ авто-линка. Разные (iss,sub), одинаковый email.
func TestCreateSAMLUserEmailCollision(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	email := fmt.Sprintf("saml-collide-%s@test.com", uniq(t))
	mustCreateUser(t, db, email) // существующий локальный аккаунт с этим email
	u, err := db.CreateSAMLUser(ctx, "SAML", email, "viewer", "https://idp.example", "nameid-collide")
	if u != nil {
		t.Fatalf("ожидали nil-юзера при коллизии email, got %+v", u)
	}
	if !errors.Is(err, storage.ErrSSOEmailTaken) {
		t.Fatalf("want ErrSSOEmailTaken, got %v", err)
	}
}
