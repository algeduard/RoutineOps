//go:build windows

package telemetry

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/shirou/gopsutil/v4/process"
)

// Foreground/idle-детекция для app-usage (Windows). Стиль вызовов WinAPI — через
// syscall.NewLazyDLL, как в internal/agent/lockui/lockui_windows.go. Имя
// foreground-процесса собирается всегда (при включённом app-usage); заголовок окна —
// ТОЛЬКО при отдельном флаге withTitle (privacy, дизайн §4).
var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
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

// foregroundApp возвращает имя процесса активного (foreground) окна и, при
// withTitle, заголовок этого окна. Пусто, если активного окна нет (напр. экран
// блокировки/рабочий стол без фокуса). Заголовок читается ТОЛЬКО при withTitle —
// когда it_admin включил capture_window_titles (иначе он не попадает даже в память).
func foregroundApp(withTitle bool) (app, title string, err error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", "", nil
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "", "", nil
	}
	if withTitle {
		// sanitizeTitle отбрасывает приватные/инкогнито-окна и режет длину (приватность).
		title = sanitizeTitle(windowText(hwnd))
	}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return "", title, err
	}
	name, err := p.Name()
	if err != nil {
		return "", title, err
	}
	return name, title, nil
}

// windowText читает заголовок окна (GetWindowTextW). Пусто, если заголовка нет.
func windowText(hwnd uintptr) string {
	var buf [512]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
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
