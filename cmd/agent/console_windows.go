//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// AttachConsole нет типизированной обёртки в x/sys/windows — зовём из kernel32.
var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// attachParentConsole привязывает stdout/stderr агента к консоли родительского
// процесса, если он запущен из неё (cmd/powershell — ручные CLI-ветки enroll,
// version, -h). Бинарь собран как GUI-subsystem (-H windowsgui), поэтому своей
// консоли у него нет: иначе в интерактивной сессии всплывало бы окно, закрытие
// которого слало CTRL_CLOSE_EVENT и убивало агент. Цена GUI-subsystem — CLI-ветки
// теряют stdout/stderr, что и чиним этим re-attach.
//
// Если родительской консоли нет (служба в session 0, трей, запуск из MSI),
// AttachConsole возвращает 0 — тогда ничего не делаем (вывод и так не нужен).
func attachParentConsole() {
	const attachParentProcess = 0xFFFFFFFF // ATTACH_PARENT_PROCESS == DWORD(-1)
	if r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	// Открываем консоль родителя напрямую: надёжнее GetStdHandle, который у
	// GUI-процесса мог закэшировать невалидный дескриптор.
	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return
	}
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return
	}
	f := os.NewFile(uintptr(h), "CONOUT$")
	os.Stdout = f
	os.Stderr = f
}
