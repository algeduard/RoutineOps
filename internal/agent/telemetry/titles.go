package telemetry

import (
	"strings"
	"unicode/utf8"
)

// maxTitleLen — потолок длины заголовка окна (руны). Заголовки могут быть длинными
// (полный путь документа, длинный тайтл страницы) — режем ради приватности и размера.
const maxTitleLen = 256

// maxURLLen — потолок длины URL (руны). URL с query могут быть очень длинными — режем
// ради приватности и размера (тот же потолок, что и у заголовков).
const maxURLLen = 256

// browserProcesses — имена foreground-процессов, из которых РАЗРЕШЕНО читать URL
// активной вкладки через UI Automation. Гейт по имени процесса — это privacy-мера:
// address bar (Edit-контрол) есть и в НЕ-браузерах, и его значение может оказаться
// чем угодно (пароль, текст документа) — URL читаем ТОЛЬКО из заведомых браузеров.
// Сравнение по имени процесса регистронезависимое; список аддитивный.
var browserProcesses = map[string]bool{
	"chrome.exe":  true, // Google Chrome
	"msedge.exe":  true, // Microsoft Edge
	"firefox.exe": true, // Mozilla Firefox
	"brave.exe":   true, // Brave (Chromium)
	"opera.exe":   true, // Opera (Chromium)
	"vivaldi.exe": true, // Vivaldi (Chromium)
	"browser.exe": true, // Яндекс.Браузер (Chromium)
}

// isBrowserProcess сообщает, является ли имя процесса известным браузером, из которого
// допустимо читать URL активной вкладки.
func isBrowserProcess(name string) bool {
	return browserProcesses[strings.ToLower(strings.TrimSpace(name))]
}

// sanitizeURL приводит сырое значение address bar к виду, пригодному для сбора:
// тримится и обрезается по maxURLLen рун. Пустой результат = «URL не собираем».
// Исключение приватных/инкогнито-окон делается ВЫШЕ по стеку (по заголовку окна,
// isPrivateBrowsing) — та же эксклюзия, что и для заголовков.
func sanitizeURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	if utf8.RuneCountInString(url) > maxURLLen {
		r := []rune(url)
		url = string(r[:maxURLLen])
	}
	return url
}

// privateMarkers — признаки приватного/инкогнито окна браузера в заголовке. При
// совпадении заголовок НЕ собирается (приватный просмотр — явный сигнал, что
// пользователь не хочет истории; см. docs/device-telemetry-design.md §4). Сравнение
// регистронезависимое по подстроке; список аддитивный.
var privateMarkers = []string{
	"инкогнито",          // Chrome / Яндекс.Браузер (RU)
	"incognito",          // Chrome (EN)
	"inprivate",          // Edge
	"приватный просмотр", // Firefox (RU)
	"private browsing",   // Firefox (EN)
	"private window",     // Safari/прочие (EN)
}

// isPrivateBrowsing сообщает, похоже ли окно на приватный/инкогнито-режим браузера.
func isPrivateBrowsing(title string) bool {
	lower := strings.ToLower(title)
	for _, m := range privateMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// sanitizeTitle приводит сырой заголовок окна к виду, пригодному для сбора:
// приватные окна отбрасываются (""), остальное обрезается по maxTitleLen рун и
// тримится. Пустой результат = «заголовок не собираем для этого окна».
func sanitizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" || isPrivateBrowsing(title) {
		return ""
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		r := []rune(title)
		title = string(r[:maxTitleLen])
	}
	return title
}
