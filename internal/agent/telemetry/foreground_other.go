//go:build !windows

package telemetry

import (
	"errors"
	"time"
)

// App-usage (foreground/idle) на первом этапе — только Windows. Для macOS/Linux —
// заглушки: сэмплер активности не запускается (appUsageSupported()==false), а сами
// функции возвращают «не поддерживается», как это делают другие платформо-зависимые
// фичи агента через *_other.go. Расширение — отдельным этапом.
var errAppUsageUnsupported = errors.New("app-usage collection not supported on this platform")

// appUsageSupported — app-usage на этой платформе не реализован.
func appUsageSupported() bool { return false }

func foregroundApp(bool, bool) (string, string, string, error) {
	return "", "", "", errAppUsageUnsupported
}

func idleDuration() (time.Duration, error) { return 0, errAppUsageUnsupported }
