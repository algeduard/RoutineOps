package storage_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func randKeyB64(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestMFACryptoRoundTrip(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", randKeyB64(t))
	uid := uuid.New()
	secret := []byte("12345678901234567890") // 20 байт как реальный TOTP-секрет
	blob, err := storage.EncryptTOTPSecret(uid, secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, secret) {
		t.Fatal("шифртекст содержит открытый секрет")
	}
	got, err := storage.DecryptTOTPSecret(uid, blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("round-trip: got %q want %q", got, secret)
	}
}

func TestMFACryptoWrongAAD(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", randKeyB64(t))
	blob, err := storage.EncryptTOTPSecret(uuid.New(), []byte("secret-bytes-here-20"))
	if err != nil {
		t.Fatal(err)
	}
	// Расшифровка под ДРУГИМ user_id (AAD) обязана провалиться — blob привязан к строке юзера.
	if _, err := storage.DecryptTOTPSecret(uuid.New(), blob); !errors.Is(err, storage.ErrMFADecrypt) {
		t.Fatalf("wrong AAD: want ErrMFADecrypt, got %v", err)
	}
}

func TestMFACryptoWrongKey(t *testing.T) {
	uid := uuid.New()
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", randKeyB64(t))
	blob, err := storage.EncryptTOTPSecret(uid, []byte("secret-bytes-here-20"))
	if err != nil {
		t.Fatal(err)
	}
	// Ротация/подмена ключа → расшифровка провалилась, но не паникой и не 500.
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", randKeyB64(t))
	if _, err := storage.DecryptTOTPSecret(uid, blob); !errors.Is(err, storage.ErrMFADecrypt) {
		t.Fatalf("wrong key: want ErrMFADecrypt, got %v", err)
	}
}

func TestMFACryptoCorruptBlob(t *testing.T) {
	uid := uuid.New()
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", randKeyB64(t))
	blob, err := storage.EncryptTOTPSecret(uid, []byte("secret-bytes-here-20"))
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)-1] ^= 0xff // ломаем тег
	if _, err := storage.DecryptTOTPSecret(uid, blob); !errors.Is(err, storage.ErrMFADecrypt) {
		t.Fatalf("corrupt blob: want ErrMFADecrypt, got %v", err)
	}
}

func TestMFACryptoKeyStatus(t *testing.T) {
	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", "")
	if _, err := storage.EncryptTOTPSecret(uuid.New(), []byte("x")); !errors.Is(err, storage.ErrMFAKeyMissing) {
		t.Fatalf("missing key: want ErrMFAKeyMissing, got %v", err)
	}
	if ok, err := storage.MFAEncKeyStatus(); ok || !errors.Is(err, storage.ErrMFAKeyMissing) {
		t.Fatalf("status missing: got ok=%v err=%v", ok, err)
	}

	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", base64.StdEncoding.EncodeToString([]byte("too-short")))
	if _, err := storage.EncryptTOTPSecret(uuid.New(), []byte("x")); !errors.Is(err, storage.ErrMFAKeyInvalid) {
		t.Fatalf("short key: want ErrMFAKeyInvalid, got %v", err)
	}
	if ok, err := storage.MFAEncKeyStatus(); ok || !errors.Is(err, storage.ErrMFAKeyInvalid) {
		t.Fatalf("status invalid: got ok=%v err=%v", ok, err)
	}

	t.Setenv("ROUTINEOPS_MFA_ENC_KEY", randKeyB64(t))
	if ok, err := storage.MFAEncKeyStatus(); !ok || err != nil {
		t.Fatalf("status valid: got ok=%v err=%v", ok, err)
	}
}
