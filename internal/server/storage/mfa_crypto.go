package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"strings"

	"github.com/google/uuid"
)

// Крипта TOTP-секрета живёт в storage-слое (как auditHMACKey в audit.go): ключ читается
// из env деплоя, НЕ из БД. Секрет TOTP приходится РАСшифровывать для проверки кода, поэтому
// это симметричное шифрование (AES-256-GCM, AEAD), а не хеш. pgcrypto не умеет AEAD, а
// секрет нельзя хранить открыто в дампе БД — отсюда шифрование в приложении ключом вне БД.
var (
	// ErrMFAKeyMissing — ROUTINEOPS_MFA_ENC_KEY не задан. Enroll/confirm невозможны (503),
	// но юзеры без MFA логинятся как обычно, а юзеры с MFA уходят на recovery-код.
	ErrMFAKeyMissing = errors.New("mfa encryption key not configured")
	// ErrMFAKeyInvalid — ключ задан, но не декодируется в ровно 32 байта (плохой конфиг).
	ErrMFAKeyInvalid = errors.New("mfa encryption key invalid (need 32 bytes base64)")
	// ErrMFADecrypt — GCM Open провалился: неверный/ротированный ключ ИЛИ повреждённый blob
	// ИЛИ blob привязан к другому user_id (AAD). Шаг-2 логина трактует это как «ключ
	// недоступен» → 401 «use a recovery code», без 500 и без расхода challenge.
	ErrMFADecrypt = errors.New("mfa secret decrypt failed")
)

// mfaKeyVersion — версия ключа в первом байте blob. Зарезервировано под ротацию ключа
// без миграции: при чтении по байту выбирается соответствующий ключ (пока только v1).
const mfaKeyVersion byte = 0x01

// mfaEncKey читает и валидирует ROUTINEOPS_MFA_ENC_KEY (base64 от 32 байт), по образцу
// auditHMACKey(). Пусто → ErrMFAKeyMissing; не 32 байта → ErrMFAKeyInvalid.
func mfaEncKey() ([]byte, error) {
	v := strings.TrimSpace(os.Getenv("ROUTINEOPS_MFA_ENC_KEY"))
	if v == "" {
		return nil, ErrMFAKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(key) != 32 {
		return nil, ErrMFAKeyInvalid
	}
	return key, nil
}

// MFAEncKeyStatus — для стартовой проверки в main.go. Возвращает (configured, err):
// err==ErrMFAKeyInvalid → отказать в старте; err==ErrMFAKeyMissing → ключа нет (ок, если
// нет MFA-юзеров); err==nil → ключ валиден.
func MFAEncKeyStatus() (bool, error) {
	_, err := mfaEncKey()
	if errors.Is(err, ErrMFAKeyMissing) {
		return false, err
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// EncryptTOTPSecret шифрует секрет TOTP для хранения в users.totp_secret_enc.
// Blob = mfaKeyVersion(1) || nonce(12) || ciphertext+tag. AAD = канонические 16 байт
// user_id (userID[:]) — привязывает шифртекст к строке юзера: перестановка blob между
// юзерами в дампе БД даёт ErrMFADecrypt.
func EncryptTOTPSecret(userID uuid.UUID, secret []byte) ([]byte, error) {
	key, err := mfaEncKey()
	if err != nil {
		return nil, err
	}
	gcm, err := newMFAGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, secret, userID[:])
	blob := make([]byte, 0, 1+len(nonce)+len(ct))
	blob = append(blob, mfaKeyVersion)
	blob = append(blob, nonce...)
	blob = append(blob, ct...)
	return blob, nil
}

// DecryptTOTPSecret расшифровывает blob из users.totp_secret_enc. Любой провал (неверный
// ключ, порча, чужой AAD) → ErrMFADecrypt (никогда не паникует).
func DecryptTOTPSecret(userID uuid.UUID, blob []byte) ([]byte, error) {
	key, err := mfaEncKey()
	if err != nil {
		return nil, err
	}
	gcm, err := newMFAGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(blob) < 1+ns+gcm.Overhead() || blob[0] != mfaKeyVersion {
		return nil, ErrMFADecrypt
	}
	nonce := blob[1 : 1+ns]
	ct := blob[1+ns:]
	pt, err := gcm.Open(nil, nonce, ct, userID[:])
	if err != nil {
		return nil, ErrMFADecrypt
	}
	return pt, nil
}

func newMFAGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
