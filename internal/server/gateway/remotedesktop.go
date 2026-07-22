package gateway

import (
	"errors"
	"io"

	"github.com/Floodww/RoutineOps/internal/server/remotedesktop"
	pb "github.com/Floodww/RoutineOps/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RemoteDesktop — серверная сторона bidi-стрима удалённого рабочего стола. Его
// открывает АГЕНТ-ХЕЛПЕР из интерактивной сессии устройства после START-команды.
// Первым сообщением обязан прийти RDHello с session_id, выданным сервером при
// открытии WebSocket админом; по нему сервер связывает этот стрим с ожидающим
// браузером. Дальше: кадры/статусы (агент→мост→WS), ввод/управление (WS→мост→агент).
// Идентичность устройства — из mTLS-серта (ADR-1); чужой агент к чужой сессии не
// подключится (AttachAgent сверяет device_id). См. docs/remote-desktop-design.md.
func (g *Gateway) RemoteDesktop(stream pb.AgentService_RemoteDesktopServer) error {
	if g.rd == nil {
		return status.Error(codes.Unimplemented, "remote desktop disabled")
	}
	deviceID, _, err := extractCertInfo(stream.Context())
	if err != nil {
		g.logger.Warn("remote desktop rejected: cert info", "err", err)
		return status.Errorf(codes.Unauthenticated, "cert: %v", err)
	}

	// Первое сообщение — обязательно Hello (связывание сессии).
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || hello.GetSessionId() == "" {
		return status.Error(codes.InvalidArgument, "expected RDHello with session_id first")
	}

	sess, ok := g.rd.AttachAgent(hello.GetSessionId(), deviceID)
	if !ok {
		g.logger.Warn("remote desktop: unknown/mismatched session",
			"device_id", deviceID, "session_id", hello.GetSessionId())
		return status.Error(codes.NotFound, "unknown or mismatched session")
	}
	// Выход из хендлера рвёт и WS-сторону (она слушает sess.Done()); удаление из
	// реестра делает WS-хендлер (владелец жизненного цикла сессии).
	defer sess.Close()

	g.logger.Info("remote desktop stream attached", "device_id", deviceID, "session_id", sess.ID)

	// Разбудить WS-сторону: агент готов, передаём размеры экрана.
	sess.SignalHello(hello)

	// send-горутина: ввод/управление из моста → агенту-хелперу.
	go func() {
		for {
			select {
			case <-sess.Done():
				return
			case <-stream.Context().Done():
				return
			case msg := <-sess.ToAgentCh():
				if err := stream.Send(msg); err != nil {
					g.logger.Warn("remote desktop: send to agent failed",
						"session_id", sess.ID, "err", err)
					sess.Close()
					return
				}
			}
		}
	}()

	// recv в отдельной горутине: stream.Recv() блокирующий, а завершение может
	// прийти со стороны WS (sess.Done()) — тогда возвращаемся из хендлера, стрим
	// закрывается и разблокирует Recv.
	recvErr := make(chan error, 1)
	go func() {
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				recvErr <- rerr
				return
			}
			if f := msg.GetFrame(); f != nil {
				sess.PushToBrowser(remotedesktop.ToBrowser{Frame: f})
				continue
			}
			if st := msg.GetStatus(); st != nil {
				sess.PushToBrowser(remotedesktop.ToBrowser{Status: st})
				continue
			}
			// повторный Hello или пустой payload — игнор
		}
	}()

	select {
	case <-sess.Done():
		return nil
	case <-stream.Context().Done():
		sess.Close()
		return stream.Context().Err()
	case rerr := <-recvErr:
		sess.Close()
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		return rerr
	}
}
