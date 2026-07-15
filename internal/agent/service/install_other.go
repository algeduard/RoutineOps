//go:build !darwin && !windows && !linux

package service

import "errors"

var errUnsupported = errors.New("установка службы поддерживается только на macOS, Windows и Linux (на этой ОС используйте run)")

func Install(cfg Config) error { return errUnsupported }

func Uninstall() error { return errUnsupported }

// Harden — no-op: ужесточение DACL службы только для Windows.
func Harden() error { return nil }
