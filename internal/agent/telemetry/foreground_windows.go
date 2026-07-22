//go:build windows

package telemetry

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/shirou/gopsutil/v4/process"
)

// Foreground/idle-детекция для app-usage (Windows). Стиль вызовов WinAPI — через
// syscall.NewLazyDLL, как в internal/agent/lockui/lockui_windows.go. Собирается
// ТОЛЬКО имя foreground-процесса (не заголовок окна) — приватность (дизайн §4).
var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetLastInputInfo         = user32.NewProc("GetLastInputInfo")

	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGetTickCount = kernel32.NewProc("GetTickCount")
)

// lastInputInfo — LASTINPUTINFO для GetLastInputInfo. dwTime — момент последнего
// ввода в тиках GetTickCount.
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// appUsageSupported — app-usage реализован на этой платформе.
func appUsageSupported() bool { return true }

// foregroundApp возвращает имя процесса активного (foreground) окна. Пусто, если
// активного окна нет (напр. экран блокировки/рабочий стол без фокуса).
func foregroundApp() (string, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", nil
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "", nil
	}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return "", err
	}
	name, err := p.Name()
	if err != nil {
		return "", err
	}
	return name, nil
}

// idleDuration возвращает, сколько прошло с последнего ввода (клавиатура/мышь).
// GetTickCount и dwTime — 32-битные и переполняются вместе каждые ~49 суток;
// вычитание в uint32 корректно обрабатывает перенос, пока idle < 49 суток.
func idleDuration() (time.Duration, error) {
	info := lastInputInfo{}
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, err := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, err
	}
	tick, _, _ := procGetTickCount.Call()
	ms := uint32(tick) - info.dwTime
	return time.Duration(ms) * time.Millisecond, nil
}
