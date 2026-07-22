//go:build enterprise

package license

import (
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/argon2"
)

// Параметры argon2id для пароля активации. Фиксированы (не хранятся в лицензии): меняются
// только вместе с кодом, а лицензии с этой версией ядра консистентны. 64 MiB × 3 прохода —
// разумная стоимость: активация редкая (человек вводит пароль), а брутфорс украденного
// файла становится дорогим.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB → 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// argon2Hash — argon2id(пароль, соль) с фиксированными параметрами. Детерминирован при
// одинаковых входах (нужно для сравнения при активации).
func argon2Hash(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
}

// HashPassword генерирует случайную соль и хеш пароля активации (оба base64) для вставки в
// Claims.PwSalt/PwHash. Для вендор-тулинга routineops-license при выпуске лицензии.
func HashPassword(password string) (saltB64, hashB64 string, err error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	hash := argon2Hash(password, salt)
	return base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(hash), nil
}
