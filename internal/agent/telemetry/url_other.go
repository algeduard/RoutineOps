//go:build !windows

package telemetry

// Чтение URL активной вкладки браузера реализовано только на Windows (через UI
// Automation). На прочих платформах — заглушка: URL не собираются (как и весь app-usage,
// см. foreground_other.go). Расширение — отдельным этапом.
func readBrowserURL(uintptr) string { return "" }
