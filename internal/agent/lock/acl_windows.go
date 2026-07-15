//go:build windows

package lock

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// userWritableDirSDDL — DACL общего каталога: SYSTEM и Администраторы — полный
// доступ; Пользователи (BU) — Modify (0x1301bf: чтение/запись/удаление файлов).
// OICI = наследуется на файлы и подкаталоги. Нужно, чтобы лок-экран и трей
// (юзер-сессия) могли писать в каталог, который служба создаёт под SYSTEM:
// снимать блокировку (ClearState) и класть файл-заявку на админ-права.
const userWritableDirSDDL = "D:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;BU)"

// EnsureUserWritableDir создаёт dir и ставит DACL, разрешающий пользователям запись.
// Вызывает служба (под SYSTEM) на старте. От локального админа не защищает (вне объёма).
func EnsureUserWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(userWritableDirSDDL)
	if err != nil {
		return fmt.Errorf("разбор SDDL каталога: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("извлечение DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("установка DACL каталога %s: %w", dir, err)
	}
	return nil
}
