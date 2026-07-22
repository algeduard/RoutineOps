//go:build windows

package remotedesktop

import (
	"runtime"
	"sync"
	"syscall"

	"github.com/lxn/walk"
	declarative "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

var (
	user32SB                       = syscall.NewLazyDLL("user32.dll")
	procSetLayeredWindowAttributes = user32SB.NewProc("SetLayeredWindowAttributes")
)

const lwaAlpha = 0x2 // LWA_ALPHA

// startSessionBanner показывает на всё время удалённого сеанса небольшую
// НЕАКТИВИРУЕМУЮ топмост-плашку «идёт удалённый сеанс» — чтобы пользователь видел
// активность не только на старте (согласие), но и в течение всей сессии. Окно живёт
// в отдельном OS-потоке с собственным циклом сообщений walk (как lockui). Возвращает
// функцию остановки (закрыть плашку) — вызывать при завершении сеанса.
//
// Окно неактивируемое (WS_EX_NOACTIVATE) и tool-window (нет кнопки на таскбаре):
// оно не должно воровать фокус у пользователя и у удалённого управления админа.
func startSessionBanner() func() {
	var mw *walk.MainWindow
	ready := make(chan struct{})
	var once sync.Once

	go func() {
		runtime.LockOSThread()
		if err := (declarative.MainWindow{
			AssignTo:   &mw,
			Title:      "Удалённый сеанс",
			Size:       declarative.Size{Width: 470, Height: 46},
			Background: declarative.SolidColorBrush{Color: walk.RGB(178, 34, 34)},
			Layout:     declarative.HBox{Margins: declarative.Margins{Left: 14, Top: 4, Right: 14, Bottom: 4}},
			Children: []declarative.Widget{
				declarative.Label{
					Text:      "●  Идёт удалённый сеанс — IT-администратор видит ваш экран",
					TextColor: walk.RGB(255, 255, 255),
				},
			},
		}).Create(); err != nil {
			close(ready)
			return
		}
		applyBannerStyle(mw)
		mw.Show()
		close(ready)
		mw.Run() // цикл сообщений; выходит при mw.Close()
	}()

	<-ready
	return func() {
		once.Do(func() {
			if mw != nil {
				mw.Synchronize(func() { mw.Close() })
			}
		})
	}
}

// applyBannerStyle делает окно неактивируемым tool-окном поверх всех и ставит его
// сверху по центру основного экрана.
func applyBannerStyle(mw *walk.MainWindow) {
	h := mw.Handle()
	// TOPMOST+NOACTIVATE — не воровать фокус; TOOLWINDOW — нет кнопки на таскбаре;
	// LAYERED+TRANSPARENT — CLICK-THROUGH: инъектированные клики админа проходят
	// сквозь плашку в приложение под ней (иначе полоса сверху была бы недоступна
	// для удалённого управления весь сеанс).
	ex := win.GetWindowLong(h, win.GWL_EXSTYLE)
	win.SetWindowLong(h, win.GWL_EXSTYLE,
		ex|win.WS_EX_TOPMOST|win.WS_EX_NOACTIVATE|win.WS_EX_TOOLWINDOW|win.WS_EX_LAYERED|win.WS_EX_TRANSPARENT)
	// Непрозрачность 255 (видимо), но hit-test пропускает ввод (WS_EX_TRANSPARENT).
	procSetLayeredWindowAttributes.Call(uintptr(h), 0, 255, lwaAlpha)
	const bannerWidth int32 = 470
	x := (win.GetSystemMetrics(win.SM_CXSCREEN) - bannerWidth) / 2
	win.SetWindowPos(h, win.HWND_TOPMOST, x, 12, 0, 0, win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}
