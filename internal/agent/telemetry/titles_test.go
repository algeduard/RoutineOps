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
		// Локали не-RU/EN (регресс на fail-open: браузер локализует слово-маркер).
		"Neuer Tab – Google Chrome (Inkognito)",             // de Chrome
		"Beispiel — Mozilla Firefox (Privater Modus)",       // de Firefox
		"Nouvel onglet — Google Chrome (Navigation privée)", // fr
		"Ejemplo — Google Chrome (Incógnito)",               // es Chrome
		"Ejemplo — Navegación privada — Firefox",            // es Firefox
		"Exemplo — Google Chrome (Guia anônima)",            // pt-BR
		"Esempio — Navigazione anonima — Firefox",           // it Firefox
		"Nowa karta — Google Chrome (Tryb prywatny)",        // pl
		"Voorbeeld — Firefox (Privévenster)",                // nl
		"Yeni Sekme — Google Chrome (Gizli)",                // tr
		"新标签页 - Google Chrome（无痕式）",                         // zh-CN
		"新しいタブ - Google Chrome（シークレット モード）",                 // ja
		"새 탭 - Google Chrome(시크릿 모드)",                       // ko
	}
	for _, s := range private {
		if !isPrivateBrowsing(s) {
			t.Errorf("ожидали private для %q", s)
		}
	}
	normal := []string{
		"Хабр — Google Chrome",
		"routineops — main — Visual Studio Code",
		"Private Equity News — Google Chrome", // «private» в контенте ≠ приватный режим (не ловим ложно)
		"",                                    // пусто
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

func TestIsBrowserProcess(t *testing.T) {
	browsers := []string{"chrome.exe", "MSEDGE.EXE", "  firefox.exe ", "Brave.exe", "browser.exe"}
	for _, s := range browsers {
		if !isBrowserProcess(s) {
			t.Errorf("ожидали браузер для %q", s)
		}
	}
	notBrowsers := []string{"code.exe", "explorer.exe", "notepad.exe", ""}
	for _, s := range notBrowsers {
		if isBrowserProcess(s) {
			t.Errorf("НЕ ожидали браузер для %q", s)
		}
	}
}

func TestSanitizeURL(t *testing.T) {
	// Обычный URL — тримится, по сути не меняется.
	if got := sanitizeURL("  https://example.com/page?x=1  "); got != "https://example.com/page?x=1" {
		t.Errorf("sanitizeURL обычного = %q", got)
	}
	// Пусто → пусто.
	if got := sanitizeURL("   "); got != "" {
		t.Errorf("пустой URL должен давать пусто, got %q", got)
	}
	// Слишком длинный URL обрезается до maxURLLen рун.
	long := "https://example.com/" + strings.Repeat("a", maxURLLen+50)
	if n := len([]rune(sanitizeURL(long))); n != maxURLLen {
		t.Errorf("длина URL после обрезки = %d, want %d", n, maxURLLen)
	}
}
