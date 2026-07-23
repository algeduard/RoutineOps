package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

// Ручная реализация RFC 6238 (TOTP) / RFC 4226 (HOTP) на stdlib — без новых Go-зависимостей
// (позиция кодбазы: крипта делается напрямую). Параметры фиксированы SHA1/6 цифр/30 c —
// дефолты Google Authenticator, Authy, 1Password, FreeOTP. Секрет шифруется отдельно в
// storage/mfa_crypto.go; здесь только сам алгоритм над уже расшифрованным секретом.

const (
	totpPeriod  = 30 // длина окна, секунд
	totpDigits  = 6
	totpSecretN = 20 // 160 бит — стандарт для HMAC-SHA1
	totpIssuer  = "RoutineOps"
)

// generateTOTPSecret — 20 случайных байт секрета (crypto/rand).
func generateTOTPSecret() ([]byte, error) {
	b := make([]byte, totpSecretN)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// totpSecretBase32 — представление секрета для otpauth URI и ручного ввода (верхний
// регистр, без паддинга — как ждут authenticator-приложения).
func totpSecretBase32(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// hotp вычисляет 6-значный HOTP(secret, counter) по RFC 4226 (dynamic truncation).
func hotp(secret []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off]&0x7f) << 24) |
		(uint32(sum[off+1]) << 16) |
		(uint32(sum[off+2]) << 8) |
		uint32(sum[off+3])
	return fmt.Sprintf("%0*d", totpDigits, bin%1_000_000)
}

// VerifyTOTP проверяет 6-значный код против секрета с дрейфом ±1 шаг (±30 c — компенсация
// рассинхрона часов). Возвращает СОВПАВШИЙ counter (для replay-guard: вызывающий обязан
// принять код только если matchedCounter > totp_last_step, затем сдвинуть last_step на него).
// Сравнение — constant-time. len(code)!=6 → сразу мимо.
func VerifyTOTP(secret []byte, code string, now time.Time) (matchedCounter int64, ok bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for _, c := range []int64{cur - 1, cur, cur + 1} {
		if c < 0 {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(hotp(secret, uint64(c))), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}

// otpauthURI собирает otpauth://totp/-ссылку для QR/ручного ввода. account — email юзера.
func otpauthURI(account, secretB32 string) string {
	label := url.PathEscape(totpIssuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secretB32)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
