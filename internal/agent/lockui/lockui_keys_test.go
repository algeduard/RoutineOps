package lockui

import "testing"

func TestKeysymToRune(t *testing.T) {
	cases := []struct {
		name string
		ks   uint32
		want rune
		ok   bool
	}{
		{"латиница A", 0x0041, 'A', true},
		{"латиница a", 0x0061, 'a', true},
		{"пробел", 0x0020, ' ', true},
		{"цифра", 0x0039, '9', true},
		{"верхняя граница ASCII", 0x007e, '~', true},
		{"DEL непечатаемый", 0x007f, 0, false},
		{"Latin-1 é", 0x00e9, 'é', true},
		{"C1 непечатаемый", 0x0085, 0, false},
		{"Return не руна", ksReturn, 0, false},
		{"Shift_L модификатор", 0xffe1, 0, false},
		{"прямой Unicode-keysym (кириллица й)", 0x01000439, 'й', true},
	}
	for _, c := range cases {
		got, ok := keysymToRune(c.ks)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: keysymToRune(%#x) = (%q,%v), ожидали (%q,%v)", c.name, c.ks, got, ok, c.want, c.ok)
		}
	}
}

func TestEffectiveKeysym(t *testing.T) {
	const a, A = 'a', 'A'
	const one, bang = '1', '!'
	cases := []struct {
		name               string
		unshifted, shifted uint32
		shift, caps        bool
		want               uint32
	}{
		{"без модификаторов", a, A, false, false, a},
		{"Shift даёт заглавную", a, A, true, false, A},
		{"CapsLock на букве — заглавная", a, A, false, true, A},
		{"CapsLock не влияет на цифру", one, bang, false, true, one},
		{"Shift на цифре — символ", one, bang, true, false, bang},
		{"Shift без уровня 1 — уровень 0", a, 0, true, false, a},
	}
	for _, c := range cases {
		if got := effectiveKeysym(c.unshifted, c.shifted, c.shift, c.caps); got != c.want {
			t.Errorf("%s: effectiveKeysym = %#x, ожидали %#x", c.name, got, c.want)
		}
	}
}

func TestRunesToLatin1(t *testing.T) {
	if got := string(runesToLatin1("aB9!")); got != "aB9!" {
		t.Errorf("ASCII: got %q", got)
	}
	if got := string(runesToLatin1("é")); got != "\xe9" {
		t.Errorf("Latin-1: got %q", got)
	}
	if got := string(runesToLatin1("абв")); got != "???" {
		t.Errorf("кириллица вне Latin-1 → '?': got %q", got)
	}
}
