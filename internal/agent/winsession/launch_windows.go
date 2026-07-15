//go:build windows

package winsession

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrNoActiveSession — сейчас никто не залогинен (нет интерактивной консольной
// сессии). Это не ошибка вызова: процесс (лок-оверлей/трей) поднимется, как только
// пользователь войдёт (для трея это делает HKLM\…\Run, для лока — фоновый цикл локера).
var ErrNoActiveSession = errors.New("нет активной консольной сессии")

// LaunchInActiveSession запускает "<exe> args…" в активной консольной сессии под
// токеном залогиненного пользователя и возвращает хэндл запущенного процесса.
// Вызывающий обязан либо закрыть хэндл (windows.CloseHandle) сразу — «запустить и
// забыть», либо использовать его для слежения за жизнью процесса. Применяется
// службой (session 0, LocalSystem), у которой нет рабочего стола: GUI рисует
// отдельный процесс в сессии пользователя.
func LaunchInActiveSession(exe string, args []string) (windows.Handle, error) {
	sid := windows.WTSGetActiveConsoleSessionId()
	if sid == 0xFFFFFFFF {
		return 0, ErrNoActiveSession
	}
	var userTok windows.Token
	if err := windows.WTSQueryUserToken(sid, &userTok); err != nil {
		return 0, ErrNoActiveSession // нет залогиненного пользователя в этой сессии
	}
	defer userTok.Close()

	var dupTok windows.Token
	if err := windows.DuplicateTokenEx(userTok, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &dupTok); err != nil {
		return 0, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer dupTok.Close()

	// Блок окружения пользователя (не критично — при сбое стартуем без него).
	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, dupTok, false); err != nil {
		env = nil
	}
	defer func() {
		if env != nil {
			windows.DestroyEnvironmentBlock(env)
		}
	}()

	cmdLine, err := windows.UTF16PtrFromString(buildCmdLine(exe, args))
	if err != nil {
		return 0, err
	}
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation

	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NO_WINDOW)
	if err := windows.CreateProcessAsUser(dupTok, nil, cmdLine, nil, nil, false,
		flags, env, nil, &si, &pi); err != nil {
		return 0, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, nil
}
