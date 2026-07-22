//go:build !windows && !darwin && !linux

package lock

import "log/slog"

// NewPlatformLocker — Locker для службы на прочих Unix (freebsd и т.п.), где
// полноэкранный оверлей ещё не реализован: лог-заглушка (состояние всё равно
// персистится в lock.json, разблокировка возможна командой сервера). exe не
// используется. Windows/macOS/Linux имеют свои реализации (SessionLocker).
func NewPlatformLocker(_ string, log *slog.Logger) Locker {
	return NewLogLocker(log)
}
