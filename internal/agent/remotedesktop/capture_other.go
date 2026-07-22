//go:build !windows

package remotedesktop

import "errors"

// newCapturer на не-Windows не поддерживается (захват экрана реализован только для
// Windows на первом этапе). Хелпер сообщит серверу статус и завершится.
func newCapturer() (capturer, error) {
	return nil, errors.New("захват экрана поддерживается только на Windows")
}
