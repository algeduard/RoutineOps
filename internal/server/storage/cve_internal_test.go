package storage

import "testing"

// Юнит-тест version-логики матчинга — без БД (функция чистая). Внутренний пакет, чтобы
// достучаться до неэкспортируемой cveVersionVulnerable.
func TestCVEVersionVulnerable(t *testing.T) {
	cases := []struct {
		constraint, installed string
		want                  bool
	}{
		{"", "1.0.0", true}, // пусто = уязвима любая версия продукта
		{"*", "9.9", true},  // звёздочка = любая
		{"<2.0.0", "1.5.0", true},
		{"<2.0.0", "2.0.0", false}, // ровно граница — не «меньше»
		{"<2.0.0", "2.1", false},
		{"<=2.0.0", "2.0.0", true},
		{"=1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.3", true}, // голое = точное совпадение
		{"=1.2.3", "1.2.4", false},
		{">1.0", "1.0.1", true},
		{">=1.0", "1.0", true},
		{"<2.0.0", "", false},          // версия неизвестна + конкретное ограничение → не выдумываем
		{"<10.0", "9.0", true},         // ЧИСЛОВОЕ сравнение (9<10), не лексикографическое
		{"<2.0.0", "1.10.0", true},     // 1.10 < 2.0 покомпонентно
		{"<1.10.0", "1.9.0", true},     // 1.9 < 1.10 (лексикографически было бы наоборот)
		{"<1.2.0", "1.2", false},       // 1.2 == 1.2.0 — не меньше
		{"<2.0.0", "1.5.0-beta", true}, // pre-release-суффикс отбрасывается
	}
	for _, c := range cases {
		if got := cveVersionVulnerable(c.constraint, c.installed); got != c.want {
			t.Errorf("cveVersionVulnerable(%q, %q) = %v, want %v", c.constraint, c.installed, got, c.want)
		}
	}
}

func TestNormalizeCVESeverity(t *testing.T) {
	cases := map[string]string{
		"LOW": "low", "Medium": "medium", " high ": "high", "CRITICAL": "critical",
		"": "medium", "bogus": "medium", // неизвестное → medium (безопасный дефолт)
	}
	for in, want := range cases {
		if got := normalizeCVESeverity(in); got != want {
			t.Errorf("normalizeCVESeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
