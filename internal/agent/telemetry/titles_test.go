package telemetry

import (
	"strings"
	"testing"
)

func TestIsPrivateBrowsing(t *testing.T) {
	private := []string{
		"Новая вкладка — Яндекс Браузер (Инкогнито)",
		"New Tab - Google Chrome (Incognito)",
		"Пример — Microsoft Edge — InPrivate",
		"Example (Private Browsing) — Mozilla Firefox",
		"Приватный просмотр — Firefox",
	}
	for _, s := range private {
		if !isPrivateBrowsing(s) {
			t.Errorf("ожидали private для %q", s)
		}
	}
	normal := []string{
		"Хабр — Google Chrome",
		"routineops — main — Visual Studio Code",
		"", // пусто
	}
	for _, s := range normal {
		if isPrivateBrowsing(s) {
			t.Errorf("НЕ ожидали private для %q", s)
		}
	}
}

func TestSanitizeTitle(t *testing.T) {
	// Приватное окно → "".
	if got := sanitizeTitle("Сайт — Chrome (Инкогнито)"); got != "" {
		t.Errorf("приватное окно должно давать пусто, got %q", got)
	}
	// Обычный заголовок — тримится, не меняется по сути.
	if got := sanitizeTitle("  Хабр — Google Chrome  "); got != "Хабр — Google Chrome" {
		t.Errorf("sanitize обычного = %q", got)
	}
	// Длинный заголовок обрезается до maxTitleLen рун.
	long := strings.Repeat("я", maxTitleLen+50)
	got := sanitizeTitle(long)
	if n := len([]rune(got)); n != maxTitleLen {
		t.Errorf("длина после обрезки = %d, want %d", n, maxTitleLen)
	}
}
