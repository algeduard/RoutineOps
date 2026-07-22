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

	cap, err := newCapturer()
	if err != nil {
		// Платформа/окружение без захвата — сообщаем серверу и выходим.
		_ = stream.Send(statusMsg(pb.RDStatusCode_RD_STATUS_CODE_ERROR, "capture unavailable: "+err.Error()))
		return err
	}
	defer cap.Close()
	w, h := cap.Bounds()

	// Hello: связываем стрим с ожидающей сессией (session_id выдан сервером).
	if err := stream.Send(&pb.RemoteDesktopClientMsg{
		Payload: &pb.RemoteDesktopClientMsg_Hello{Hello: &pb.RDHello{
			SessionId:    sessionID,
			ScreenWidth:  int32(w),
			ScreenHeight: int32(h),
		}},
	}); err != nil {
		return err
	}
	_ = stream.Send(statusMsg(pb.RDStatusCode_RD_STATUS_CODE_READY, ""))
	log.Info("remote desktop helper: сессия установлена", slog.String("session_id", sessionID),
		slog.Int("w", w), slog.Int("h", h))

	inj := newInjector(w, h)

	// recv-горутина: ввод/управление от сервера.
	go func() {
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				cancel()
				return
			}
			if in := msg.GetInput(); in != nil {
				if inj != nil {
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
