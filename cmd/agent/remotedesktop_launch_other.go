//go:build !windows

package main

import (
	"errors"
	"log/slog"
)

// launchRemoteDesktopHelper на не-Windows не поддерживается: захват экрана/ввода
// реализован только для Windows (первый этап). Ошибку залогирует handleRemoteDesktop.
func launchRemoteDesktopHelper(_ string, _ []string, _ *slog.Logger) error {
	return errors.New("удалённый рабочий стол поддерживается только на Windows")
}
