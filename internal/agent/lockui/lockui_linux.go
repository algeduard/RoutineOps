//go:build linux

// Полноэкранный замок блокировки устройства для Linux (X11). Паритет с Windows
// (lxn/walk) и macOS (Cocoa): окно поверх всех с полем пароля, оффлайн-сверка по
// bcrypt-хешу из lock.json. Реализовано на чистом Go через github.com/jezek/xgb —
// без cgo и без тяжёлых GUI-тулкитов: создаётся override-redirect окно во весь
// экран, клавиатура и указатель перехватываются (GrabKeyboard/GrabPointer), пока
// не введён верный пароль.
//
// Разблокировка — как на macOS: юзер-сессия НЕ может перезаписать lock.json
// напрямую (его владелец — демон под root, а каталог /tmp со sticky-битом не даёт
// переименовать чужой файл), поэтому при верном пароле кладём запрос через
// lock.WriteUnlockRequest — демон сверит пароль сам и снимет блокировку
// авторитетно (Manager.processUnlockRequests).
//
// Ограничения (следующие шаги):
//   - Только X11. Под чистым Wayland X-сервера нет; на большинстве Wayland-сессий
//     работает Xwayland, но override-redirect и глобальный grab там ненадёжны —
//     полноценный Wayland-замок отдельная задача (см. docs/ROADMAP.md).
//   - Ctrl+Alt+F<n> (переключение VT) из клиента X перехватить нельзя — это делает
//     ядро/logind. Как и у Windows-замка (там — Ctrl+Alt+Del), это ограничение
//     платформы, а не обход самого пароля.
package lockui

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/crypto/bcrypt"

	"github.com/Floodww/RoutineOps/internal/agent/lock"
)

// modShift / modLock — биты поля State в KeyPressEvent (X11: Shift=1, Lock=2).
const (
	modShift = 1
	modLock  = 2
)

// Run показывает X11-замок, если устройство заблокировано (по statePath). Блокирует
// поток до верного пароля или до снятия блокировки сервером. Запускается как
// отдельный процесс `agent lock-screen` в графической сессии пользователя (служба
// из session 0 GUI рисовать не может — её поднимает SessionLocker, см. пакет lock).
func Run(statePath string, log *slog.Logger) {
	st, err := lock.ReadState(statePath)
	if err != nil || !st.Locked {
		return // не заблокировано — показывать нечего
	}
	reason := st.Reason
	if reason == "" {
		reason = "Устройство заблокировано администратором. Обратитесь в IT для разблокировки."
	}

	ov, err := newX11Overlay(reason, log)
	if err != nil {
		// Нет X-дисплея / не смогли создать окно — состояние всё равно persist'ится
		// в lock.json, разблокировка возможна командой сервера. Wayland-сессия без
		// Xwayland попадёт сюда же.
		log.Warn("lock-screen: X11-замок не поднят (нужен X11-дисплей; Wayland — следующий шаг)", slog.Any("error", err))
		return
	}
	defer ov.shutdown()
	ov.loop(statePath)
}

// x11Overlay — состояние одного показанного замка.
type x11Overlay struct {
	conn   *xgb.Conn
	screen *xproto.ScreenInfo
	win    xproto.Window
	gcText xproto.Gcontext // белый текст на чёрном
	gcErr  xproto.Gcontext // красный текст (ошибки)
	log    *slog.Logger

	reason      string
	unicodeFont bool // true → рисуем ImageText16 (кириллица), false → ImageText8 (Latin-1)
	charW       int  // приблизительная ширина знака (для горизонтального центрирования)

	// Раскладка клавиатуры (GetKeyboardMapping): плоский массив keysym'ов, начиная
	// с minKeycode, по keysymsPerKeycode на каждый keycode.
	keymap            []xproto.Keysym
	minKeycode        xproto.Keycode
	keysymsPerKeycode int

	password []rune // введённые символы (для маскирования и сборки строки)
	errMsg   string // сообщение под полем ввода ("Неверный пароль" и т.п.)

	stop      chan struct{}
	stopOnce  sync.Once
	closeOnce sync.Once
}

