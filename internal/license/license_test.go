//go:build enterprise

package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func testKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func mustIssue(t *testing.T, c Claims, priv ed25519.PrivateKey) string {
	t.Helper()
	blob, err := Issue(c, priv)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return blob
}

func TestIssueParseRoundTrip(t *testing.T) {
	pub, priv := testKeys(t)
	in := Claims{
		Licensee:  "ACME Corp",
		Edition:   "enterprise",
		Features:  []string{"sso", "scim"},
		Seats:     500,
		LicenseID: "lic-123",
		IssuedAt:  time.Now().Truncate(time.Second),
		ExpiresAt: time.Now().Add(365 * 24 * time.Hour).Truncate(time.Second),
	}
	blob := mustIssue(t, in, priv)

	lic, err := Parse(blob, pub)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if lic.Claims.Licensee != "ACME Corp" || lic.Claims.Edition != "enterprise" || lic.Claims.Seats != 500 {
		t.Fatalf("claims round-trip mismatch: %+v", lic.Claims)
	}
	if !lic.Claims.Has("sso") || !lic.Claims.Has("scim") || lic.Claims.Has("filevault") {
		t.Fatalf("feature set wrong: %v", lic.Claims.Features)
	}
	if lic.Blob() != blob {
		t.Fatalf("Blob() should return the original")
	}
}

func TestParseTamperedPayloadFails(t *testing.T) {
	pub, priv := testKeys(t)
	blob := mustIssue(t, Claims{Licensee: "ACME", Edition: "enterprise"}, priv)

	// Декодируем blob, портим байт payload, пересобираем — подпись перестаёт сходиться.
	outer, _ := base64.StdEncoding.DecodeString(blob)
	var w wire
	if err := json.Unmarshal(outer, &w); err != nil {
		t.Fatal(err)
	}
	payload, _ := base64.StdEncoding.DecodeString(w.Payload)
	payload[0] ^= 0xFF // «ACME» → мусор, подпись не пересчитана
	w.Payload = base64.StdEncoding.EncodeToString(payload)
	reOuter, _ := json.Marshal(w)
	tampered := base64.StdEncoding.EncodeToString(reOuter)

	if _, err := Parse(tampered, pub); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered payload: err = %v, want ErrSignature", err)
	}
}

func TestParseWrongKeyFails(t *testing.T) {
	_, priv := testKeys(t)
	otherPub, _ := testKeys(t)
	blob := mustIssue(t, Claims{Edition: "enterprise"}, priv)

	if _, err := Parse(blob, otherPub); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong key: err = %v, want ErrSignature", err)
	}
}

func TestParseMalformed(t *testing.T) {
	pub, _ := testKeys(t)
	for _, blob := range []string{"", "   ", "not-base64-!!!", base64.StdEncoding.EncodeToString([]byte("{not json"))} {
		if _, err := Parse(blob, pub); !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q): err = %v, want ErrMalformed", blob, err)
		}
	}
}

func TestParseNilPubKeyRejects(t *testing.T) {
	_, priv := testKeys(t)
	blob := mustIssue(t, Claims{Edition: "enterprise"}, priv)
	// Нет корня доверия → не доверяем даже валидно сформированному blob'у.
	if _, err := Parse(blob, nil); !errors.Is(err, ErrSignature) {
		t.Fatalf("nil pubkey: err = %v, want ErrSignature", err)
	}
}

