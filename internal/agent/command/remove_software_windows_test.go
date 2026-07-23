//go:build windows

package command

import "testing"

func TestSilentUninstall(t *testing.T) {
	guid := "{12345678-1234-1234-1234-1234567890AB}"
	cases := []struct {
		name, quiet, uninstall, subkey, want string
	}{
		{"quiet предпочтителен", `"C:\app\unins.exe" /S`, `"C:\app\unins.exe"`, "App", `"C:\app\unins.exe" /S`},
		{"MSI по GUID-подключу", "", `MsiExec.exe /I` + guid, guid, "msiexec /x " + guid + " /qn /norestart"},
		{"MSI GUID из UninstallString", "", `MsiExec.exe /X` + guid, "SomeApp", "msiexec /x " + guid + " /qn /norestart"},
		{"Inno unins000.exe → /VERYSILENT", "", `"C:\Program Files\App\unins000.exe"`, "App", `"C:\Program Files\App\unins000.exe" /VERYSILENT /SUPPRESSMSGBOXES /NORESTART`},
		{"не-MSI не-Inno без Quiet → пусто (fail-fast, не вешаем UI)", "", `"C:\app\setup.exe" --uninstall`, "App", ""},
		{"нет строк деинсталляции → пусто", "", "", "App", ""},
	}
	for _, c := range cases {
		if got := silentUninstall(c.quiet, c.uninstall, c.subkey); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsUninstallSuccess(t *testing.T) {
	for _, c := range []struct {
		code int
		want bool
	}{
		{0, true}, {3010, true}, {1641, true}, // 0/ребут-успех
		{1, false}, {1603, false}, {1605, false}, {-1, false}, // ошибки
	} {
		if got := isUninstallSuccess(c.code); got != c.want {
			t.Errorf("isUninstallSuccess(%d) = %v, want %v", c.code, got, c.want)
		}
	}
}