// newX11Overlay открывает соединение с X-сервером и создаёт полноэкранное
// override-redirect окно с захватом клавиатуры и указателя.
func newX11Overlay(reason string, log *slog.Logger) (*x11Overlay, error) {
	conn, err := xgb.NewConn() // читает $DISPLAY и $XAUTHORITY
	if err != nil {
		return nil, err
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)

	win, err := xproto.NewWindowId(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// override-redirect (512) убирает окно из-под управления WM: без рамки, поверх
	// всех, не перехватывается тайлингом/таскбаром. Значения — в порядке возрастания
	// битов маски: BackPixel(2), OverrideRedirect(512), EventMask(2048).
	mask := uint32(xproto.CwBackPixel | xproto.CwOverrideRedirect | xproto.CwEventMask)
	values := []uint32{
		screen.BlackPixel,
		1, // override-redirect = true
		xproto.EventMaskExposure | xproto.EventMaskKeyPress,
	}
	if err := xproto.CreateWindowChecked(conn, screen.RootDepth, win, screen.Root,
		0, 0, screen.WidthInPixels, screen.HeightInPixels, 0,
		xproto.WindowClassInputOutput, screen.RootVisual, mask, values).Check(); err != nil {
		conn.Close()
		return nil, err
	}

	o := &x11Overlay{
		conn:   conn,
		screen: screen,
		win:    win,
		log:    log,
		reason: reason,
		stop:   make(chan struct{}),
	}

	if err := o.setupFonts(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := o.loadKeymap(setup); err != nil {
		conn.Close()
		return nil, err
	}

	xproto.MapWindow(conn, win)
	o.raise()
	o.grabKeyboard()
	// Захват указателя, чтобы клики не уходили под окном (CursorNone=0, ловим
	// события только на нашем окне — маску можно оставить нулевой).
	xproto.GrabPointer(conn, false, win, 0, xproto.GrabModeAsync, xproto.GrabModeAsync,
		xproto.WindowNone, xproto.CursorNone, xproto.TimeCurrentTime)
	xproto.SetInputFocus(conn, xproto.InputFocusPointerRoot, win, xproto.TimeCurrentTime)
	return o, nil
}

// setupFonts подбирает шрифт и создаёт графические контексты для текста и ошибок.
// Предпочтителен Unicode-шрифт (iso10646-1), чтобы отрисовать кириллический reason;
// если его нет — фолбэк на Latin-1 «fixed» (кириллица станет '?').
func (o *x11Overlay) setupFonts() error {
	fid, unicode, charW, err := openBestFont(o.conn)
	if err != nil {
		return err
	}
	o.unicodeFont = unicode
	o.charW = charW

	textGC, err := xproto.NewGcontextId(o.conn)
	if err != nil {
		return err
	}
	// Порядок значений — по возрастанию битов: Foreground(4), Background(8), Font(16384).
	xproto.CreateGC(o.conn, textGC, xproto.Drawable(o.win),
		xproto.GcForeground|xproto.GcBackground|xproto.GcFont,
		[]uint32{o.screen.WhitePixel, o.screen.BlackPixel, uint32(fid)})
	o.gcText = textGC

	errGC, err := xproto.NewGcontextId(o.conn)
	if err != nil {
		return err
	}
	red := o.allocPixel(0xffff, 0x3000, 0x3000, o.screen.WhitePixel)
	xproto.CreateGC(o.conn, errGC, xproto.Drawable(o.win),
		xproto.GcForeground|xproto.GcBackground|xproto.GcFont,
		[]uint32{red, o.screen.BlackPixel, uint32(fid)})
	o.gcErr = errGC
	return nil
}

// allocPixel выделяет цвет в палитре экрана; при ошибке возвращает fallback.
func (o *x11Overlay) allocPixel(r, g, b uint16, fallback uint32) uint32 {
	reply, err := xproto.AllocColor(o.conn, o.screen.DefaultColormap, r, g, b).Reply()
	if err != nil || reply == nil {
		return fallback
	}
	return reply.Pixel
}

// openBestFont ищет Unicode-шрифт фиксированной ширины (кириллица) и открывает его;
// при неудаче открывает Latin-1 «fixed». Возвращает id шрифта, признак Unicode и
// приблизительную ширину знака (для центрирования; точные метрики MVP не считает).
func openBestFont(conn *xgb.Conn) (fid xproto.Font, unicode bool, charW int, err error) {
	// ListFonts матчит доступные шрифты по шаблону (OpenFont ждёт точное имя).
	// «-misc-fixed-…-iso10646-1» есть в стандартной поставке xorg-fonts.
	for _, pat := range []string{
		"-misc-fixed-medium-r-normal--20-*-*-*-*-*-iso10646-1",
		"-*-*-medium-r-normal--20-*-*-*-*-*-iso10646-1",
		"-*-*-medium-r-normal--18-*-*-*-*-*-iso10646-1",
	} {
		if name := firstFontMatch(conn, pat); name != "" {
			if f, e := openNamedFont(conn, name); e == nil {
				return f, true, 10, nil
			}
		}
	}
	// Фолбэк: 8-битный «fixed» (ISO8859-1) — есть почти всегда.
	f, e := openNamedFont(conn, "fixed")
	if e != nil {
		return 0, false, 0, fmt.Errorf("не удалось открыть ни один X-шрифт: %w", e)
	}
	return f, false, 7, nil
}

// firstFontMatch возвращает имя первого шрифта, подошедшего под шаблон ("" — нет).
func firstFontMatch(conn *xgb.Conn, pattern string) string {
	reply, err := xproto.ListFonts(conn, 8, uint16(len(pattern)), pattern).Reply()
	if err != nil || reply == nil || len(reply.Names) == 0 {
		return ""
	}
	return reply.Names[0].Name
}

// openNamedFont открывает шрифт по точному имени.
func openNamedFont(conn *xgb.Conn, name string) (xproto.Font, error) {
	fid, err := xproto.NewFontId(conn)
	if err != nil {
		return 0, err
	}
	if err := xproto.OpenFontChecked(conn, fid, uint16(len(name)), name).Check(); err != nil {
		return 0, err
	}
	return fid, nil
}

// loadKeymap загружает раскладку клавиатуры сервера (keycode → keysym'ы).
func (o *x11Overlay) loadKeymap(setup *xproto.SetupInfo) error {
	o.minKeycode = setup.MinKeycode
	count := int(setup.MaxKeycode) - int(setup.MinKeycode) + 1
	if count <= 0 || count > 255 {
		return fmt.Errorf("некорректный диапазон keycode: %d..%d", setup.MinKeycode, setup.MaxKeycode)
	}
	reply, err := xproto.GetKeyboardMapping(o.conn, o.minKeycode, byte(count)).Reply()
	if err != nil {
		return err
	}
	if reply.KeysymsPerKeycode == 0 {
		return fmt.Errorf("пустая раскладка клавиатуры")
	}
	o.keymap = reply.Keysyms
	o.keysymsPerKeycode = int(reply.KeysymsPerKeycode)
	return nil
}

// lookupKeysym переводит keycode+модификаторы события в keysym по загруженной раскладке.
func (o *x11Overlay) lookupKeysym(kc xproto.Keycode, state uint16) uint32 {
	if o.keysymsPerKeycode == 0 || kc < o.minKeycode {
		return 0
	}
	idx := (int(kc) - int(o.minKeycode)) * o.keysymsPerKeycode
	if idx < 0 || idx >= len(o.keymap) {
		return 0
	}
	unshifted := uint32(o.keymap[idx])
	shifted := unshifted
	if o.keysymsPerKeycode > 1 {
		shifted = uint32(o.keymap[idx+1])
	}
	return effectiveKeysym(unshifted, shifted, state&modShift != 0, state&modLock != 0)
}

// grabKeyboard перехватывает клавиатуру на окно замка. Может не удаться, если её уже
// держит другой grab (скринсейвер, меню) — тогда повторим на следующем тике сторожа.
func (o *x11Overlay) grabKeyboard() {
	reply, err := xproto.GrabKeyboard(o.conn, false, o.win, xproto.TimeCurrentTime,
		xproto.GrabModeAsync, xproto.GrabModeAsync).Reply()
	if err != nil {
		o.log.Warn("lock-screen: не удалось захватить клавиатуру", slog.Any("error", err))
		return
	}
	if reply.Status != xproto.GrabStatusSuccess {
		o.log.Warn("lock-screen: клавиатура занята другим захватом — повторим", slog.Int("status", int(reply.Status)))
	}
}

// raise поднимает окно поверх всех.
func (o *x11Overlay) raise() {
	xproto.ConfigureWindow(o.conn, o.win, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
}

// loop — цикл событий. Отдельная горутина раз в секунду переподнимает окно, держит
// захват и проверяет, не снял ли блокировку сервер (файл стал !Locked извне);
// в этом случае соединение закрывается, WaitForEvent разблокируется, и мы выходим.
func (o *x11Overlay) loop(statePath string) {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-o.stop:
				return
			case <-t.C:
				if s, err := lock.ReadState(statePath); err == nil && !s.Locked {
					o.log.Info("lock-screen: блокировка снята сервером — закрываем замок")
					o.shutdown() // разбудит WaitForEvent (соединение закроется)
					return
				}
				o.raise()
				o.grabKeyboard()
				xproto.SetInputFocus(o.conn, xproto.InputFocusPointerRoot, o.win, xproto.TimeCurrentTime)
			}
		}
	}()

	o.draw()
	for {
		ev, xerr := o.conn.WaitForEvent()
		if ev == nil && xerr == nil {
			return // соединение закрыто (снятие блокировки или завершение)
		}
		if xerr != nil {
			continue // протокольная ошибка на fire-and-forget-запрос — не фатально
		}
		switch e := ev.(type) {
		case xproto.ExposeEvent:
			o.draw()
		case xproto.KeyPressEvent:
			if o.onKey(e, statePath) {
				return // введён верный пароль
			}
		}
	}
}