func TestValidAt(t *testing.T) {
	pub, priv := testKeys(t)
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	lic := func(nb, exp time.Time) *License {
		l, err := Parse(mustIssue(t, Claims{NotBefore: nb, ExpiresAt: exp}, priv), pub)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	// Активна.
	if err := lic(now.Add(-day), now.Add(day)).ValidAt(now, 0); err != nil {
		t.Errorf("active: %v", err)
	}
	// Ещё не действует.
	if err := lic(now.Add(day), now.Add(2*day)).ValidAt(now, 0); !errors.Is(err, ErrNotYet) {
		t.Errorf("not-yet: err = %v, want ErrNotYet", err)
	}
	// Истекла.
	if err := lic(now.Add(-2*day), now.Add(-day)).ValidAt(now, 0); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: err = %v, want ErrExpired", err)
	}
	// Истекла вчера, но grace 7 дней покрывает.
	if err := lic(now.Add(-2*day), now.Add(-day)).ValidAt(now, 7*day); err != nil {
		t.Errorf("grace should keep valid: %v", err)
	}
	// grace недостаточен.
	if err := lic(now.Add(-30*day), now.Add(-10*day)).ValidAt(now, 7*day); !errors.Is(err, ErrExpired) {
		t.Errorf("grace exceeded: err = %v, want ErrExpired", err)
	}
	// Бессрочная (zero-время) — всегда валидна.
	if err := lic(time.Time{}, time.Time{}).ValidAt(now, 0); err != nil {
		t.Errorf("perpetual: %v", err)
	}
}

func TestCheckPassword(t *testing.T) {
	pub, priv := testKeys(t)
	salt, hash, err := HashPassword("s3cret-activation")
	if err != nil {
		t.Fatal(err)
	}
	withPw, _ := Parse(mustIssue(t, Claims{Edition: "enterprise", PwSalt: salt, PwHash: hash}, priv), pub)

	if err := withPw.CheckPassword("s3cret-activation"); err != nil {
		t.Errorf("correct password rejected: %v", err)
	}
	if err := withPw.CheckPassword("wrong"); !errors.Is(err, ErrPassword) {
		t.Errorf("wrong password: err = %v, want ErrPassword", err)
	}
	if err := withPw.CheckPassword(""); !errors.Is(err, ErrPassword) {
		t.Errorf("empty password against protected license: err = %v, want ErrPassword", err)
	}

	// Лицензия без пароля активации — любой пароль (в т.ч. пустой) подходит.
	noPw, _ := Parse(mustIssue(t, Claims{Edition: "enterprise"}, priv), pub)
	if err := noPw.CheckPassword(""); err != nil {
		t.Errorf("no-password license should accept empty: %v", err)
	}
	if err := noPw.CheckPassword("whatever"); err != nil {
		t.Errorf("no-password license should accept anything: %v", err)
	}
}

func TestHasWholeEdition(t *testing.T) {
	// Пустой список фич = вся редакция.
	all := Claims{Edition: "enterprise"}
	if !all.Has("sso") || !all.Has("anything") {
		t.Errorf("empty features must grant everything")
	}
	// Ограниченный список.
	limited := Claims{Features: []string{"sso"}}
	if !limited.Has("sso") || limited.Has("scim") {
		t.Errorf("limited features gate wrong: %v", limited.Features)
	}
}

func TestPubKeyResolution(t *testing.T) {
	pub, _ := testKeys(t)
	b64 := base64.StdEncoding.EncodeToString(pub)

	// env override.
	t.Setenv("ROUTINEOPS_LICENSE_PUBKEY", b64)
	got, err := PubKey()
	if err != nil || !got.Equal(pub) {
		t.Fatalf("env override: got=%v err=%v", got, err)
	}

	// Кривой ключ → ошибка.
	t.Setenv("ROUTINEOPS_LICENSE_PUBKEY", "not-base64!!!")
	if _, err := PubKey(); err == nil {
		t.Errorf("invalid base64 pubkey should error")
	}

	// Не тот размер → ошибка.
	t.Setenv("ROUTINEOPS_LICENSE_PUBKEY", base64.StdEncoding.EncodeToString([]byte("too short")))
	if _, err := PubKey(); err == nil {
		t.Errorf("wrong-size pubkey should error")
	}

	// Пусто и без вшитого дефолта → (nil, nil): лицензирование выключено, не ошибка.
	t.Setenv("ROUTINEOPS_LICENSE_PUBKEY", "")
	got, err = PubKey()
	if err != nil || got != nil {
		t.Errorf("no key: got=%v err=%v, want (nil,nil)", got, err)
	}
}
