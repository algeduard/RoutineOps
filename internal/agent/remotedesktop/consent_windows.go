//go:build windows

package remotedesktop

import (
	"os"
	"runtime"
	"time"

	"github.com/lxn/walk"
	declarative "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// defaultConsentTimeout — сколько ждём решения пользователя; по истечении — ОТКАЗ
// (fail-safe). Переопределяется env ROUTINEOPS_RD_CONSENT_TIMEOUT (для тестов/тюнинга).
const defaultConsentTimeout = 30 * time.Second

func consentTimeout() time.Duration {
	if v := os.Getenv("ROUTINEOPS_RD_CONSENT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultConsentTimeout
}

// requestConsent показывает пользователю в его интерактивной сессии модальный
// запрос на удалённый доступ. Возвращает true ТОЛЬКО при явном «Разрешить»; отказ,
// закрытие окна, таймаут или ошибка показа → false (fail-safe: без явного согласия
// сеанс не начинается). Стиль GUI — walk, как в internal/agent/lockui.
func requestConsent() bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var dlg *walk.Dialog
	if err := (declarative.Dialog{
		AssignTo: &dlg,
		Title:    "Запрос удалённого доступа",
		MinSize:  declarative.Size{Width: 470, Height: 200},
		Layout:   declarative.VBox{Margins: declarative.Margins{Left: 20, Top: 20, Right: 20, Bottom: 16}},
		Children: []declarative.Widget{
			declarative.Label{
				Text: "IT-администратор запрашивает удалённый доступ к вашему рабочему столу.\n\n" +
					"Во время сеанса администратор видит ваш экран и может управлять мышью\n" +
					"и клавиатурой. Разрешить подключение?",
			},
			declarative.VSpacer{},
			declarative.Composite{
				Layout: declarative.HBox{},
				Children: []declarative.Widget{
					declarative.HSpacer{},
					declarative.PushButton{Text: "Отклонить", OnClicked: func() { dlg.Cancel() }},
					declarative.PushButton{Text: "Разрешить", OnClicked: func() { dlg.Accept() }},
				},
			},
		},
	}).Create(nil); err != nil {
		return false // не смогли показать окно — безопасно отказать
	}

	// Поверх всех окон и на передний план — запрос не должен потеряться.
	win.SetWindowPos(dlg.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_SHOWWINDOW)
	win.SetForegroundWindow(dlg.Handle())

	// Автоотказ по таймауту.
	go func() {
		time.Sleep(consentTimeout())
		dlg.Synchronize(func() { dlg.Cancel() })
	}()

	return dlg.Run() == walk.DlgCmdOK
}