// onKey обрабатывает нажатие. Возвращает true, если замок пора закрыть (верный
// пароль или блокировка уже снята сервером).
func (o *x11Overlay) onKey(e xproto.KeyPressEvent, statePath string) bool {
	switch ks := o.lookupKeysym(e.Detail, e.State); ks {
	case ksReturn, ksKPEnter:
		return o.submit(statePath)
	case ksBackSpace:
		if n := len(o.password); n > 0 {
			o.password = o.password[:n-1]
		}
		o.errMsg = ""
	case ksEscape:
		o.password = o.password[:0]
		o.errMsg = ""
	default:
		if r, ok := keysymToRune(ks); ok {
			o.password = append(o.password, r)
			o.errMsg = ""
		}
	}
	o.draw()
	return false
}

// submit сверяет введённый пароль со СВЕЖИМ состоянием (окно может висеть долго, а
// демон за это время мог применить НОВЫЙ лок с другим hash — сверяться с
// прочитанным при старте нельзя). При совпадении шлёт запрос на разблокировку.
func (o *x11Overlay) submit(statePath string) bool {
	cur, err := lock.ReadState(statePath)
	if err != nil {
		// Fail-closed, как у Windows-замка и демона: транзиентный сбой I/O — не
		// повод закрывать замок, повторный Enter попробует ещё раз.
		o.errMsg = "Не удалось проверить состояние блокировки — попробуйте ещё раз"
		o.draw()
		return false
	}
	if !cur.Locked {
		return true // разблокировано сервером, пока вводили пароль
	}
	pw := string(o.password)
	if bcrypt.CompareHashAndPassword([]byte(cur.Hash), []byte(pw)) != nil {
		o.errMsg = "Неверный пароль"
		o.password = o.password[:0]
		o.draw()
		return false
	}
	// Пароль верный. Юзер-сессия не может переписать lock.json (владелец — демон
	// под root, /tmp со sticky-битом), поэтому шлём запрос — демон снимет
	// блокировку сам (Manager.processUnlockRequests). Зеркало macOS-замка.
	if err := lock.WriteUnlockRequest(filepath.Dir(statePath), pw); err != nil {
		o.log.Error("lock-screen: не удалось отправить запрос на разблокировку", slog.Any("error", err))
	}
	return true
}

