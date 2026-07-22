package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/Floodww/RoutineOps/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// jpegBytes — минимальный валидный префикс JPEG (SOI) + мусор.
func jpegBytes(n int) []byte {
	b := make([]byte, n)
	b[0], b[1] = 0xFF, 0xD8
	return b
}

func TestSubmitHelpRequest_OK(t *testing.T) {
	db := newDB(t)
	bot := newMockNotifier()
	gw := newGWWithBot(t, db, bot)
	ctx, fp := makeCertCtx(t, "dev-help-ok")
	registerDevice(t, db, "dev-help-ok", fp)

	resp, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{
		Message:        "не открывается 1С <b>жирным</b>",
		ScreenshotJpeg: jpegBytes(64),
		CreatedAt:      time.Now().Unix(),
		Reporter:       `OFFICE\ivanov`,
	})
	if err != nil {
		t.Fatalf("SubmitHelpRequest: %v", err)
	}
	if resp.RequestId == "" {
		t.Fatal("пустой request_id")
	}

	rows, err := db.ListHelpRequests(context.Background(), "", "new")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.ID == resp.RequestId {
			found = true
			if !r.HasScreenshot || r.Reporter != `OFFICE\ivanov` {
				t.Errorf("строка обращения неверна: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("обращение не найдено в списке")
	}

	// Telegram-уведомление уходит в горутине; текст сотрудника экранирован (HTML).
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("уведомление не отправлено")
	case <-bot.notified:
	}
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.Messages) != 1 || !strings.Contains(bot.Messages[0], "&lt;b&gt;жирным&lt;/b&gt;") {
		t.Errorf("текст уведомления: %q", bot.Messages)
	}
}

func TestSubmitHelpRequest_UnknownDevice(t *testing.T) {
	db := newDB(t)
	gw := newGW(t, db)
	ctx, _ := makeCertCtx(t, "dev-help-ghost") // серт есть, устройства в БД нет

	_, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{Message: "кто я"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
}

// Кулдаун: второе обращение сразу за первым — ResourceExhausted (транзиент для
// файла-заявки агента: она останется и доедет после паузы).
func TestSubmitHelpRequest_Cooldown(t *testing.T) {
	db := newDB(t)
	gw := newGW(t, db)
	ctx, fp := makeCertCtx(t, "dev-help-cd")
	registerDevice(t, db, "dev-help-cd", fp)

	if _, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{Message: "первое"}); err != nil {
		t.Fatalf("первое обращение: %v", err)
	}
	_, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{Message: "спам"})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestSubmitHelpRequest_Validation(t *testing.T) {
	db := newDB(t)
	gw := newGW(t, db)
	ctx, fp := makeCertCtx(t, "dev-help-val")
	registerDevice(t, db, "dev-help-val", fp)

	// Пустое обращение — терминально (InvalidArgument).
	if _, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("пустое: code = %v, want InvalidArgument", status.Code(err))
	}
	// Не-JPEG вместо скриншота — терминально.
	if _, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{
		Message: "х", ScreenshotJpeg: []byte("<script>"),
	}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("не-JPEG: code = %v, want InvalidArgument", status.Code(err))
	}
	// Слишком большой скриншот — терминально.
	if _, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{
		Message: "х", ScreenshotJpeg: jpegBytes(2<<20 + 1),
	}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("большой скриншот: code = %v, want InvalidArgument", status.Code(err))
	}
}

// Сверхдлинный текст режется до лимита, а не отклоняется: InvalidArgument снял бы
// файл-заявку у агента, и сотрудник потерял бы обращение целиком.
func TestSubmitHelpRequest_TruncatesLongMessage(t *testing.T) {
	db := newDB(t)
	gw := newGW(t, db)
	ctx, fp := makeCertCtx(t, "dev-help-long")
	registerDevice(t, db, "dev-help-long", fp)

	long := strings.Repeat("щ", 5000)
	resp, err := gw.SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{Message: long})
	if err != nil {
		t.Fatalf("SubmitHelpRequest: %v", err)
	}
	rows, _ := db.ListHelpRequests(context.Background(), "", "")
	for _, r := range rows {
		if r.ID == resp.RequestId {
			if got := len([]rune(r.Message)); got != 4000 {
				t.Errorf("длина сообщения = %d рун, want 4000", got)
			}
			return
		}
	}
	t.Fatal("обращение не найдено")
}
