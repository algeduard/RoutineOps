//go:build !windows

package helpui

import (
	"errors"
	"log/slog"
)

// Run: окно обращения пока только на Windows (walk). macOS — следующим шагом
// (нужен нативный диалог); трей на других платформах пункт меню не показывает,
// так что сюда попадает только ручной запуск `agent help-window`.
func Run(_ string, _ *slog.Logger) error {
	return errors.New("окно обращения за помощью поддерживается только на Windows")
}
