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
// withTitle/withURL, заголовок окна и URL активной вкладки браузера. Пусто, если
// активного окна нет (напр. экран блокировки/рабочий стол без фокуса). Заголовок
// читается ТОЛЬКО при withTitle (capture_window_titles), URL — ТОЛЬКО при withURL
// (capture_urls) — иначе не попадают даже в память.
//
// URL собирается ЛИШЬ из известных браузеров (isBrowserProcess) и НИКОГДА из
// приватных/инкогнито-окон — та же эксклюзия по заголовку (isPrivateBrowsing), что и
// для window_title. Чтение URL best-effort: его ошибка/пустота не влияет на имя
// процесса и заголовок.
func foregroundApp(withTitle, withURL bool) (app, title, url string, err error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", "", "", nil
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "", "", "", nil
	}
	// Заголовок окна нужен и для title, и для эксклюзии приватных окон при чтении URL,
	// поэтому читаем его сырым один раз, если нужен хоть один из сборов. truncated —
	// заголовок не поместился в буфер (маркер инкогнито обычно суффикс «… (Incognito)»,
	// и обрезка могла его срезать — тогда приватность не подтвердить).
	var raw string
	var truncated bool
	if withTitle || withURL {
		raw, truncated = windowText(hwnd)
	}
	if withTitle {
		// sanitizeTitle отбрасывает приватные/инкогнито-окна и режет длину (приватность).
		title = sanitizeTitle(raw)
	}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return "", title, "", err
	}
	name, err := p.Name()
	if err != nil {
		return "", title, "", err
	}
	// URL — только для известных браузеров и НЕ из приватных/инкогнито-окон. Если заголовок
	// пришёл обрезанным, приватность по нему не подтвердить — fail-closed: URL не читаем.
	if withURL && isBrowserProcess(name) && !truncated && !isPrivateBrowsing(raw) {
		url = sanitizeURL(readBrowserURL(hwnd))
	}
	return name, title, url, nil
}

// windowText читает заголовок окна (GetWindowTextW). Возвращает текст и признак обрезки
// (заголовок длиннее буфера). Буфер большой намеренно: маркер инкогнито — суффикс, и на
// маленьком буфере длинный document.title вытолкнул бы его за обрезку, обойдя эксклюзию.
func windowText(hwnd uintptr) (string, bool) {
	var buf [2048]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return "", false
	}
	// GetWindowTextW возвращает не больше len-1 символов (место под NUL); равенство
	// len-1 означает, что заголовок, вероятно, длиннее и был усечён.
	truncated := int(n) >= len(buf)-1
	return syscall.UTF16ToString(buf[:n]), truncated
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
