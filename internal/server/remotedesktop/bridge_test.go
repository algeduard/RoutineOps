package remotedesktop

import (
	"context"
	"testing"
	"time"

	pb "github.com/Floodww/RoutineOps/proto"
)

func TestCreateGetRemove(t *testing.T) {
	b := New()
	s := b.Create("dev-1")
	if s.ID == "" || s.DeviceID != "dev-1" {
		t.Fatalf("некорректная сессия: %+v", s)
	}
	got, ok := b.Get(s.ID)
	if !ok || got != s {
		t.Fatalf("Get не вернул созданную сессию")
	}
	// Два вызова Create дают разные id.
	if s2 := b.Create("dev-1"); s2.ID == s.ID {
		t.Fatalf("session_id должен быть уникальным")
	}

	b.Remove(s.ID)
	if _, ok := b.Get(s.ID); ok {
		t.Fatalf("Remove не удалил сессию")
	}
	select {
	case <-s.Done():
	default:
		t.Fatalf("Remove должен закрыть сессию (Done)")
	}
	// Повторный Remove идемпотентен.
	b.Remove(s.ID)
}

func TestAttachAgent(t *testing.T) {
	b := New()
	s := b.Create("dev-1")

	// Чужой device_id не привязывается.
	if _, ok := b.AttachAgent(s.ID, "dev-OTHER"); ok {
		t.Fatalf("привязка с чужим device_id должна быть отклонена")
	}
	// Неизвестная сессия.
	if _, ok := b.AttachAgent("nope", "dev-1"); ok {
		t.Fatalf("привязка к неизвестной сессии должна быть отклонена")
	}
	// Верная привязка — успех.
	if _, ok := b.AttachAgent(s.ID, "dev-1"); !ok {
		t.Fatalf("верная привязка должна пройти")
	}
	// Повторная привязка той же сессии — отклонена (agentAttached).
	if _, ok := b.AttachAgent(s.ID, "dev-1"); ok {
		t.Fatalf("повторная привязка должна быть отклонена")
	}
}

func TestPushToBrowserDropsOldest(t *testing.T) {
	b := New()
	s := b.Create("dev-1")
	// Заполняем буфер сверх ёмкости — PushToBrowser не должен блокировать и обязан
	// сохранить самые свежие кадры (видео-семантика).
	total := toBrowserBuffer + 10
	for i := 0; i < total; i++ {
		seq := int64(i)
		s.PushToBrowser(ToBrowser{Frame: &pb.RDVideoFrame{Seq: seq}})
	}
	// В канале должно быть не больше ёмкости, и последний элемент — самый свежий.
	got := len(s.ToBrowserCh())
	if got > toBrowserBuffer {
		t.Fatalf("в буфере %d элементов, ожидали <= %d", got, toBrowserBuffer)
	}
	var last int64 = -1
	for i := 0; i < got; i++ {
		m := <-s.ToBrowserCh()
		last = m.Frame.GetSeq()
	}
	if last != int64(total-1) {
		t.Fatalf("последний кадр seq=%d, ожидали самый свежий %d", last, total-1)
	}
}

func TestWaitHello(t *testing.T) {
	b := New()
	s := b.Create("dev-1")

	// Приходит hello — WaitHello его возвращает.
	go s.SignalHello(&pb.RDHello{SessionId: s.ID, ScreenWidth: 1920, ScreenHeight: 1080})
	h, ok := s.WaitHello(context.Background())
	if !ok || h.GetScreenWidth() != 1920 {
		t.Fatalf("WaitHello не вернул hello: ok=%v h=%v", ok, h)
	}

	// Отмена контекста — WaitHello выходит с false.
	s2 := b.Create("dev-2")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok := s2.WaitHello(ctx); ok {
		t.Fatalf("WaitHello должен вернуть false по отмене контекста")
	}
}

func TestPushToAgentAfterClose(t *testing.T) {
	b := New()
	s := b.Create("dev-1")
	s.Close()
	if s.PushToAgent(&pb.RemoteDesktopServerMsg{}) {
		t.Fatalf("PushToAgent после Close должен вернуть false")
	}
}
