package storage_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMFAStorageLifecycle(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mustCreateUser(t, db, fmt.Sprintf("mfa-%s@test.com", uniq(t)))

	// Свежий юзер: MFA выключена, полей нет.
	enabled, secret, lastStep, confirmedAt, err := db.GetUserMFA(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if enabled || secret != nil || lastStep != 0 || confirmedAt != nil {
		t.Fatalf("fresh user: enabled=%v secret=%v step=%d confirmed=%v", enabled, secret, lastStep, confirmedAt)
	}

	// Pending-секрет не включает MFA.
	if err := db.SetUserTOTPPending(ctx, u.ID, []byte("blob-bytes")); err != nil {
		t.Fatal(err)
	}
	enabled, secret, _, _, _ = db.GetUserMFA(ctx, u.ID)
	if enabled {
		t.Fatal("pending не должен включать MFA")
	}
	if string(secret) != "blob-bytes" {
		t.Fatalf("secret=%q", secret)
	}

	// Confirm: включает MFA, ставит last_step, кладёт recovery-коды.
	if err := db.ConfirmUserTOTP(ctx, u.ID, 100, []string{"h1", "h2", "h3"}); err != nil {
		t.Fatal(err)
	}
	enabled, _, lastStep, confirmedAt, _ = db.GetUserMFA(ctx, u.ID)
	if !enabled || lastStep != 100 || confirmedAt == nil {
		t.Fatalf("after confirm: enabled=%v step=%d confirmed=%v", enabled, lastStep, confirmedAt)
	}
	if n, _ := db.CountRecoveryCodes(ctx, u.ID); n != 3 {
		t.Fatalf("recovery count=%d want 3", n)
	}

	// AdvanceTOTPLastStep — CAS: строго больше проходит, повтор/старое отвергается.
	if adv, _ := db.AdvanceTOTPLastStep(ctx, u.ID, 101); !adv {
		t.Fatal("101 должен продвинуть")
	}
	if adv, _ := db.AdvanceTOTPLastStep(ctx, u.ID, 101); adv {
		t.Fatal("повтор 101 НЕ должен продвинуть (replay)")
	}
	if adv, _ := db.AdvanceTOTPLastStep(ctx, u.ID, 50); adv {
		t.Fatal("старый counter НЕ должен продвинуть")
	}
	if _, _, lastStep, _, _ = db.GetUserMFA(ctx, u.ID); lastStep != 101 {
		t.Fatalf("lastStep=%d want 101", lastStep)
	}

	// RecoveryCodeExists — проверка без расхода.
	if ok, _ := db.RecoveryCodeExists(ctx, u.ID, "h1"); !ok {
		t.Fatal("h1 должен существовать до расхода")
	}
	if ok, _ := db.RecoveryCodeExists(ctx, u.ID, "nope"); ok {
		t.Fatal("несуществующий код не должен находиться")
	}
	// ConsumeRecoveryCode — одноразово.
	if ok, _ := db.ConsumeRecoveryCode(ctx, u.ID, "h1"); !ok {
		t.Fatal("h1 должен погаситься")
	}
	if ok, _ := db.RecoveryCodeExists(ctx, u.ID, "h1"); ok {
		t.Fatal("h1 после расхода не должен находиться")
	}
	if ok, _ := db.ConsumeRecoveryCode(ctx, u.ID, "h1"); ok {
		t.Fatal("h1 второй раз НЕ должен")
	}
	if n, _ := db.CountRecoveryCodes(ctx, u.ID); n != 2 {
		t.Fatalf("count after consume=%d want 2", n)
	}
	if n, _ := db.CountEnabledMFAUsers(ctx); n < 1 {
		t.Fatalf("enabled MFA users=%d want >=1", n)
	}

	// ReplaceRecoveryCodes — старые исчезают.
	if err := db.ReplaceRecoveryCodes(ctx, u.ID, []string{"n1", "n2"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountRecoveryCodes(ctx, u.ID); n != 2 {
		t.Fatalf("after replace=%d want 2", n)
	}
	if ok, _ := db.ConsumeRecoveryCode(ctx, u.ID, "h2"); ok {
		t.Fatal("старый h2 после replace НЕ должен гаситься")
	}

	// Disable — обнуляет всё.
	if err := db.DisableUserTOTP(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	enabled, secret, lastStep, confirmedAt, _ = db.GetUserMFA(ctx, u.ID)
	if enabled || secret != nil || lastStep != 0 || confirmedAt != nil {
		t.Fatalf("after disable: enabled=%v secret=%v step=%d confirmed=%v", enabled, secret, lastStep, confirmedAt)
	}
	if n, _ := db.CountRecoveryCodes(ctx, u.ID); n != 0 {
		t.Fatalf("recovery after disable=%d want 0", n)
	}
}

func TestMFAChallengeLifecycle(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	u := mustCreateUser(t, db, fmt.Sprintf("chal-%s@test.com", uniq(t)))

	// Create + lookup.
	if err := db.CreateMFAChallenge(ctx, u.ID, "hash-A", 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	uid, ok, err := db.LookupMFAChallenge(ctx, "hash-A")
	if err != nil || !ok || uid != u.ID {
		t.Fatalf("lookup: uid=%s ok=%v err=%v", uid, ok, err)
	}

	// Consume атомарно один раз.
	uid, ok, _ = db.MarkMFAChallengeConsumed(ctx, "hash-A")
	if !ok || uid != u.ID {
		t.Fatalf("consume: uid=%s ok=%v", uid, ok)
	}
	if _, ok, _ := db.MarkMFAChallengeConsumed(ctx, "hash-A"); ok {
		t.Fatal("повторный consume НЕ должен")
	}
	if _, ok, _ := db.LookupMFAChallenge(ctx, "hash-A"); ok {
		t.Fatal("lookup израсходованного НЕ должен")
	}
	if _, ok, _ := db.LookupMFAChallenge(ctx, "nope"); ok {
		t.Fatal("lookup неизвестного НЕ должен")
	}

	// Истёкший challenge не находится и удаляется чисткой.
	if err := db.CreateMFAChallenge(ctx, u.ID, "hash-exp", -1*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.LookupMFAChallenge(ctx, "hash-exp"); ok {
		t.Fatal("истёкший lookup НЕ должен")
	}
	n, err := db.DeleteExpiredMFAChallenges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("DeleteExpiredMFAChallenges removed %d want >=1", n)
	}
}
