//go:build windows

package remotedesktop

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
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
// закрытие окна, таймаут, отмена ctx (смерть стрима/сессии) или ошибка показа →
// false (fail-safe: без явного согласия сеанс не начинается). Стиль GUI — walk,
// как в internal/agent/lockui.
//
// ctx отменяется, когда сервер снёс сессию (WS закрыт / attach не удался) — тогда
// диалог закрывается, чтобы не висел запрос для сеанса, которого никто не смотрит.
func requestConsent(ctx context.Context) bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var dlg *walk.Dialog
	// decided гарантирует, что ПЕРВОЕ решение (клик или таймаут/отмена) окончательно:
	// без него dlg.Cancel() из очереди Synchronize мог бы перезаписать уже
	// выставленный клик «Разрешить» (Accept) на Cancel в той же итерации mainLoop
	// walk — явное согласие молча стало бы отказом. Оба колбэка исполняются на
	// UI-потоке диалога, поэтому atomic-CAS достаточно.
	var decided int32
	accept := func() {
		if atomic.CompareAndSwapInt32(&decided, 0, 1) {
			dlg.Accept()
		}
	}
	cancel := func() {
		if atomic.CompareAndSwapInt32(&decided, 0, 1) {
			dlg.Cancel()
		}
	}

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
					declarative.PushButton{Text: "Отклонить", OnClicked: cancel},
					declarative.PushButton{Text: "Разрешить", OnClicked: accept},
				},
			},
		},
	}).Create(nil); err != nil {
		return false // не смогли показать окно — безопасно отказать
	}

	// Поверх всех окон и на передний план — запрос не должен потеряться.
	win.SetWindowPos(dlg.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_SHOWWINDOW)
	win.SetForegroundWindow(dlg.Handle())

	// Автоотказ по таймауту — ОТМЕНЯЕМЫЙ таймер (timer.Stop на выходе), чтобы не
	// оставлять висящую горутину/замыкание после раннего ответа пользователя.
	timer := time.AfterFunc(consentTimeout(), func() { dlg.Synchronize(cancel) })
	defer timer.Stop()

	// Отмена по смерти стрима/сессии: если сервер закрыл сессию, закрываем диалог.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			dlg.Synchronize(cancel)
		case <-stop:
		}
	}()

	return dlg.Run() == walk.DlgCmdOK
}
