//go:build !windows

package remotedesktop

import "context"

// requestConsent на не-Windows: захват экрана не поддерживается (newCapturer вернёт
// ошибку раньше), поэтому сюда не доходим. На всякий случай — отказ (fail-safe: без
// явного GUI-согласия сеанс не начинается).
func requestConsent(context.Context) bool { return false }
