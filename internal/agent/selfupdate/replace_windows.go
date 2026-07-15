//go:build windows

package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// replaceExecutable подменяет текущий исполняемый файл на Windows. Запущенный
// .exe удалить нельзя, но можно переименовать: отодвигаем текущий в .old и пишем
// новый на его место. Старый .old удалится при следующем запуске (он ещё занят
// работающим процессом до перезапуска).
func replaceExecutable(data []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	old := exe + ".old"
	// Убиваем другие экземпляры (например, tray в юзер-сессии), чтобы они
	// отпустили блокировку файла .old от прошлого обновления.
	baseExe := filepath.Base(exe)
	_ = exec.Command("taskkill", "/F", "/IM", baseExe, "/FI", fmt.Sprintf("PID ne %d", os.Getpid())).Run()
	_ = exec.Command("taskkill", "/F", "/IM", baseExe+".old", "/FI", fmt.Sprintf("PID ne %d", os.Getpid())).Run()

	_ = os.Remove(old) // подчистить .old от прошлого обновления (если уже не занят)

	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("отодвинуть текущий .exe: %w", err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		// Откат: вернуть старый бинарь на место, чтобы служба не осталась без exe.
		_ = os.Rename(old, exe)
		return fmt.Errorf("запись нового .exe: %w", err)
	}
	return nil
}

// CleanupOld удаляет оставшийся после обновления <exe>.old (best-effort,
// вызывать при старте — прошлый процесс уже не держит файл).
func CleanupOld() {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		_ = os.Remove(exe + ".old")
	}
}