// draw перерисовывает содержимое окна (по центру экрана).
func (o *x11Overlay) draw() {
	xproto.ClearArea(o.conn, false, o.win, 0, 0, 0, 0) // очистить в фон (чёрный)
	cy := int(o.screen.HeightInPixels) / 2
	o.drawCentered(o.gcText, cy-90, "Устройство заблокировано")
	o.drawCentered(o.gcText, cy-50, o.reason)
	o.drawCentered(o.gcText, cy, "Введите пароль разблокировки и нажмите Enter:")
	o.drawCentered(o.gcText, cy+40, "["+strings.Repeat("*", len(o.password))+"]")
	if o.errMsg != "" {
		o.drawCentered(o.gcErr, cy+90, o.errMsg)
	}
}

// drawCentered рисует строку, приблизительно центрируя её по горизонтали.
func (o *x11Overlay) drawCentered(gc xproto.Gcontext, y int, s string) {
	runeCount := len([]rune(s))
	x := (int(o.screen.WidthInPixels) - runeCount*o.charW) / 2
	if x < 20 {
		x = 20 // не уезжаем за левый край на длинных строках
	}
	o.drawText(gc, int16(x), int16(y), s)
}

// drawText рисует текст выбранным шрифтом. ImageText8/16 принимают не больше 255
// знаков за вызов — длинные строки бьём на части.
func (o *x11Overlay) drawText(gc xproto.Gcontext, x, y int16, s string) {
	if o.unicodeFont {
		cs := toChar2b(s)
		for len(cs) > 0 {
			n := min(len(cs), 254)
			xproto.ImageText16(o.conn, byte(n), xproto.Drawable(o.win), gc, x, y, cs[:n])
			x += int16(n * o.charW)
			cs = cs[n:]
		}
		return
	}
	b := runesToLatin1(s)
	for len(b) > 0 {
		n := min(len(b), 254)
		xproto.ImageText8(o.conn, byte(n), xproto.Drawable(o.win), gc, x, y, string(b[:n]))
		x += int16(n * o.charW)
		b = b[n:]
	}
}

// toChar2b кодирует строку в UCS-2 (CHAR2B) для ImageText16. Символы вне BMP
// (кодовая точка > 0xffff) UCS-2 не представимы — заменяются на '?'.
func toChar2b(s string) []xproto.Char2b {
	rs := []rune(s)
	out := make([]xproto.Char2b, 0, len(rs))
	for _, r := range rs {
		if r > 0xffff {
			r = '?'
		}
		out = append(out, xproto.Char2b{Byte1: byte(r >> 8), Byte2: byte(r)})
	}
	return out
}

// shutdown идемпотентно останавливает сторож-горутину и закрывает соединение с X
// (последнее разблокирует WaitForEvent в loop).
func (o *x11Overlay) shutdown() {
	o.stopOnce.Do(func() { close(o.stop) })
	o.closeOnce.Do(func() {
		if o.conn != nil {
			o.conn.Close()
		}
	})
}
