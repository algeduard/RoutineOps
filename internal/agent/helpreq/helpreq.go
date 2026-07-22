// Package helpreq — обращение сотрудника за помощью («Сообщить о проблеме» в
// трее). Окно помощи (юзер-сессия) кладёт заявку файлом, служба подхватывает и
// шлёт SubmitHelpRequest: у процессов юзер-сессии нет машинной mTLS-идентичности
// (session-0 isolation), поэтому RPC делает служба — тот же паттерн, что заявка
// на админ-права (internal/agent/admin/userrequest.go).
package helpreq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Floodww/RoutineOps/internal/agent/transport"
	pb "github.com/Floodww/RoutineOps/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// submitTimeout — потолок на один SubmitHelpRequest. Больше admin-овских 30с:
// вместе со скриншотом заявка весит сотни килобайт, а канал в поле бывает узкий.
const submitTimeout = 60 * time.Second

// UserRequest — заявка-файл. ScreenshotJPEG в JSON уезжает как base64
// (стандартное поведение encoding/json для []byte).
type UserRequest struct {
	Message        string `json:"message"`
	ScreenshotJPEG []byte `json:"screenshot_jpeg,omitempty"`
	Reporter       string `json:"reporter"` // логин консольного пользователя
	CreatedAt      int64  `json:"created_at"`
}

// Write кладёт заявку файлом (вызывает окно помощи по кнопке «Отправить»).
// Через tmp+rename: файл с base64-скриншотом весит сотни килобайт, и служба
// не должна увидеть его полузаписанным на своём тике. 0o600 — до отправки в
// файле лежит скриншот экрана сотрудника, другим пользователям машины он ни к
// чему (на Windows права best-effort, каталог закрыт ACL ProgramData).
func Write(path string, r UserRequest) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().Unix()
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename заявки: %w", err)
	}
	return nil
}

// Read читает заявку (вызывает служба). os.ErrNotExist = заявки нет.
func Read(path string) (UserRequest, error) {
	var r UserRequest
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(data, &r)
}

// Watch опрашивает файл-заявку: при появлении шлёт SubmitHelpRequest.
// Запускать в службе (у неё есть mTLS-идентичность).
func Watch(ctx context.Context, dialer *transport.Dialer, path string, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	send := func(r UserRequest) error { return submit(ctx, dialer, r, log) }
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			processRequest(path, send, log)
		}
	}
}

// processRequest обрабатывает один тик. Судьба файла-заявки (контракт как у
// admin.processRequest):
//   - успех            → удалить (доставлено);
//   - терминальный код → удалить (повтор бессмыслен);
//   - транзиент        → оставить (до-слать на следующем тике). Сюда входит и
//     ResourceExhausted — серверный кулдаун: заявка доедет после паузы.
func processRequest(path string, send func(UserRequest) error, log *slog.Logger) {
	req, err := Read(path)
	if err != nil {
		return // заявки нет (или файл недоступен)
	}
	switch sendErr := send(req); {
	case sendErr == nil:
		log.Info("helpreq: обращение за помощью отправлено",
			slog.String("reporter", req.Reporter), slog.Bool("screenshot", len(req.ScreenshotJPEG) > 0))
	case isTerminalSubmitErr(sendErr):
		log.Error("helpreq: обращение отклонено сервером окончательно — снимаю (повтор не поможет)",
			slog.String("code", status.Code(sendErr).String()),
			slog.Any("error", sendErr))
	default:
		if status.Code(sendErr) == codes.ResourceExhausted {
			log.Info("helpreq: серверный кулдаун обращений — повторю позже")
		} else {
			log.Error("helpreq: обращение не отправлено — повторю", slog.Any("error", sendErr))
		}
		return // транзиент: оставляем файл
	}
	if err := os.Remove(path); err != nil {
		log.Warn("helpreq: файл-заявка не удалён", slog.Any("error", err))
	}
}

func submit(ctx context.Context, dialer *transport.Dialer, r UserRequest, log *slog.Logger) error {
	conn, err := dialer.Dial()
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, submitTimeout)
	defer cancel()

	resp, err := pb.NewAgentServiceClient(conn).SubmitHelpRequest(ctx, &pb.SubmitHelpRequestRequest{
		Message:        r.Message,
		ScreenshotJpeg: r.ScreenshotJPEG,
		CreatedAt:      r.CreatedAt,
		Reporter:       r.Reporter,
	})
	if err != nil {
		return err
	}
	log.Info("helpreq: обращение зарегистрировано на сервере",
		slog.String("request_id", resp.GetRequestId()))
	return nil
}

// isTerminalSubmitErr — сервер вынес окончательный вердикт, повтор бессмыслен
// (та же семантика, что isTerminalRequestErr у admin-заявок):
//   - NotFound        — устройство не существует (снято/призрак);
//   - InvalidArgument — заявка не пройдёт валидацию ни при каком повторе.
//
// ResourceExhausted (кулдаун) и всё остальное — транзиент.
func isTerminalSubmitErr(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}
