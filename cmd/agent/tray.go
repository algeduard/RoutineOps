//go:build windows || (darwin && cgo)

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/Floodww/RoutineOps/internal/agent/admin"
	"github.com/Floodww/RoutineOps/internal/agent/config"
	"github.com/Floodww/RoutineOps/internal/agent/lock"
	"github.com/Floodww/RoutineOps/internal/agent/status"
)

// runTray запускает иконку статуса в системном трее (Windows/macOS). Это отдельный
// per-user процесс: служба и трей не общаются напрямую,
// поэтому статус трей читает из status-файла, который пишет служба
// (см. internal/agent/status). Меню и пункта «выход» нет — это индикатор, а не
// управление: остановить агента может только админ через службу.
func runTray(cfg *config.Config) {
	setupTrayPlatform() // платформенно-зависимая настройка (например, скрытие консоли)

	// «Нет связи», если последний heartbeat старше 2× интервала.
	staleAfter := 2 * cfg.HeartbeatInterval
	statusPath := statusFilePath(cfg)

	update := func() {
		st, err := status.Read(statusPath)
		switch {
		case err != nil:
			systray.SetTooltip("RoutineOps: агент не запущен")
		case st.Online(staleAfter):
			id := st.DeviceID
			if id == "" {
				id = "—"
			}
			systray.SetTooltip(fmt.Sprintf("RoutineOps: на связи (%s)", id))
		default:
			systray.SetTooltip("RoutineOps: нет связи с сервером")
		}
	}

	// Замок блокировки: трей (юзер-сессия) запускает отдельный процесс
	// `agent lock-screen`, когда устройство заблокировано (служба в session-0 GUI
	// показать не может). Состояние читаем из общего файла; lockRunning не даёт
	// плодить лишние окна, пока одно уже висит.
	lockPath := lockStatePath(cfg)
	var lockMu sync.Mutex
	var lockCmd *exec.Cmd

	checkLock := func() {
		st, err := lock.ReadState(lockPath)

		lockMu.Lock()
		defer lockMu.Unlock()

		isLocked := err == nil && st.Locked

		if !isLocked {
			if lockCmd != nil && lockCmd.Process != nil {
				_ = lockCmd.Process.Kill()
				lockCmd = nil
			}
			return
		}

		if lockCmd != nil {
			return // уже запущен
		}

		exe, err := os.Executable()
		if err != nil {
			return
		}
		cmd := exec.Command(exe, "lock-screen", "-lock-state", lockPath)
		configureLockScreenCmd(cmd) // платформенно-зависимые атрибуты процесса
		if err := cmd.Start(); err != nil {
			return
		}
		lockCmd = cmd
		go func(c *exec.Cmd) {
			_ = c.Wait()
			lockMu.Lock()
			if lockCmd == c {
				lockCmd = nil
			}
			lockMu.Unlock()
		}(cmd)
	}

	// Кнопка «Запросить админ-права»: трей кладёт файл-заявку, службу её подхватит
	// и отправит RequestAdminAccess (у трея нет машинной mTLS-идентичности).
	adminReqPath := adminRequestPath(cfg)
	requestAdmin := func(mi *systray.MenuItem) {
		if err := admin.WriteUserRequest(adminReqPath, "запрос с устройства (трей)"); err != nil {
			mi.SetTitle("Не удалось отправить — повторить")
			return
		}
		mi.SetTitle("Запрос отправлен ✓")
		go func() { time.Sleep(10 * time.Second); mi.SetTitle("Запросить админ-права") }()
	}

	// Кнопка «Сообщить о проблеме»: запускает окно helpui отдельным процессом —
	// текст и превью скриншота в пункт меню не поместить, а снять экран может
	// только юзер-сессия (служба в session 0 рабочий стол не видит). Заявку окно
	// кладёт файлом рядом с admin-request.json, отправит её служба. Один процесс
	// за раз (helpCmd) — от даблклика; сверх того окно держит свой мьютекс.
	// Пока только Windows: на macOS диалога helpui ещё нет.
	var helpMu sync.Mutex
	var helpCmd *exec.Cmd
	openHelpWindow := func() {
		helpMu.Lock()
		defer helpMu.Unlock()
		if helpCmd != nil {
			return // окно уже открыто
		}
		exe, err := os.Executable()
		if err != nil {
			return
		}
		cmd := exec.Command(exe, "help-window", "-lock-state", lockPath)
		configureLockScreenCmd(cmd) // те же атрибуты, что у lock-screen (спрятать консоль)
		if err := cmd.Start(); err != nil {
			return
		}
		helpCmd = cmd
		go func(c *exec.Cmd) {
			_ = c.Wait()
			helpMu.Lock()
			if helpCmd == c {
				helpCmd = nil
			}
			helpMu.Unlock()
		}(cmd)
	}

	onReady := func() {
		setTrayIcon() // иконка платформенная: .ico на Windows, template-PNG на macOS
		if runtime.GOOS == "windows" {
			mHelp := systray.AddMenuItem("Сообщить о проблеме", "Отправить обращение в ИТ-отдел (можно со скриншотом)")
			go func() {
				for range mHelp.ClickedCh {
					openHelpWindow()
				}
			}()
		}
		mReqAdmin := systray.AddMenuItem("Запросить админ-права", "Запросить временные права администратора")
		go func() {
			for range mReqAdmin.ClickedCh {
				requestAdmin(mReqAdmin)
			}
		}()
		update()
		checkLock()
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for range t.C {
				update()
				checkLock()
			}
		}()
	}

	systray.Run(onReady, func() {})
}
