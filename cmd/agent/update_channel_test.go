package main

import (
	"net/url"
	"testing"
)

// updateURLWithDevice дописывает ?device=<CN> к URL манифеста, сохраняя существующие
// query-параметры; пустой CN/URL оставляет URL как есть.
func TestUpdateURLWithDevice(t *testing.T) {
	// Пустой deviceID — URL не трогаем (серт ещё не подняли → сервер даст stable).
	if got := updateURLWithDevice("https://host/api/v1/agent/version", ""); got != "https://host/api/v1/agent/version" {
		t.Errorf("пустой deviceID изменил URL: %q", got)
	}
	// Пустой URL — тоже без изменений.
	if got := updateURLWithDevice("", "cn-1"); got != "" {
		t.Errorf("пустой URL изменился: %q", got)
	}

	// device добавляется и корректно кодируется.
	got := updateURLWithDevice("https://host/api/v1/agent/version", "device CN/±")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("результат не парсится: %v", err)
	}
	if u.Query().Get("device") != "device CN/±" {
		t.Errorf("device = %q, хотим %q", u.Query().Get("device"), "device CN/±")
	}

	// Существующие query-параметры сохраняются.
	got = updateURLWithDevice("https://host/api/v1/agent/version?foo=bar", "cn-9")
	u, _ = url.Parse(got)
	if u.Query().Get("foo") != "bar" || u.Query().Get("device") != "cn-9" {
		t.Errorf("потеряли/переписали query: foo=%q device=%q", u.Query().Get("foo"), u.Query().Get("device"))
	}
}
