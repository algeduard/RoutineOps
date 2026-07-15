package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	pb "github.com/Floodww/RoutineOps/proto"
	"google.golang.org/grpc"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeClient реализует pb.AgentServiceClient, перехватывая только Ack/Report.
// Остальные методы наследуются от nil-интерфейса и не должны вызываться.
type fakeClient struct {
	pb.AgentServiceClient
	mu          sync.Mutex
	acked       []string
	results     []*pb.TaskResult
	lockReports []*pb.ReportLockStatusRequest
	ackErr      error
	repErr      error
}

func (f *fakeClient) ReportLockStatus(_ context.Context, in *pb.ReportLockStatusRequest, _ ...grpc.CallOption) (*pb.ReportLockStatusResponse, error) {
	f.mu.Lock()
	f.lockReports = append(f.lockReports, in)
	f.mu.Unlock()
	return &pb.ReportLockStatusResponse{}, nil
}

func (f *fakeClient) lockReportsCopy() []*pb.ReportLockStatusRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*pb.ReportLockStatusRequest(nil), f.lockReports...)
}

func (f *fakeClient) AckTaskReceived(_ context.Context, in *pb.TaskReceivedAck, _ ...grpc.CallOption) (*pb.TaskReceivedAckResponse, error) {
	f.mu.Lock()
	f.acked = append(f.acked, in.GetTaskId())
	f.mu.Unlock()
	return &pb.TaskReceivedAckResponse{}, f.ackErr
}

func (f *fakeClient) ReportTaskResult(_ context.Context, in *pb.TaskResult, _ ...grpc.CallOption) (*pb.TaskResultAck, error) {
	f.mu.Lock()
	f.results = append(f.results, in)
	f.mu.Unlock()
	return &pb.TaskResultAck{}, f.repErr
}

func (f *fakeClient) ackedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.acked...)
}

func (f *fakeClient) resultsCopy() []*pb.TaskResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*pb.TaskResult(nil), f.results...)
}

// newTestExecutor собирает Executor с фейковым connect. statePath="" → дедуп в памяти.
// Возвращает также счётчик вызовов connect.
func newTestExecutor(t *testing.T, fc *fakeClient) (*Executor, *int) {
	t.Helper()
	e := NewExecutor(nil, quietLog(), "")
	var connectCalls int
	var mu sync.Mutex
	e.connect = func() (pb.AgentServiceClient, func(), error) {
		mu.Lock()
		connectCalls++
		mu.Unlock()
		return fc, func() {}, nil
	}
	return e, &connectCalls
}

// Задача без task_id отбрасывается без соединения.
func TestSubmit_EmptyTaskID_Skipped(t *testing.T) {
	fc := &fakeClient{}
	e, connectCalls := newTestExecutor(t, fc)
	e.connect = func() (pb.AgentServiceClient, func(), error) {
		t.Fatal("connect не должен вызываться для задачи без task_id")
		return nil, nil, nil
	}
	e.Submit(&pb.Task{})
	e.Shutdown()
	if *connectCalls != 0 {
		t.Errorf("connect вызван %d раз для пустого task_id", *connectCalls)
	}
}

// После Shutdown новые задачи отклоняются.
func TestSubmit_NotAccepting_Rejected(t *testing.T) {
	fc := &fakeClient{}
	e, connectCalls := newTestExecutor(t, fc)
	e.Shutdown() // accepting=false
	e.Submit(&pb.Task{TaskId: "t-after-shutdown", Platform: "macos", ScriptContent: "echo hi"})
	if *connectCalls != 0 {
		t.Errorf("задача принята после Shutdown (connect вызван %d раз)", *connectCalls)
	}
}

// Happy-path: новая задача подтверждается (ack) и результат уходит как SUCCESS.
func TestHandle_NewTask_AcksAndReportsSuccess(t *testing.T) {
	fc := &fakeClient{}
	e, _ := newTestExecutor(t, fc)

	e.Submit(&pb.Task{TaskId: "t-ok", Platform: "macos", ScriptContent: "echo hi"})
	e.Shutdown() // дожидается завершения in-flight задач

	if acks := fc.ackedIDs(); len(acks) != 1 || acks[0] != "t-ok" {
		t.Fatalf("ожидался 1 ack 't-ok', получено %v", acks)
	}
	res := fc.resultsCopy()
	if len(res) != 1 {
		t.Fatalf("ожидался 1 результат, получено %d", len(res))
	}
	if res[0].GetStatus() != pb.TaskStatus_TASK_STATUS_SUCCESS {
		t.Errorf("статус = %v, ожидался SUCCESS", res[0].GetStatus())
	}
}

