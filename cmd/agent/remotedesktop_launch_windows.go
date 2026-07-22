//go:build windows

package main

import (
	"log/slog"

	"github.com/Floodww/RoutineOps/internal/agent/winsession"
	"golang.org/x/sys/windows"
)

// launchRemoteDesktopHelper запускает процесс-хелпер удалённого рабочего стола в
// активной консольной сессии пользователя (там есть доступ к экрану/вводу; служба
// в session 0 — нет). Тот же механизм, что у lock-overlay и трея. Handle процесса
// нам не нужен (хелпер завершится сам при закрытии стрима) — закрываем ссылку,
// чтобы не течь дескрипторами; это НЕ убивает процесс.
func launchRemoteDesktopHelper(exe string, args []string, log *slog.Logger) error {
	proc, err := winsession.LaunchInActiveSession(exe, args)
	if err != nil {
		return err
	}
	_ = windows.CloseHandle(proc)
	log.Info("remote desktop: хелпер запущен в активной сессии")
	return nil
}
