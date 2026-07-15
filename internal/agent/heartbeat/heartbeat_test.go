package heartbeat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Floodww/RoutineOps/internal/agent/transport"
	pb "github.com/Floodww/RoutineOps/proto"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type recvMsg struct {
	task *pb.Task
	err  error
}

// fakeStream реализует transport.Stream (grpc.BidiStreamingClient). Неиспользуемые
// методы приходят из встроенного nil-grpc.ClientStream (в тестах не вызываются).
type fakeStream struct {
	grpc.ClientStream
	mu      sync.Mutex
	sent    []*pb.HeartbeatRequest
	sendErr error
	recv    chan recvMsg
}

func newFakeStream() *fakeStream { return &fakeStream{recv: make(chan recvMsg)} }

func (f *fakeStream) Send(hb *pb.HeartbeatRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, hb)
	return nil
}

func (f *fakeStream) Recv() (*pb.Task, error) {
	m := <-f.recv
	return m.task, m.err
}

func (f *fakeStream) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeStream) firstIP() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return ""
	}
	return f.sent[0].GetIpAddress()
}

var _ transport.Stream = (*fakeStream)(nil)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("таймаут ожидания: %s", msg)
}

func newHB(s *fakeStream, onTask func(*pb.Task), onConnect func()) *Heartbeater {
	return &Heartbeater{
		Interval:  10 * time.Millisecond,
		IPFunc:    func() string { return "192.0.2.7" },
		OnTask:    onTask,
		OnConnect: onConnect,
		Log:       discardLog(),
	}
}

// TestSessionSendsImmediateHeartbeatAndOnConnect: первый heartbeat уходит сразу
// (до тика), OnConnect вызывается ровно один раз, IP берётся из IPFunc.
func TestSessionSendsImmediateHeartbeatAndOnConnect(t *testing.T) {
	fs := newFakeStream()
	var connects int
	var mu sync.Mutex
	h := newHB(fs, nil, func() { mu.Lock(); connects++; mu.Unlock() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Session(ctx, fs) }()

	waitFor(t, func() bool { return fs.sentCount() >= 1 }, "первый heartbeat")
	if ip := fs.firstIP(); ip != "192.0.2.7" {
		t.Fatalf("IP в heartbeat = %q, want 192.0.2.7", ip)
	}
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return connects == 1 }, "OnConnect один раз")

	cancel()
	fs.recv <- recvMsg{err: io.EOF} // разблокировать recv-горутину
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Session вернул %v, ждали context.Canceled", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if connects != 1 {
		t.Fatalf("OnConnect вызван %d раз, ждали 1", connects)
	}
}

// TestSessionDeliversTasks: входящий Task попадает в OnTask.
func TestSessionDeliversTasks(t *testing.T) {
	fs := newFakeStream()
	got := make(chan *pb.Task, 1)
	h := newHB(fs, func(task *pb.Task) { got <- task }, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Session(ctx, fs) }()

	fs.recv <- recvMsg{task: &pb.Task{TaskId: "t-42"}}
	select {
	case task := <-got:
		if task.GetTaskId() != "t-42" {
			t.Fatalf("OnTask получил task_id=%q, want t-42", task.GetTaskId())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnTask не вызван на входящий Task")
	}

	fs.recv <- recvMsg{err: io.EOF}
	<-done
}

// TestSessionReturnsRecvError: ошибка Recv (обрыв стрима) → Session её возвращает,
// чтобы транспорт переподключился.
func TestSessionReturnsRecvError(t *testing.T) {
	fs := newFakeStream()
	h := newHB(fs, nil, nil)

	done := make(chan error, 1)
	go func() { done <- h.Session(context.Background(), fs) }()

	wantErr := errors.New("обрыв соединения")
	fs.recv <- recvMsg{err: wantErr}
	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("Session вернул %v, ждали %v", err, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Session не вернулся на ошибку Recv")
	}
}

// TestSessionSendErrorPropagates: ошибка первого Send → Session её возвращает
// сразу, OnConnect не вызывается.
func TestSessionSendErrorPropagates(t *testing.T) {
	fs := newFakeStream()
	fs.sendErr = errors.New("стрим закрыт на отправке")
	var connects int
	h := newHB(fs, nil, func() { connects++ })

	done := make(chan error, 1)
	go func() { done <- h.Session(context.Background(), fs) }()

	select {
	case err := <-done:
		if err == nil || err.Error() != "стрим закрыт на отправке" {
			t.Fatalf("Session вернул %v, ждали ошибку Send", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Session не вернулся на ошибку Send")
	}
	if connects != 0 {
		t.Fatalf("OnConnect не должен вызываться при ошибке первого Send (вызван %d)", connects)
	}
	fs.recv <- recvMsg{err: io.EOF} // разблокировать recv-горутину
}

// TestSessionPeriodicHeartbeats: по тикам уходит несколько heartbeat'ов.
func TestSessionPeriodicHeartbeats(t *testing.T) {
	fs := newFakeStream()
	h := newHB(fs, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Session(ctx, fs) }()

	waitFor(t, func() bool { return fs.sentCount() >= 3 }, "несколько периодических heartbeat")
	cancel()
	fs.recv <- recvMsg{err: io.EOF}
	<-done
}
