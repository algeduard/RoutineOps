//go:build windows

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// drainStatus сливает статусы службы, чтобы Execute не блокировался на отправке
// в неблокирующий канал статуса.
func drainStatus(s <-chan svc.Status) {
	go func() {
		for range s {
		}
	}()
}

// TestExecuteWorkExitCode проверяет ключевой инвариант self-update: когда work()
// завершается сам с ошибкой (запрос на перезапуск для применения нового exe),
// служба отдаёт SCM ненулевой код выхода — иначе FailureActions не сработают и
// агент останется лежать (полевой баг 2.2.2).
func TestExecuteWorkExitCode(t *testing.T) {
	tests := []struct {
		name     string
		workErr  error
		wantCode uint32
	}{
		{name: "work вышел с ошибкой (self-update) → ненулевой код", workErr: errors.New("перезапуск для применения самообновления"), wantCode: 1},
		{name: "work вышел без ошибки → код 0", workErr: nil, wantCode: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &handler{work: func(context.Context) error { return tc.workErr }}

			r := make(chan svc.ChangeRequest)
			s := make(chan svc.Status, 8)
			drainStatus(s)

			type result struct {
				svcSpecific bool
				code        uint32
			}
			res := make(chan result, 1)
			go func() {
				ss, code := h.Execute(nil, r, s)
				res <- result{ss, code}
			}()

			select {
			case got := <-res:
				if got.svcSpecific {
					t.Fatalf("svcSpecificEC=true, ожидался win32-код выхода")
				}
				if got.code != tc.wantCode {
					t.Fatalf("код выхода=%d, ожидался %d", got.code, tc.wantCode)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Execute не завершился после выхода work()")
			}
		})
	}
}

// TestExecuteStopReturnsZero проверяет, что штатный стоп по команде SCM остаётся
// успешным (код 0): фикс self-update не должен ломать ветку Stop/Shutdown.
func TestExecuteStopReturnsZero(t *testing.T) {
	// work блокируется до отмены контекста — имитация живого агента.
	h := &handler{work: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}

	r := make(chan svc.ChangeRequest)
	s := make(chan svc.Status, 8)
	drainStatus(s)

	type result struct {
		svcSpecific bool
		code        uint32
	}
	res := make(chan result, 1)
	go func() {
		ss, code := h.Execute(nil, r, s)
		res <- result{ss, code}
	}()

	// Даём службе выйти в Running и шлём Stop.
	r <- svc.ChangeRequest{Cmd: svc.Stop}

	select {
	case got := <-res:
		if got.svcSpecific || got.code != 0 {
			t.Fatalf("Stop вернул svcSpecific=%v code=%d, ожидался false/0", got.svcSpecific, got.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute не завершился после Stop")
	}
}