// Падающий скрипт → результат ERROR с заполненным error_log.
func TestHandle_ScriptError_ReportsError(t *testing.T) {
	fc := &fakeClient{}
	e, _ := newTestExecutor(t, fc)

	e.Submit(&pb.Task{TaskId: "t-fail", Platform: "macos", ScriptContent: "exit 3"})
	e.Shutdown()

	res := fc.resultsCopy()
	if len(res) != 1 {
		t.Fatalf("ожидался 1 результат, получено %d", len(res))
	}
	if res[0].GetStatus() != pb.TaskStatus_TASK_STATUS_ERROR {
		t.Errorf("статус = %v, ожидался ERROR", res[0].GetStatus())
	}
	if res[0].GetErrorLog() == "" {
		t.Error("error_log пуст для упавшего скрипта")
	}
}

// Повторная доставка той же задачи: ack дважды, но скрипт выполняется один раз.
func TestHandle_DuplicateTask_AcksButRunsOnce(t *testing.T) {
	fc := &fakeClient{}
	e, _ := newTestExecutor(t, fc)

	task := &pb.Task{TaskId: "t-dup", Platform: "macos", ScriptContent: "echo hi"}
	e.Submit(task)
	e.Submit(task)
	e.Shutdown()

	if acks := fc.ackedIDs(); len(acks) != 2 {
		t.Errorf("ожидалось 2 ack (обе доставки), получено %d", len(acks))
	}
	if res := fc.resultsCopy(); len(res) != 1 {
		t.Errorf("ожидался 1 результат (вторая доставка — дедуп), получено %d", len(res))
	}
}

// Конкурентные Submit, гонящиеся с Shutdown: проверяем синхронизацию accepting/wg
// (запускать с -race). Инвариант — никакой паники/гонки; число результатов не
// превышает числа ack, а ack — числа поданных задач.
func TestConcurrentSubmitDuringShutdown(t *testing.T) {
	fc := &fakeClient{}
	e, _ := newTestExecutor(t, fc)

	const n = 40
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			e.Submit(&pb.Task{
				TaskId:        fmt.Sprintf("t-%d", i),
				Platform:      "macos",
				ScriptContent: "echo hi",
			})
		}(i)
	}
	// Останавливаем исполнитель, пока часть сабмитов ещё в полёте.
	go e.Shutdown()

	wg.Wait()
	e.Shutdown() // дренируем оставшиеся принятые задачи (идемпотентно)

	acks := len(fc.ackedIDs())
	res := len(fc.resultsCopy())
	if acks > n {
		t.Fatalf("ack больше числа поданных задач: acks=%d n=%d", acks, n)
	}
	if res > acks {
		t.Fatalf("результатов больше, чем ack: res=%d acks=%d", res, acks)
	}
}

// Ошибки ack/report лишь логируются — handle не должен паниковать и доводит
// задачу до конца.
func TestHandle_AckAndReportErrors_AreLogged(t *testing.T) {
	fc := &fakeClient{ackErr: errors.New("ack rpc failed"), repErr: errors.New("report rpc failed")}
	e, _ := newTestExecutor(t, fc)

	e.Submit(&pb.Task{TaskId: "t-rpcerr", Platform: "macos", ScriptContent: "echo hi"})
	e.Shutdown()

	if len(fc.ackedIDs()) != 1 {
		t.Errorf("ack должен был быть вызван несмотря на ошибку, вызовов %d", len(fc.ackedIDs()))
	}
	if len(fc.resultsCopy()) != 1 {
		t.Errorf("report должен был быть вызван несмотря на ошибку, вызовов %d", len(fc.resultsCopy()))
	}
}

// Ошибка соединения: задача не выполняется, ack не шлётся, seen не помечается
// (сервер передоставит) — следующая доставка выполнит её. Зовём handle напрямую,
// чтобы не завершать исполнитель Shutdown'ом между попытками.
func TestHandle_ConnectError_NoAckNoSeen(t *testing.T) {
	fc := &fakeClient{}
	e := NewExecutor(nil, quietLog(), "")
	fail := true
	e.connect = func() (pb.AgentServiceClient, func(), error) {
		if fail {
			return nil, nil, errors.New("dial failed")
		}
		return fc, func() {}, nil
	}

	task := &pb.Task{TaskId: "t-retry", Platform: "macos", ScriptContent: "echo hi"}
	e.handle(task) // connect падает
	if len(fc.ackedIDs()) != 0 {
		t.Fatal("ack отправлен несмотря на ошибку соединения")
	}

	// Связь восстановилась — повторная доставка должна выполниться (seen не помечен).
	fail = false
	e.handle(task)
	if res := fc.resultsCopy(); len(res) != 1 {
		t.Errorf("после восстановления связи задача должна выполниться один раз, результатов %d", len(res))
	}
}
