package telemetry

import (
	"strings"
	"unicode/utf8"
)

// maxTitleLen — потолок длины заголовка окна (руны). Заголовки могут быть длинными
// (полный путь документа, длинный тайтл страницы) — режем ради приватности и размера.
const maxTitleLen = 256

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
