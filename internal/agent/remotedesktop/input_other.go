//go:build !windows

package remotedesktop

// newInjector на не-Windows возвращает nil — инъекция ввода не поддерживается.
// (На этих платформах хелпер и так не стартует: newCapturer отдаёт ошибку.)
func newInjector(w, h int) injector { return nil }
