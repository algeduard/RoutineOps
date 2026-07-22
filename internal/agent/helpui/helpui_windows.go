//go:build windows

// Package helpui — окно «Сообщить о проблеме» (юзер-сессия).
//
// Запускается как `agent help-window` отдельным процессом в сессии пользователя
// (запускает трей по пункту меню): сотрудник описывает проблему и опционально
// прикладывает скриншот. Скриншот снимается ДО показа окна и ТОЛЬКО по явному
// действию пользователя (открыл окно ← сам, отправил с чекбоксом ← сам),
// с превью — сотрудник видит, что именно уйдёт в ИТ-отдел. Заявка пишется
// файлом (helpreq.Write); отправляет её служба — у юзер-сессии нет машинной
// mTLS-идентичности (тот же паттерн, что admin-request.json).
package helpui

import (
	"log/slog"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"github.com/lxn/walk"
	declarative "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/Floodww/RoutineOps/internal/agent/helpreq"
)

// Параметры скриншота: даунскейл до 1600px по длинной стороне + JPEG 70 держат
// типичный кадр в 100–400КБ — с большим запасом до серверного лимита 2МБ.
const (
	screenshotMaxDim  = 1600
	screenshotQuality = 70
)

// singleInstance не даёт открыть два окна помощи: трей может быть перезапущен
// и потерять счёт дочерним процессам. Local\ — своё пространство имён у каждой
// логон-сессии (при быстром переключении пользователей окна независимы).
// Хэндл намеренно не закрываем — мьютекс живёт до конца процесса (паттерн
// trayAlreadyRunning / lockui.singleInstance).
func singleInstance() bool {
	name, err := windows.UTF16PtrFromString(`Local\RoutineOpsHelpWindow`)
	if err != nil {
		return true // не смогли проверить — показываем
	}
	_, err = windows.CreateMutex(nil, false, name)
	return err != windows.ERROR_ALREADY_EXISTS
}

// currentUsername — логин консольного пользователя (поле reporter обращения).
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USERNAME")
}

// Run показывает окно обращения и по «Отправить» кладёт заявку в spoolPath.
// Блокирует поток до закрытия окна.
func Run(spoolPath string, log *slog.Logger) error {
	if !singleInstance() {
		log.Info("help-window: окно уже открыто другим процессом — выходим")
		return nil
	}
	runtime.LockOSThread() // GUI walk требует постоянного OS-потока

	// Скриншот — ДО создания окна: иначе окно попало бы в кадр и закрыло собой
	// проблему, которую сотрудник хочет показать.
	shot, shotJPEG, shotErr := captureScreen(screenshotMaxDim, screenshotQuality)
	if shotErr != nil {
		log.Warn("help-window: скриншот не снят — обращение будет без него", slog.Any("error", shotErr))
	}
	hasShot := len(shotJPEG) > 0

	var mw *walk.MainWindow
	var msgEdit *walk.TextEdit
	var attachCB *walk.CheckBox
	var preview *walk.ImageView
	var errLabel *walk.Label

	submit := func() {
		msg := strings.TrimSpace(msgEdit.Text())
		attach := hasShot && attachCB.Checked()
		if msg == "" && !attach {
			errLabel.SetText("Опишите проблему или приложите скриншот.")
			return
		}
		req := helpreq.UserRequest{
			Message:   msg,
			Reporter:  currentUsername(),
			CreatedAt: time.Now().Unix(),
		}
		if attach {
			req.ScreenshotJPEG = shotJPEG
		}
		if err := helpreq.Write(spoolPath, req); err != nil {
			log.Error("help-window: не удалось записать заявку", slog.Any("error", err))
			errLabel.SetText("Не удалось сохранить обращение — попробуйте ещё раз.")
			return
		}
		log.Info("help-window: обращение записано, отправит служба",
			slog.Bool("screenshot", attach))
		walk.MsgBox(mw, "Обращение принято",
			"Обращение передано агенту и будет отправлено в ИТ-отдел.",
			walk.MsgBoxIconInformation)
		mw.Close()
	}

	err := (declarative.MainWindow{
		AssignTo: &mw,
		Title:    "RoutineOps — сообщить о проблеме",
		MinSize:  declarative.Size{Width: 480, Height: 420},
		Size:     declarative.Size{Width: 580, Height: 620},
		Layout:   declarative.VBox{Margins: declarative.Margins{Left: 16, Top: 12, Right: 16, Bottom: 12}, Spacing: 8},
		Children: []declarative.Widget{
			declarative.Label{Text: "Опишите проблему — обращение уйдёт в ИТ-отдел:"},
			declarative.TextEdit{
				AssignTo: &msgEdit,
				VScroll:  true,
				MinSize:  declarative.Size{Height: 110},
			},
			declarative.CheckBox{
				AssignTo: &attachCB,
				Text:     "Приложить скриншот экрана",
				Checked:  hasShot,
				Enabled:  hasShot,
				OnCheckedChanged: func() {
					// Превью прячем вместе с галкой — видно ровно то, что уйдёт.
					preview.SetVisible(attachCB.Checked())
				},
			},
			declarative.ImageView{
				AssignTo: &preview,
				Mode:     declarative.ImageViewModeZoom,
				MinSize:  declarative.Size{Height: 200},
				Visible:  hasShot,
			},
			declarative.Label{
				Text:      "Скриншот снят в момент открытия окна — проверьте, что на нём нет лишнего.",
				TextColor: walk.RGB(120, 120, 120),
				Visible:   hasShot,
			},
			declarative.Label{AssignTo: &errLabel, TextColor: walk.RGB(220, 40, 40)},
			declarative.Composite{
				Layout: declarative.HBox{},
				Children: []declarative.Widget{
					declarative.HSpacer{},
					declarative.PushButton{Text: "Отправить", OnClicked: submit},
					declarative.PushButton{Text: "Отмена", OnClicked: func() { mw.Close() }},
				},
			},
		},
	}).Create()
	if err != nil {
		return err
	}

	if shot != nil {
		if bmp, err := walk.NewBitmapFromImage(shot); err == nil {
			_ = preview.SetImage(bmp)
		} else {
			log.Warn("help-window: превью скриншота не построено", slog.Any("error", err))
		}
	}

	win.SetForegroundWindow(mw.Handle())
	mw.Run()
	return nil
}
