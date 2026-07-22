package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/registry"
	"github.com/Floodww/RoutineOps/internal/server/remotedesktop"
	pb "github.com/Floodww/RoutineOps/proto"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// helloWaitTimeout — сколько ждём, пока служба поднимет хелпер и тот откроет
// gRPC-стрим (RDHello). Устройство онлайн, но запуск процесса в интерактивной
// сессии + dial сервера занимают время; при простое браузер получит понятную
// ошибку вместо вечного «подключение».
const helloWaitTimeout = 20 * time.Second

// wsReadLimit — потолок на входящее WS-сообщение (события ввода — крошечные).
const wsReadLimit = 32 * 1024

// WithRemoteDesktop включает удалённый рабочий стол: registry шлёт START-команду
// подключённому устройству, bridge связывает WebSocket админа со стримом хелпера.
// Монтирует WebSocket-ручку в подгруппу it_admin (как WithAdminRoutes).
func WithRemoteDesktop(reg *registry.Registry, bridge *remotedesktop.Bridge) RouterOption {
	return func(h *Handler, r chi.Router) {
		h.registry = reg
		h.rdBridge = bridge
		r.Group(func(ar chi.Router) {
			ar.Use(h.requireRole("it_admin"))
			ar.Get("/devices/{id}/remote-desktop", h.remoteDesktopWS)
		})
	}
}

// setRDUnattendedRequest — тело PUT /devices/{id}/rd-unattended. Указатель, чтобы
// отличить отсутствие поля от явного false (нельзя случайно выключить из-за пустого JSON).
type setRDUnattendedRequest struct {
	Unattended *bool `json:"unattended"`
}

type rdUnattendedResponse struct {
	Unattended bool `json:"unattended"`
}

// setDeviceRDUnattended включает/выключает opt-in unattended-доступ удалённого
// рабочего стола для устройства (миграция 039, devices.rd_unattended). it_admin +
// requireHuman (см. маршрут) + аудит: включение снимает consent-ГЕЙТ для будущих
// сеансов этого устройства — чувствительное действие, оно должно быть решением
// ЧЕЛОВЕКА (не сервисного токена) и прослеживаемо. Что unattended НЕ трогает: плашку
// «идёт сеанс» на устройстве и аудит старта/стопа сеансов — они остаются всегда
// (unattended убирает подтверждение, не видимость).
func (h *Handler) setDeviceRDUnattended(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req setRDUnattendedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Unattended == nil {
		http.Error(w, "unattended is required", http.StatusBadRequest)
		return
	}
	found, err := h.db.SetRDUnattended(r.Context(), id, *req.Unattended)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	claims := r.Context().Value(claimsKey).(*jwtClaims)
	h.audit(r.Context(), claims.UserID, claims.Email, "set_rd_unattended", "device", id,
		map[string]bool{"unattended": *req.Unattended})
	writeJSON(w, http.StatusOK, rdUnattendedResponse{Unattended: *req.Unattended})
}

// wsInputEvent — событие ввода из браузера (browser→server). Координаты мыши
// нормализованы 0..1 по видимой области кадра (устойчивы к масштабу/ресайзу).
type wsInputEvent struct {
	T      string  `json:"t"`                // mouse_move|mouse_down|mouse_up|wheel|key
	X      float64 `json:"x,omitempty"`      // 0..1
	Y      float64 `json:"y,omitempty"`      // 0..1
	Button int32   `json:"button,omitempty"` // 0=left,1=right,2=middle
	Delta  int32   `json:"delta,omitempty"`  // колесо
	Code   int32   `json:"code,omitempty"`   // Windows virtual-key
	Down   bool    `json:"down,omitempty"`   // клавиша нажата/отпущена
	Ctrl   bool    `json:"ctrl,omitempty"`
	Alt    bool    `json:"alt,omitempty"`
	Shift  bool    `json:"shift,omitempty"`
	Meta   bool    `json:"meta,omitempty"`
}

