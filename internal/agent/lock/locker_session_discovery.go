package lock

import (
	"strconv"
	"strings"
)

// Чистые парсеры вывода systemd-logind (loginctl) для поиска активной графической
// сессии. Вынесены без build-тега намеренно: это разбор строк без обращений к ОС,
// поэтому их можно юнит-тестировать на любой платформе (в т.ч. в CI под Windows),
// тогда как запуск оверлея с понижением привилегий (locker_session_linux.go)
// собирается только под linux и вживую здесь не проверяется. На не-linux функции
// просто не используются — для Go это не ошибка.

// minInteractiveUID — граница «человеческих» учёток: greeter'ы дисплей-менеджера
// (gdm/sddm/lightdm) держат настоящую активную сессию на seat0, но под системным
// UID (<1000). Без фильтра служба подняла бы замок в сессии greeter'а на экране
// входа, где входить некому. Совпадает с scripts.minRegularUID (SYS_UID_MAX ≤999).
const minInteractiveUID = 1000

// loginctlSessionIDs достаёт идентификаторы сессий из вывода
// `loginctl list-sessions --no-legend`. Первая колонка — id сессии; строки без
// числового второго поля (UID) считаем мусором/заголовком и пропускаем.
func loginctlSessionIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if _, err := strconv.Atoi(f[1]); err != nil {
			continue // второе поле обязано быть UID-числом
		}
		ids = append(ids, f[0])
	}
	return ids
}

// parseLoginctlProps разбирает вывод `loginctl show-session <id> -p ...` — строки
// вида KEY=VALUE — в map.
func parseLoginctlProps(out string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			m[line[:i]] = strings.TrimSpace(line[i+1:])
		}
	}
	return m
}

// activeX11FromProps решает по свойствам сессии, годится ли она для оверлея:
// активная (Active=yes), тип x11 (Wayland/tty — следующие шаги), обычный
// пользователь (UID≥1000) с непустым Display. Возвращает uid, DISPLAY, имя учётки.
func activeX11FromProps(props map[string]string) (uid int, display, name string, ok bool) {
	if props["Active"] != "yes" {
		return 0, "", "", false
	}
	if props["Type"] != "x11" {
		return 0, "", "", false // чистый Wayland/tty — оверлей X11 их не покрывает
	}
	uid, err := strconv.Atoi(props["User"])
	if err != nil || uid < minInteractiveUID {
		return 0, "", "", false
	}
	display = props["Display"]
	if display == "" {
		return 0, "", "", false
	}
	return uid, display, props["Name"], true
}
