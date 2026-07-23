//go:build windows

package command

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// uninstallRegistryPaths — те же ветки Uninstall (64/32-бит), что читает инвентарь
// (collector_windows.go). HKLM: агент — служба (SYSTEM), видит машинно-широкие установки;
// per-user (HKCU) не видны — известное ограничение (корпоративное ПО обычно машинное).
var uninstallRegistryPaths = []string{
	`Software\Microsoft\Windows\CurrentVersion\Uninstall`,
	`Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
}

// removeSoftware находит продукт в реестре Uninstall по DisplayName (== name из инвентаря)
// и запускает его деинсталлятор ТИХО. version, если задан, сверяется с DisplayVersion —
// защита от удаления не той версии при совпадении имени. Возвращает вывод деинсталлятора.
func removeSoftware(ctx context.Context, name, version string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("remove_software: пустое имя продукта")
	}
	cmdline, err := findUninstallCommand(name, version)
	if err != nil {
		return "", err
	}
	// cmdline — тихая командная строка деинсталлятора (из реестра, её положил сам
	// установщик). Запускаем через cmd /C, т.к. это готовая команда с аргументами.
	out, err := exec.CommandContext(ctx, "cmd", "/C", cmdline).CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			// Не смогли даже запустить процесс, либо ctx (таймаут) отменил — не код выхода.
			return string(out), fmt.Errorf("запуск деинсталлятора: %w", err)
		}
		if !isUninstallSuccess(ee.ExitCode()) {
			return string(out), fmt.Errorf("деинсталлятор завершился с кодом %d", ee.ExitCode())
		}
		// 3010/1641 — удалено, но нужен ребут: успех, а не ошибка.
		return "удалено; требуется перезагрузка: " + string(out), nil
	}
	return "удалено: " + string(out), nil
}

// isUninstallSuccess — коды выхода, считающиеся успехом удаления: 0 (успех), 3010
// (ERROR_SUCCESS_REBOOT_REQUIRED — удалено, нужен ребут; /norestart заставляет msiexec
// вернуть именно его вместо перезагрузки), 1641 (ERROR_SUCCESS_REBOOT_INITIATED). Прочие —
// реальная ошибка.
func isUninstallSuccess(code int) bool {
	return code == 0 || code == 3010 || code == 1641
}

var msiProductRe = regexp.MustCompile(`\{[0-9A-Fa-f-]{36}\}`)

// innoUninstallerRe — деинсталлятор Inno Setup (unins000.exe и т.п.). Поддерживает тихий
// режим /VERYSILENT, поэтому такой можно запускать под SYSTEM без зависшего диалога.
var innoUninstallerRe = regexp.MustCompile(`(?i)\\unins\d*\.exe`)

// findUninstallCommand ищет по HKLM Uninstall (64/32-бит) запись с DisplayName == name и
// отдаёт ТИХУЮ команду деинсталляции. Предпочитает QuietUninstallString; для MSI собирает
// `msiexec /x {ProductCode} /qn`; иначе — UninstallString как есть (может показать UI).
func findUninstallCommand(name, version string) (string, error) {
	for _, base := range uninstallRegistryPaths {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, base, registry.READ)
		if err != nil {
			continue
		}
		subs, err := k.ReadSubKeyNames(-1)
		k.Close()
		if err != nil {
			continue
		}
		for _, sub := range subs {
			s, err := registry.OpenKey(registry.LOCAL_MACHINE, base+`\`+sub, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			display, _, _ := s.GetStringValue("DisplayName")
			if !strings.EqualFold(strings.TrimSpace(display), strings.TrimSpace(name)) {
				s.Close()
				continue
			}
			if version != "" {
				dv, _, _ := s.GetStringValue("DisplayVersion")
				if strings.TrimSpace(dv) != strings.TrimSpace(version) {
					s.Close()
					continue // имя совпало, версия нет — не тот продукт
				}
			}
			quiet, _, _ := s.GetStringValue("QuietUninstallString")
			uninstall, _, _ := s.GetStringValue("UninstallString")
			s.Close()
			if cmd := silentUninstall(quiet, uninstall, sub); cmd != "" {
				return cmd, nil
			}
			// Нашли продукт, но тихо удалить нечем — НЕ запускаем интерактивный
			// деинсталлятор под SYSTEM (повис бы диалог на невидимом рабочем столе).
			return "", fmt.Errorf("для %q нет тихой деинсталляции (поддерживаются MSI, QuietUninstallString и Inno Setup)", name)
		}
	}
	return "", fmt.Errorf("%q не найдено в списке установленного ПО", name)
}

// silentUninstall выбирает ГАРАНТИРОВАННО тихую команду. Порядок: QuietUninstallString
// как есть; MSI → `msiexec /x {ProductCode} /qn /norestart` (ProductCode = имя подключа,
// если GUID, иначе извлекается из UninstallString); Inno Setup → unins*.exe с
// /VERYSILENT. Если тихого пути нет — "" (вызывающий вернёт ошибку): запускать
// интерактивный деинсталлятор под SYSTEM в session 0 НЕЛЬЗЯ — его диалог повис бы на
// невидимом рабочем столе до таймаута, заняв слот и осиротив процесс.
func silentUninstall(quiet, uninstall, subkey string) string {
	if strings.TrimSpace(quiet) != "" {
		return quiet
	}
	if strings.TrimSpace(uninstall) == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(uninstall), "msiexec") {
		code := subkey
		if !msiProductRe.MatchString(code) {
			code = msiProductRe.FindString(uninstall)
		}
		if msiProductRe.MatchString(code) {
			return "msiexec /x " + code + " /qn /norestart"
		}
	}
	if innoUninstallerRe.MatchString(uninstall) {
		return uninstall + " /VERYSILENT /SUPPRESSMSGBOXES /NORESTART"
	}
	return "" // доказанно-тихого пути нет — fail-fast, интерактивный не запускаем
}
