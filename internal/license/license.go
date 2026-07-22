//go:build enterprise

// Package license — enterprise-лицензионное ядро (см. docs/license-core-design.md).
// Подписанный офлайн-файл лицензии: ed25519-подпись деплойера + argon2id-пароль активации.
// В open-core этого файла нет (build-tag) — там пустой doc_free.go, роут /license = 404.
package license

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Claims — содержимое лицензии (payload, который подписывается). Пустой Features означает
// «вся редакция» (см. Has). Zero-время у Not*/Expires* = «без ограничения по сроку».
type Claims struct {
	Licensee  string    `json:"licensee,omitempty"`
	Edition   string    `json:"edition,omitempty"`
	Features  []string  `json:"features,omitempty"`
	Seats     int       `json:"seats,omitempty"`
	LicenseID string    `json:"license_id,omitempty"`
	IssuedAt  time.Time `json:"issued_at"`
	NotBefore time.Time `json:"not_before"`
	ExpiresAt time.Time `json:"expires_at"`
	// Пароль активации (argon2id, соль+хеш в base64). Пусто = без пароля.
	PwSalt string `json:"pw_salt,omitempty"`
	PwHash string `json:"pw_hash,omitempty"`
}

// Has сообщает, разрешена ли фича. ПУСТОЙ список фич = вся редакция (true на любую) —
// иначе лицензия «без фич» выглядела бы как «ничего не разрешено», что противоположно
// смыслу «полная редакция». Это же поведение отражает веб (featuresLabel → «вся редакция»).
func (c *Claims) Has(feature string) bool {
	if len(c.Features) == 0 {
		return true
	}
	for _, f := range c.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// License — распарсенная и проверенная ПО ПОДПИСИ лицензия. blob хранится для персиста
// (на диск кладём ровно то, что пришло, без ре-сериализации — чтобы подпись оставалась
// верной байт-в-байт).
type License struct {
	Claims Claims
	blob   string
}

// Blob возвращает исходную base64-строку лицензии (для сохранения на rw-том).
func (l *License) Blob() string { return l.blob }

var (
	// ErrMalformed — blob не декодируется (не base64/не тот JSON). Отличать от ErrSignature:
	// «мусор в поле» ≠ «подделка валидной формы».
	ErrMalformed = errors.New("license: malformed blob")
	// ErrSignature — подпись не сходится с публичным ключом (подделка/чужой ключ/правка).
	ErrSignature = errors.New("license: invalid signature")
	// ErrNotYet — not_before ещё не наступил (частая причина — отставшие часы сервера).
	ErrNotYet = errors.New("license: not yet valid")
	// ErrExpired — срок (с учётом grace) вышел.
	ErrExpired = errors.New("license: expired")
	// ErrPassword — неверный пароль активации.
	ErrPassword = errors.New("license: wrong activation password")
)

// wire — внешняя обёртка blob'а: base64(JSON{payload, signature}). Подписываются БАЙТЫ
// payload (до base64), поэтому проверка не зависит от того, как payload перекодирован.
type wire struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// Parse декодирует blob и ПРОВЕРЯЕТ ed25519-подпись публичным ключом. Срок и пароль здесь
// НЕ проверяются (см. ValidAt/CheckPassword) — чтобы UI/сервер отличали «подпись битая»
// от «истекла» и «не активирована». pub нулевой длины = лицензирование без корня доверия
// → ErrSignature (не доверяем неподписанному).
func Parse(blob string, pub ed25519.PublicKey) (*License, error) {
	blob = strings.TrimSpace(blob)
	if blob == "" {
		return nil, ErrMalformed
	}
	outer, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, ErrMalformed
	}
	var w wire
	if err := json.Unmarshal(outer, &w); err != nil {
		return nil, ErrMalformed
	}
	payload, err := base64.StdEncoding.DecodeString(w.Payload)
	if err != nil {
		return nil, ErrMalformed
	}
	sig, err := base64.StdEncoding.DecodeString(w.Signature)
	if err != nil {
		return nil, ErrMalformed
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, payload, sig) {
		return nil, ErrSignature
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrMalformed
	}
	return &License{Claims: c, blob: blob}, nil
}

// ValidAt сообщает, действует ли лицензия на момент now с учётом grace. ErrNotYet, если
// not_before в будущем; ErrExpired, если now позже expires_at+grace. Zero-время не
// ограничивает соответствующую границу.
func (l *License) ValidAt(now time.Time, grace time.Duration) error {
	if !l.Claims.NotBefore.IsZero() && now.Before(l.Claims.NotBefore) {
		return ErrNotYet
	}
	if !l.Claims.ExpiresAt.IsZero() && now.After(l.Claims.ExpiresAt.Add(grace)) {
		return ErrExpired
	}
	return nil
}

// CheckPassword сверяет пароль активации в ПОСТОЯННОЕ время. Если у лицензии пароля нет
// (PwHash пуст) — активация без пароля, любой (в т.ч. пустой) подходит.
func (l *License) CheckPassword(password string) error {
	if l.Claims.PwHash == "" {
		return nil
	}
	salt, err := base64.StdEncoding.DecodeString(l.Claims.PwSalt)
	if err != nil {
		return ErrPassword
	}
	want, err := base64.StdEncoding.DecodeString(l.Claims.PwHash)
	if err != nil {
		return ErrPassword
	}
	got := argon2Hash(password, salt)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPassword
	}
	return nil
}

// Issue подписывает Claims приватным ключом и возвращает blob (одна base64-строка). Для
// вендор-тулинга routineops-license; на сервере не используется.
func Issue(c Claims, priv ed25519.PrivateKey) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, payload)
	outer, err := json.Marshal(wire{
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(outer), nil
}

// defaultPubKeyB64 — публичный ключ деплойера (base64), вкомпилированный на сборке через
//
//	-ldflags "-X github.com/Floodww/RoutineOps/internal/license.defaultPubKeyB64=<base64>"
//
// Пусто по умолчанию: тогда корень доверия задаёт только ROUTINEOPS_LICENSE_PUBKEY, а без
// него лицензирование фактически выключено (статус «не задана»).
var defaultPubKeyB64 string

// PubKey возвращает публичный ключ проверки лицензий: сначала override деплойера
// ROUTINEOPS_LICENSE_PUBKEY, иначе вшитый defaultPubKeyB64. (nil, nil) — корень доверия
// не задан (лицензирование выключено, не ошибка). Модель доверия — см. дизайн-док:
// env-override читает тот же оператор, что владеет сервером (свой корень доверия).
func PubKey() (ed25519.PublicKey, error) {
	b64 := strings.TrimSpace(os.Getenv("ROUTINEOPS_LICENSE_PUBKEY"))
	if b64 == "" {
		b64 = strings.TrimSpace(defaultPubKeyB64)
	}
	if b64 == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("license pubkey: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license pubkey: неверный размер %d (ожидался %d)", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
