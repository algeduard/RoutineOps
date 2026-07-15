//go:build windows

package collector

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// psOut запускает PowerShell-команду и возвращает её stdout (или "" при ошибке).
// [Console]::OutputEncoding принудительно переводится в UTF-8, чтобы Go читал
// байты корректно на системах с кодовой страницей CP1251/CP866.
func psOut(command string) string {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false); `+command,
	).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// osVersion возвращает версию ОС чистым UTF-8. Раньше использовался `cmd /c ver`,
// но на локализованной Windows он печатает текст («Версия …») в OEM-кодировке
// (CP866/CP1251) — в UI это превращалось в ромбики. Берём строку через PowerShell
// (psOut форсирует UTF-8), `ver` оставлен лишь запасным путём.
func osVersion() string {
	if v := strings.TrimSpace(psOut(`$o = Get-CimInstance Win32_OperatingSystem; "$($o.Caption) ($($o.Version))"`)); v != "" {
		return v
	}
	out, err := exec.Command("cmd", "/c", "ver").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// cpuModel возвращает дружелюбное имя процессора («Intel(R) Core(TM) Ultra 5 …»).
// Раньше первым шёл PROCESSOR_IDENTIFIER, но он даёт лишь «Intel64 Family 6 Model
// … GenuineIntel» (семейство/модель, не маркетинговое имя). Имя лежит в реестре
// (ProcessorNameString) — читаем его нативно; env остаётся последним фолбэком.
func cpuModel() string {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		name, _, gerr := k.GetStringValue("ProcessorNameString")
		k.Close()
		if gerr == nil {
			if name = strings.TrimSpace(name); name != "" {
				return name
			}
		}
	}
	if v := strings.TrimSpace(psOut(`(Get-CimInstance Win32_Processor).Name`)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER"))
}

func ramMegabytes() int64 {
	s := strings.TrimSpace(psOut(`(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory`))
	b, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return b / (1024 * 1024)
}

func diskTotal() string {
	s := strings.TrimSpace(psOut(`(Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='C:'").Size`))
	b, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return ""
	}
	return humanBytes(b)
}

func serialNumber() string {
	return strings.TrimSpace(psOut("(Get-CimInstance Win32_BIOS).SerialNumber"))
}

// uninstallPaths — ветки реестра со списком установленного ПО: 64-битная и
// 32-битная (Wow6432Node). Это разные физические ветки, не дубли друг друга.
var uninstallPaths = []string{
	`Software\Microsoft\Windows\CurrentVersion\Uninstall`,
	`Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
}

// installedSoftware читает ветки реестра Uninstall (64/32-бит) НАТИВНО через
// Windows-реестр. Раньше шло через PowerShell + ConvertTo-Json, но на полевых
// данных DisplayName с кириллицей/неразрывными пробелами приходил битым (ромбики
// U+FFFD из-за кодовых страниц консоли). Реестр отдаёт строки в UTF-16, Go
// конвертирует их в UTF-8 сам — класс бага с кодировкой исчезает, заодно нет
// сабпроцесса. Достаточно DisplayName + DisplayVersion.
func installedSoftware() []Software {
	var sw []Software
	for _, base := range uninstallPaths {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, base, registry.READ)
		if err != nil {
			continue // ветки может не быть (например, Wow6432Node на 32-битной ОС)
		}
		names, err := k.ReadSubKeyNames(-1)
		k.Close()
		if err != nil {
			continue
		}
		for _, name := range names {
			sub, err := registry.OpenKey(registry.LOCAL_MACHINE, base+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			display, _, err := sub.GetStringValue("DisplayName")
			if err != nil || display == "" {
				sub.Close()
				continue // записи без DisplayName (обновления, системные компоненты) пропускаем
			}
			version, _, _ := sub.GetStringValue("DisplayVersion")
			sub.Close()
			sw = append(sw, Software{Name: display, Version: version})
		}
	}
	return sw
}
