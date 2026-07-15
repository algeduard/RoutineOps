//go:build !windows && !darwin

package lock

import "log/slog"

// NewPlatformLocker — Locker для службы под текущей ОС. Вне Windows и macOS
// полноэкранный оверлей пока не реализован — лог-заглушка (состояние всё равно
// персистится в lock.json). exe не используется.
func NewPlatformLocker(_ string, log *slog.Logger) Locker {
	return NewLogLocker(log)
}
