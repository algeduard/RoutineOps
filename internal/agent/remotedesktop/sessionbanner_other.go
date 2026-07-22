//go:build !windows

package remotedesktop

// startSessionBanner на не-Windows — no-op (хелпер там не запускается). Возвращает
// пустую функцию остановки.
func startSessionBanner() func() { return func() {} }