// remoteDesktopWS — WebSocket-эндпоинт удалённого рабочего стола (только it_admin).
// Хореография: создать сессию → попросить устройство поднять хелпер (START по
// Connect-стриму) → дождаться RDHello → мостить кадры (в браузер) и ввод (агенту).
func (h *Handler) remoteDesktopWS(w http.ResponseWriter, r *http.Request) {
	if h.rdBridge == nil || h.registry == nil {
		http.Error(w, "remote desktop disabled", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	// Резолвим CN устройства (ключ registry / mTLS-идентичность агента).
	cn, err := h.db.GetDeviceCN(ctx, id)
	if err != nil || cn == "" {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	if !h.registry.Connected(cn) {
		http.Error(w, "device is offline", http.StatusConflict)
		return
	}

	claims, _ := ctx.Value(claimsKey).(*jwtClaims)
	var actorID, actorEmail string
	if claims != nil {
		actorID, actorEmail = claims.UserID, claims.Email
	}

	// Политика unattended-доступа (opt-in на устройство, миграция 039). Когда включена —
	// сообщаем агенту в START-команде, что для ЭТОЙ сессии можно пропустить запрос
	// согласия пользователя. Плашка «идёт сеанс» и аудит старта/стопа при этом
	// СОХРАНЯЮТСЯ (unattended снимает подтверждение, не видимость). Ошибка чтения или
	// неизвестное устройство → unattended=false (fail-safe: без явного opt-in согласие
	// НИКОГДА не пропускается).
	unattended, _, err := h.db.GetRDUnattended(ctx, id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Регистрируем сессию и просим устройство поднять хелпер захвата.
	sess := h.rdBridge.Create(cn)
	defer h.rdBridge.Remove(sess.ID)

	startTask := &pb.Task{
		TaskId: "rd-" + sess.ID,
		RemoteDesktop: &pb.RemoteDesktopCommand{
			SessionId:  sess.ID,
			Action:     pb.RemoteDesktopAction_REMOTE_DESKTOP_ACTION_START,
			Unattended: unattended,
		},
	}
	if !h.registry.Send(cn, startTask) {
		http.Error(w, "device busy", http.StatusConflict)
		return
	}

	// Аудит старта включает признак unattended: факт «сеанс шёл БЕЗ запроса согласия»
	// должен быть виден при разборе (наравне с opt-in-переключением политики).
	h.audit(ctx, actorID, actorEmail, "remote_desktop_start", "device", id,
		map[string]any{"session_id": sess.ID, "unattended": unattended})
	started := time.Now()

	// Апгрейд в WebSocket. Origin проверяется по умолчанию (same-origin) — плюс
	// запрос уже прошёл jwtMiddleware+requireRole, так что это второй рубеж.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.Warn("remote desktop: ws accept", "session_id", sess.ID, "err", err)
		h.audit(context.WithoutCancel(ctx), actorID, actorEmail, "remote_desktop_end", "device", id,
			map[string]any{"session_id": sess.ID, "reason": "ws_accept_failed"})
		return
	}
	c.SetReadLimit(wsReadLimit)

	endReason := "closed"
	defer func() {
		sess.Close()
		_ = c.Close(websocket.StatusNormalClosure, "")
		h.audit(context.WithoutCancel(ctx), actorID, actorEmail, "remote_desktop_end", "device", id,
			map[string]any{
				"session_id":   sess.ID,
				"reason":       endReason,
				"duration_sec": int(time.Since(started).Seconds()),
			})
	}()

	// Ждём, пока хелпер подключится и пришлёт RDHello (размеры экрана).
	waitCtx, cancelWait := context.WithTimeout(ctx, helloWaitTimeout)
	hello, ok := sess.WaitHello(waitCtx)
	cancelWait()
	if !ok {
		endReason = "agent_no_show"
		_ = writeWSJSON(ctx, c, map[string]any{"type": "error", "message": "агент не поднял сессию"})
		return
	}

	// Сообщаем браузеру, что сессия готова, размеры источника и режим доступа. Флаг
	// unattended даёт вебу показать явную пометку «сеанс без запроса согласия».
	_ = writeWSJSON(ctx, c, map[string]any{
		"type":       "ready",
		"w":          hello.GetScreenWidth(),
		"h":          hello.GetScreenHeight(),
		"unattended": unattended,
	})

	// browser→agent: читаем события ввода и кладём в мост.
	go func() {
		for {
			typ, data, rerr := c.Read(ctx)
			if rerr != nil {
				sess.Close()
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var ev wsInputEvent
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			if msg := inputEventToProto(&ev); msg != nil {
				sess.PushToAgent(msg)
			}
		}
	}()

	// agent→browser: кадры (binary) и статусы (text). Завершаемся при закрытии сессии.
	for {
		select {
		case <-sess.Done():
			return
		case <-ctx.Done():
			return
		case m := <-sess.ToBrowserCh():
			switch {
			case m.Frame != nil:
				if err := c.Write(ctx, websocket.MessageBinary, m.Frame.GetData()); err != nil {
					endReason = "ws_write_failed"
					return
				}
			case m.Status != nil:
				_ = writeWSJSON(ctx, c, map[string]any{
					"type":    "status",
					"code":    int(m.Status.GetCode()),
					"message": m.Status.GetMessage(),
				})
				if m.Status.GetCode() == pb.RDStatusCode_RD_STATUS_CODE_USER_DENIED {
					endReason = "user_denied"
					return
				}
			}
		}
	}
}

func writeWSJSON(ctx context.Context, c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, b)
}

// inputEventToProto маппит событие ввода из браузера в RDInputEvent. nil — событие
// неизвестного типа (игнорируется).
func inputEventToProto(ev *wsInputEvent) *pb.RemoteDesktopServerMsg {
	in := &pb.RDInputEvent{
		X: ev.X, Y: ev.Y, Button: ev.Button, WheelDelta: ev.Delta,
		KeyCode: ev.Code, KeyDown: ev.Down,
		Ctrl: ev.Ctrl, Alt: ev.Alt, Shift: ev.Shift, Meta: ev.Meta,
	}
	switch ev.T {
	case "mouse_move":
		in.Type = pb.RDInputType_RD_INPUT_TYPE_MOUSE_MOVE
	case "mouse_down":
		in.Type = pb.RDInputType_RD_INPUT_TYPE_MOUSE_DOWN
	case "mouse_up":
		in.Type = pb.RDInputType_RD_INPUT_TYPE_MOUSE_UP
	case "wheel":
		in.Type = pb.RDInputType_RD_INPUT_TYPE_WHEEL
	case "key":
		in.Type = pb.RDInputType_RD_INPUT_TYPE_KEY
	default:
		return nil
	}
	return &pb.RemoteDesktopServerMsg{Payload: &pb.RemoteDesktopServerMsg_Input{Input: in}}
}
