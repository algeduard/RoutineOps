package command

import (
	"sync"
	"testing"

	pb "github.com/Floodww/RoutineOps/proto"
)

// Инвариант агентской стороны: значение unattended из RemoteDesktopCommand доезжает
// до лаунчера хелпера БЕЗ изменений. Лаунчер прокидывает его флагом -unattended в
// хелпер, который на его основании пропускает (или нет) запрос согласия. Значит
// «согласие пропускается ТОЛЬКО когда сервер прислал unattended=true» держится на этой
// передаче: агент сам пропуск не инициирует.
func TestHandleRemoteDesktop_UnattendedPropagatesToLauncher(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unattended bool
	}{
		{"unattended", true},
		{"attended", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewExecutor(nil, quietLog(), "", nil)

			var (
				mu          sync.Mutex
				calls       int
				gotSID      string
				gotUnattend bool
			)
			e.SetRemoteDesktopLauncher(func(sid string, unattended bool) error {
				mu.Lock()
				defer mu.Unlock()
				calls++
				gotSID = sid
				gotUnattend = unattended
				return nil
			})

			e.handle(&pb.Task{
				TaskId: "rd-1",
				RemoteDesktop: &pb.RemoteDesktopCommand{
					SessionId:  "sess-1",
					Action:     pb.RemoteDesktopAction_REMOTE_DESKTOP_ACTION_START,
					Unattended: tc.unattended,
				},
			})

			mu.Lock()
			defer mu.Unlock()
			if calls != 1 {
				t.Fatalf("лаунчер вызван %d раз, ожидался 1", calls)
			}
			if gotSID != "sess-1" {
				t.Fatalf("session_id = %q, ожидался sess-1", gotSID)
			}
			if gotUnattend != tc.unattended {
				t.Fatalf("unattended = %v, ожидалось %v (значение команды)", gotUnattend, tc.unattended)
			}
		})
	}
}

// STOP-команда хелпер не запускает (teardown — по закрытию стрима сервером), поэтому
// лаунчер не должен вызываться и unattended роли не играет.
func TestHandleRemoteDesktop_StopDoesNotLaunch(t *testing.T) {
	e := NewExecutor(nil, quietLog(), "", nil)
	var calls int
	e.SetRemoteDesktopLauncher(func(_ string, _ bool) error { calls++; return nil })

	e.handle(&pb.Task{
		TaskId: "rd-stop",
		RemoteDesktop: &pb.RemoteDesktopCommand{
			SessionId:  "sess-1",
			Action:     pb.RemoteDesktopAction_REMOTE_DESKTOP_ACTION_STOP,
			Unattended: true,
		},
	})
	if calls != 0 {
		t.Fatalf("лаунчер вызван %d раз на STOP, ожидался 0", calls)
	}
}
