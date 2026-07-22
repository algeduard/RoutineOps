// Package remotedesktop реализует агент-хелпер удалённого рабочего стола: процесс,
// запускаемый службой в активной интерактивной сессии пользователя (там, где есть
// доступ к экрану и вводу). Хелпер открывает bidi-стрим RemoteDesktop к серверу,
// шлёт кадры экрана вверх и применяет события ввода, приходящие вниз. Сервер
// проксирует поток в WebSocket админ-браузера. См. docs/remote-desktop-design.md.
//
// Захват экрана и инъекция ввода платформо-зависимы (Windows: GDI BitBlt +
// SendInput). На неподдерживаемых платформах newCapturer возвращает ошибку —
// хелпер сообщает статус и завершается.
package remotedesktop

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Floodww/RoutineOps/internal/agent/transport"
	pb "github.com/Floodww/RoutineOps/proto"
)

const (
	// frameInterval — период захвата кадра (MVP ~8 fps). Компромисс между
	// плавностью и нагрузкой на CPU/канал; адаптивный fps — следующий этап.
	frameInterval = 125 * time.Millisecond
	// jpegQuality — качество JPEG кадра (0..100). Низкое ради размера/скорости.
	jpegQuality = 50
)

// capturer захватывает виртуальный экран. Реализация платформо-зависима.
type capturer interface {
	// Bounds возвращает размеры источника (весь виртуальный экран, все мониторы).
	Bounds() (w, h int)
	// Capture снимает текущий кадр в image.RGBA (каналы уже RGBA).
	Capture() (*image.RGBA, error)
	Close()
}

// injector применяет события ввода к системе. Реализация платформо-зависима.
type injector interface {
	Inject(ev *pb.RDInputEvent)
}

// RunHelper — точка входа хелпера. Открывает стрим RemoteDesktop, представляется
// сервером выданным session_id (RDHello), затем гоняет цикл захвата и применяет
// входящий ввод. Возвращает nil при штатном завершении (STOP/закрытие стрима).
func RunHelper(ctx context.Context, dialer *transport.Dialer, sessionID string, log *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := dialer.Dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewAgentServiceClient(conn)

	stream, err := client.RemoteDesktop(ctx)
	if err != nil {
		return err
	}

	// Захват создаём ДО Hello, чтобы знать размеры экрана. Но Hello шлём ВСЕГДА
	// (даже при отказе захвата) — иначе сервер не свяжет сессию и последующий ERROR
	// не дойдёт до браузера (gateway требует Hello первым сообщением), а WS будет
	// ждать полный таймаут и покажет generic-ошибку.
	cap, capErr := newCapturer()
	var w, h int
	if capErr == nil {
		w, h = cap.Bounds()
		defer cap.Close()
	}

	if err := stream.Send(&pb.RemoteDesktopClientMsg{
		Payload: &pb.RemoteDesktopClientMsg_Hello{Hello: &pb.RDHello{
			SessionId:    sessionID,
			ScreenWidth:  int32(w),
			ScreenHeight: int32(h),
		}},
	}); err != nil {
		return err
	}

	if capErr != nil {
		log.Warn("remote desktop helper: захват недоступен", slog.Any("error", capErr))
		_ = stream.Send(statusMsg(pb.RDStatusCode_RD_STATUS_CODE_ERROR, "capture unavailable: "+capErr.Error()))
		gracefulClose(stream) // дать статусу флашнуться до conn.Close (recv-горутина ещё не запущена)
		return capErr
	}

	inj := newInjector(w, h)
	// granted гейтит применение ввода: до согласия админ НЕ должен управлять машиной,
	// хотя браузер уже «активен» (сервер шлёт ready по Hello, до согласия).
	var granted atomic.Bool

	// recv-горутина запускается ДО согласия: (а) ловит смерть стрима/сессии и
	// отменяет ctx (что закроет и диалог согласия), (б) до согласия ввод игнорирует.
	go func() {
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				cancel()
				return
			}
			if in := msg.GetInput(); in != nil {
				if granted.Load() && inj != nil {
					inj.Inject(in)
				}
				continue
			}
			if ctl := msg.GetControl(); ctl != nil {
				if ctl.GetAction() == pb.RDControlAction_RD_CONTROL_ACTION_STOP {
					cancel()
					return
				}
			}
		}
	}()

	// Согласие пользователя на устройстве (attended). Показываем модальный запрос в
	// сессии пользователя ДО отправки кадров: без явного «Разрешить» сеанс не
	// начинается (fail-safe). Отменяется при смерти стрима/сессии (ctx).
	if !requestConsent(ctx) {
		log.Info("remote desktop helper: пользователь отклонил доступ", slog.String("session_id", sessionID))
		_ = stream.Send(statusMsg(pb.RDStatusCode_RD_STATUS_CODE_USER_DENIED, "пользователь отклонил удалённый доступ"))
		// Дать статусу дойти до сервера: half-close + ждать закрытия стрима
		// (recv-горутина отменит ctx), но не дольше пары секунд.
		_ = stream.CloseSend()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
		return nil
	}
	granted.Store(true)

	// Плашка «идёт сеанс» на всё время сессии: пользователь видит активность не
	// только в момент согласия, но и пока админ подключён.
	stopBanner := startSessionBanner()
	defer stopBanner()

	if err := stream.Send(statusMsg(pb.RDStatusCode_RD_STATUS_CODE_READY, "")); err != nil {
		return err // сессия уже мертва (WS закрыт / таймаут) — не поднимаем захват зря
	}
	log.Info("remote desktop helper: сессия установлена", slog.String("session_id", sessionID),
		slog.Int("w", w), slog.Int("h", h))

	// Цикл захвата: кадр → JPEG → RDVideoFrame.
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	var seq int64
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			img, cerr := cap.Capture()
			if cerr != nil {
				log.Warn("remote desktop: захват кадра", slog.Any("error", cerr))
				continue
			}
			data, eerr := encodeJPEG(img)
			if eerr != nil {
				continue
			}
			seq++
			frame := &pb.RemoteDesktopClientMsg{Payload: &pb.RemoteDesktopClientMsg_Frame{Frame: &pb.RDVideoFrame{
				Seq:      seq,
				TsUnixMs: time.Now().UnixMilli(),
				Format:   pb.RDImageFormat_RD_IMAGE_FORMAT_JPEG,
				Width:    int32(img.Rect.Dx()),
				Height:   int32(img.Rect.Dy()),
				Data:     data,
				KeyFrame: true, // MVP: каждый кадр полный
			}}}
			if err := stream.Send(frame); err != nil {
				return err
			}
		}
	}
}

// gracefulClose закрывает отправляющую сторону стрима и ограниченно ждёт его
// завершения — чтобы уже отправленный терминальный статус (ERROR) успел флашнуться
// до жёсткого conn.Close (иначе фрейм может отброситься). Вызывать только когда
// recv-горутина ещё НЕ запущена (иначе два Recv на одном стриме).
func gracefulClose(stream pb.AgentService_RemoteDesktopClient) {
	_ = stream.CloseSend()
	done := make(chan struct{})
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				break
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func encodeJPEG(img *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func statusMsg(code pb.RDStatusCode, msg string) *pb.RemoteDesktopClientMsg {
	return &pb.RemoteDesktopClientMsg{Payload: &pb.RemoteDesktopClientMsg_Status{Status: &pb.RDStatus{
		Code: code, Message: msg,
	}}}
}
