// Помощники декодирования X11-клавиш для лок-экрана. Вынесены из lockui_linux.go
// без build-тега намеренно: это чистые функции над uint32/rune без зависимости от
// X11, поэтому их можно юнит-тестировать на любой ОС (в т.ч. в CI под Windows),
// тогда как сам GUI (lockui_linux.go) собирается только под linux и вживую здесь
// не проверяется. На не-linux функции просто не используются (это не ошибка Go).
package lockui

// Специальные (непечатаемые) keysym'ы X11, которые нужны лок-экрану — значения из
// keysymdef.h. Enter/BackSpace/Escape обрабатываются отдельно от ввода символов.
const (
	ksBackSpace = 0xff08
	ksReturn    = 0xff0d
	ksEscape    = 0xff1b
	ksKPEnter   = 0xff8d // Enter на цифровом блоке
)

// keysymToRune переводит X11 keysym в печатаемую руну. Второй результат false —
// keysym непечатаемый (функциональные клавиши, модификаторы, Enter/Backspace):
// такие лок-экран в пароль не добавляет. Latin-1 keysym'ы 0x20..0xff совпадают с
// кодовыми точками Unicode один-в-один, а «прямые» Unicode-keysym'ы (0x01000000+,
// keysymdef.h) несут код символа в младших битах — так с клавиатуры приходят,
// например, кириллические буквы.
func keysymToRune(ks uint32) (rune, bool) {
	switch {
	case ks >= 0x20 && ks <= 0x7e: // печатаемый ASCII
		return rune(ks), true
	case ks >= 0xa0 && ks <= 0xff: // печатаемый Latin-1 (é, ü, …)
		return rune(ks), true
	case ks >= 0x01000100 && ks <= 0x0110ffff: // прямые Unicode-keysym'ы
		return rune(ks - 0x01000000), true
	}
	return 0, false // 0x7f (DEL), C1-управляющие, функциональные клавиши и т.п.
}

// effectiveKeysym выбирает keysym с учётом Shift и CapsLock из двух уровней
// раскладки клавиши (уровень 0 — без Shift, уровень 1 — с Shift). Это сознательно
// упрощённая модель X (полная учитывает группы и mode_switch), но её достаточно
// для ввода пароля: латиница, цифры, пунктуация и базовая кириллица.
func effectiveKeysym(unshifted, shifted uint32, shift, caps bool) uint32 {
	if shift {
		if shifted != 0 {
			return shifted
		}
		return unshifted
	}
	// CapsLock влияет только на латинские буквы (для цифр/пунктуации — нет).
	if caps && unshifted >= 'a' && unshifted <= 'z' {
		return unshifted - 0x20 // строчная → заглавная
	}
	return unshifted
}

// runesToLatin1 переводит строку в байты ISO8859-1 для ImageText8 — фолбэк, когда
// на сервере нашёлся только 8-битный шрифт «fixed» (Latin-1). Символы вне Latin-1
// (кириллица) таким шрифтом не отрисовать, поэтому заменяются на '?'. Когда есть
// Unicode-шрифт (iso10646-1), lockui_linux.go рисует через ImageText16 и сюда не
// заходит — тогда кириллица отображается корректно.
func runesToLatin1(s string) []byte {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r >= 0x20 && r <= 0xff {
			b = append(b, byte(r))
		} else {
			b = append(b, '?')
		}
	}
	return b
}
