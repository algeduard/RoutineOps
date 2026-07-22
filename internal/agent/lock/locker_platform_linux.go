//go:build linux

package lock

import "log/slog"

// NewPlatformLocker — Locker для службы под Linux: SessionLocker поднимает
// полноэкранный X11-замок (internal/agent/lockui) в активной графической сессии
// пользователя. Служба живёт в session 0 без дисплея и GUI рисовать не может, но
// под root может запустить процесс в сессии залогиненного пользователя. exe —
// путь к бинарю агента (os.Executable).
func NewPlatformLocker(exe string, log *slog.Logger) Locker {
	return NewSessionLocker(exe, log)
}
