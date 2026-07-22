//go:build !windows && !linux && (!darwin || !cgo)

// Package lockui — полноэкранный замок блокировки устройства (юзер-сессия).
package lockui

import "log/slog"

// Run — заглушка для прочих Unix (freebsd и т.п.) и для CGO=0-сборок macOS
// (кросс-компиляция без Cocoa, см. build-mac в Makefile): полноэкранный оверлей
// есть под Windows (lxn/walk), macOS с CGO (lockui_darwin.go) и Linux/X11
// (lockui_linux.go), здесь — только предупреждение в лог.
func Run(_ string, log *slog.Logger) {
	log.Warn("lock-screen: полноэкранный замок не реализован на этой ОС")
}
