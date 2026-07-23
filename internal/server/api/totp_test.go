package api

import (
	"bytes"
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B — эталонные векторы для SHA1, seed ASCII "12345678901234567890".
// В стандарте коды 8-значные; берём младшие 6 (bin % 10^6) — это и есть наш totpDigits.
func TestTOTPRFC6238Vectors(t *testing.T) {
	seed := []byte("12345678901234567890")
	cases := []struct {
		t    int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		counter := c.t / totpPeriod
		if got := hotp(seed, uint64(counter)); got != c.code {
			t.Errorf("hotp at T=%d: got %s want %s", c.t, got, c.code)
		}
		mc, ok := VerifyTOTP(seed, c.code, time.Unix(c.t, 0))
		if !ok || mc != counter {
			t.Errorf("VerifyTOTP at T=%d: mc=%d ok=%v want counter=%d", c.t, mc, ok, counter)
		}
	}
}

func TestTOTPDrift(t *testing.T) {
	seed := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	cur := int64(1111111111) / totpPeriod

	// Клиент опережает на один шаг — принимается, matched = cur+1.
	if mc, ok := VerifyTOTP(seed, hotp(seed, uint64(cur+1)), now); !ok || mc != cur+1 {
		t.Errorf("ahead drift: mc=%d ok=%v", mc, ok)
	}
	// Клиент отстаёт на один шаг — принимается, matched = cur-1.
	if mc, ok := VerifyTOTP(seed, hotp(seed, uint64(cur-1)), now); !ok || mc != cur-1 {
		t.Errorf("behind drift: mc=%d ok=%v", mc, ok)
	}
	// Код 60 c назад (cur-2) — за окном, отвергается.
	if _, ok := VerifyTOTP(seed, hotp(seed, uint64(cur-2)), now); ok {
		t.Error("cur-2 должен быть отвергнут")
	}
	// Неверная длина — сразу мимо.
	if _, ok := VerifyTOTP(seed, "12345", now); ok {
		t.Error("5-значный код должен быть отвергнут")
	}
}

func TestTOTPSecretAndURI(t *testing.T) {
	s, err := generateTOTPSecret()
	if err != nil || len(s) != totpSecretN {
		t.Fatalf("secret: len=%d err=%v", len(s), err)
	}
	b32 := totpSecretBase32(s)
	dec, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil || !bytes.Equal(dec, s) {
		t.Fatalf("base32 round-trip failed: err=%v", err)
	}
	uri := otpauthURI("user@example.com", b32)
	if !strings.HasPrefix(uri, "otpauth://totp/RoutineOps:") {
		t.Errorf("uri prefix wrong: %s", uri)
	}
	for _, want := range []string{"secret=" + b32, "issuer=RoutineOps", "algorithm=SHA1", "digits=6", "period=30"} {
		if !strings.Contains(uri, want) {
			t.Errorf("uri missing %q: %s", want, uri)
		}
	}
}
