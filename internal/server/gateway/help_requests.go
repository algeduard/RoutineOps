package gateway

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"strconv"
	"time"

	pb "github.com/Floodww/RoutineOps/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Лимиты обращения. Скриншот: агент даунскейлит и жмёт JPEG сам, 2МБ — защита
// от сломанного/чужого клиента (лимит gRPC 4МБ — с запасом). Текст: 4000 рун —
// больше в окно трея не влезает осмысленно; лишнее режем, а не отклоняем
// (InvalidArgument терминален для файла-заявки — сотрудник потерял бы обращение).
const (
	helpMaxScreenshotBytes = 2 << 20
	helpMaxMessageRunes    = 4000
	helpDefaultCooldownSec = 60
)

// jpegSOI — сигнатура начала JPEG (Start Of Image).
var jpegSOI = []byte{0xFF, 0xD8}

// SubmitHelpRequest — обращение за помощью с устройства («Сообщить о проблеме»
// в трее). Ошибки по контракту файла-заявки (см. admin.processRequest):
// терминальные (NotFound/InvalidArgument) снимают заявку, транзиентные
// (Unavailable/ResourceExhausted) оставляют на повтор — поэтому кулдаун
// возвращает ResourceExhausted: заявка доедет следующим тиком после паузы.
func (g *Gateway) SubmitHelpRequest(ctx context.Context, req *pb.SubmitHelpRequestRequest) (*pb.SubmitHelpRequestResponse, error) {
	_, fingerprint, err := extractCertInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "cert: %v", err)
	}
	deviceID, err := g.db.GetDeviceIDByFingerprint(ctx, fingerprint)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup device: %v", err)
	}
	if deviceID == "" {
		return nil, status.Errorf(codes.NotFound, "device not found")
	}

	message := req.Message
	if r := []rune(message); len(r) > helpMaxMessageRunes {
		message = string(r[:helpMaxMessageRunes])
	}
	if message == "" && len(req.ScreenshotJpeg) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "empty help request")
	}
	if len(req.ScreenshotJpeg) > helpMaxScreenshotBytes {
		return nil, status.Errorf(codes.InvalidArgument,
			"screenshot too large: %d bytes (max %d)", len(req.ScreenshotJpeg), helpMaxScreenshotBytes)
	}
	if len(req.ScreenshotJpeg) > 0 && !bytes.HasPrefix(req.ScreenshotJpeg, jpegSOI) {
		return nil, status.Errorf(codes.InvalidArgument, "screenshot is not a JPEG")
	}

	// Кулдаун на устройство: не даём одной машине заспамить БД и Telegram.
	// Ошибку чтения НЕ считаем поводом отклонить обращение (fail-open) — кулдаун
	// это анти-спам, а не контроль доступа.
	cooldown := time.Duration(helpDefaultCooldownSec) * time.Second
	if s, _ := g.db.GetSystemSetting(ctx, "help_request_cooldown_seconds"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			cooldown = time.Duration(v) * time.Second
		}
	}
	if last, err := g.db.LastHelpRequestAt(ctx, deviceID); err == nil && !last.IsZero() {
		if wait := cooldown - time.Since(last); wait > 0 {
			return nil, status.Errorf(codes.ResourceExhausted,
				"help request cooldown: retry in %s", wait.Round(time.Second))
		}
	}

	createdAt := g.clampAgentTime("created_at", req.CreatedAt)
	id, err := g.db.CreateHelpRequest(ctx, deviceID, req.Reporter, message, req.ScreenshotJpeg, createdAt)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "create help request: %v", err)
	}
	g.logger.Info("help request received", "device_id", deviceID, "request_id", id,
		"reporter", req.Reporter, "screenshot", len(req.ScreenshotJpeg) > 0)

	if g.bot != nil {
		hostname, _ := g.db.GetDeviceHostname(ctx, deviceID)
		reporter := req.Reporter
		if reporter == "" {
			reporter = "не указан"
		}
		// Текст сотрудника — недоверенный ввод, экранируем (parse_mode=HTML у бота).
		preview := message
		if r := []rune(preview); len(r) > 300 {
			preview = string(r[:300]) + "…"
		}
		note := ""
		if len(req.ScreenshotJpeg) > 0 {
			note = "\n📎 Приложен скриншот"
		}
		text := fmt.Sprintf("🆘 <b>Обращение за помощью</b>\nУстройство: <code>%s</code>\nПользователь: %s\n\n%s%s\n\nОткройте панель MDM, раздел «Обращения».",
			html.EscapeString(hostname), html.EscapeString(reporter), html.EscapeString(preview), note)
		go g.bot.NotifyITAdmins(context.Background(), text)
	}

	return &pb.SubmitHelpRequestResponse{RequestId: id}, nil
}
