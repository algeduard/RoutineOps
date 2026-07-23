//go:build enterprise

package api

import "testing"

func TestRedactWebhookURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://user:pass@siem.example/hook", "https://siem.example/hook"},    // userinfo убран
		{"https://siem.example/hook?token=SECRET", "https://siem.example/hook"}, // query-токен убран
		{"https://siem.example/hook#frag", "https://siem.example/hook"},         // fragment убран
		{"https://user:pass@siem.example/hook?token=SECRET", "https://siem.example/hook"},
		{"https://siem.example/path", "https://siem.example/path"}, // чистый — как есть
		{"  https://siem.example/x  ", "https://siem.example/x"},   // тримится
	}
	for _, c := range cases {
		if got := redactWebhookURL(c.in); got != c.want {
			t.Errorf("redactWebhookURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
