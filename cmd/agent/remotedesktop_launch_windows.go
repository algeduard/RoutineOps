//go:build windows

package main

import (
	"log/slog"
	"os/exec"

	"github.com/Floodww/RoutineOps/internal/agent/service"
	"github.com/Floodww/RoutineOps/internal/agent/winsession"
	"golang.org/x/sys/windows"
)

// launchRemoteDesktopHelper запускает процесс-хелпер удалённого рабочего стола там,
// где есть доступ к экрану/вводу — в активной интерактивной сессии пользователя.
//
// Под службой (session 0, рабочего стола нет) поднимаем хелпер в активной
// консольной сессии через CreateProcessAsUser (winsession) — тот же механизм, что
// у lock-overlay и трея. При интерактивном запуске (dev/консоль: `agent run`) сам
// агент уже в сессии пользователя, поэтому хелпер стартует обычным дочерним
// процессом — winsession там недоступен (WTSQueryUserToken требует SYSTEM).
func launchRemoteDesktopHelper(exe string, args []string, log *slog.Logger) error {
	if service.RunningAsService() {
		proc, err := winsession.LaunchInActiveSession(exe, args)
		if err != nil {
			return err
		}
		// Handle процесса нам не нужен (хелпер завершится сам при закрытии стрима) —
		// закрываем ссылку, чтобы не течь дескрипторами; это НЕ убивает процесс.
		_ = windows.CloseHandle(proc)
		log.Info("remote desktop: хелпер запущен в активной сессии (служба)")
		return nil
	}
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	log.Info("remote desktop: хелпер запущен дочерним процессом (интерактивно)")
	return nil
}
