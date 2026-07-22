// Package remotedesktop содержит серверный мост сессий удалённого рабочего стола.
//
// Он связывает два конца одной сессии, встречающихся на сервере:
//   - gRPC bidi-стрим RemoteDesktop, который открывает АГЕНТ-ХЕЛПЕР из
//     интерактивной сессии устройства (кадры экрана вверх, ввод вниз);
//   - WebSocket админ-браузера (кадры в браузер, ввод из браузера).
//
// Сервер к агенту дозвониться не может (агент за NAT, см. ADR-1/ADR-5), поэтому
// сессию инициирует админ (открывает WS), сервер выдаёт session_id и командой
// Task{remote_desktop:START} по Connect-стриму просит службу поднять хелпер; тот
// открывает RemoteDesktop-стрим и первым сообщением возвращает session_id
// (RDHello), чем сервер и связывает конкретный браузер с конкретным стримом.
// См. docs/remote-desktop-design.md.
package remotedesktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	pb "github.com/Floodww/RoutineOps/proto"
)

// Буферы каналов. Видео дропается (важен свежий кадр, а не полный ряд), поэтому
// буфер маленький: при переполнении выкидываем самый старый кадр. Ввод не
// дропаем молча — буфер с запасом, порядок key-down/up обязан сохраняться.
const (
	toBrowserBuffer = 4
	toAgentBuffer   = 256
)

// ToBrowser — то, что сервер отдаёт в WebSocket. Ровно одно поле непусто.
type ToBrowser struct {
	Frame  *pb.RDVideoFrame // бинарный кадр экрана (JPEG)
	Status *pb.RDStatus     // статус/ошибка хелпера
	Ready  *Ready           // метаданные готовности (размер экрана)
}

// Ready — первое служебное сообщение в браузер: агент поднял сессию.
type Ready struct {
	Width  int32
	Height int32
}

// Session — один активный (или устанавливаемый) сеанс удалённого стола.
type Session struct {
	ID       string
	DeviceID string // device_id устройства-источника (для скоупинга агентского стрима)

	toBrowser chan ToBrowser                  // agent→browser: кадры/статусы (drop-old)
	toAgent   chan *pb.RemoteDesktopServerMsg // browser→agent: ввод/управление
	hello     chan *pb.RDHello                // доставка RDHello ровно один раз

	agentAttached bool // защищено mu моста при AttachAgent (одна попытка привязки)

	closeOnce sync.Once
	done      chan struct{}
}

func newSession(id, deviceID string) *Session {
	return &Session{
		ID:        id,
		DeviceID:  deviceID,
		toBrowser: make(chan ToBrowser, toBrowserBuffer),
		toAgent:   make(chan *pb.RemoteDesktopServerMsg, toAgentBuffer),
		hello:     make(chan *pb.RDHello, 1),
		done:      make(chan struct{}),
	}
}

// PushToBrowser кладёт кадр/статус для браузера. При переполнении буфера
// выбрасывает самый старый элемент (video-friendly: копить лаг хуже, чем терять
// кадр). После Close — no-op.
func (s *Session) PushToBrowser(m ToBrowser) {
	for {
		select {
		case <-s.done:
			return
		case s.toBrowser <- m:
			return
		default:
			// буфер полон — снять самый старый и повторить
			select {
			case <-s.toBrowser:
			default:
			}
		}
	}
}

// ToBrowserCh — канал для чтения WebSocket-стороной.
func (s *Session) ToBrowserCh() <-chan ToBrowser { return s.toBrowser }

// PushToAgent кладёт ввод/управление для агента-хелпера. Возвращает false, если
// сессия закрыта или буфер переполнен (ввод под таким давлением допустимо
// уронить, но НЕ блокировать recv-петлю WebSocket).
func (s *Session) PushToAgent(m *pb.RemoteDesktopServerMsg) bool {
	// Сначала ДЕТЕРМИНИРОВАННО проверяем закрытие: если объединить проверку done с
	// отправкой в одном select, при закрытой сессии и свободном буфере select
	// выбрал бы случайно и мог бы принять ввод в мёртвую сессию.
	select {
	case <-s.done:
		return false
	default:
	}

	// mouse_move — позиционное событие: под давлением его допустимо УРОНИТЬ (важен
	// свежий указатель, а не полный ряд). Роняем именно НОВОЕ движение, НЕ трогая
	// очередь — иначе выкинули бы из головы буфера чужое state-transition событие.
	if in := m.GetInput(); in != nil && in.GetType() == pb.RDInputType_RD_INPUT_TYPE_MOUSE_MOVE {
		select {
		case s.toAgent <- m:
			return true
		default:
			return false // буфер полон — тихо роняем это движение
		}
	}

	// Всё остальное (нажатия/отпускания клавиш и кнопок, колесо, управление) —
	// state-transition: ронять НЕЛЬЗЯ, иначе на устройстве застрянет зажатый
	// модификатор/кнопка (key-down доставлен, а key-up потерян). Доставляем
	// блокирующе — лучше короткая задержка ввода, чем застрявшая клавиша; если
	// сессия закрылась, выходим.
	select {
	case <-s.done:
		return false
	case s.toAgent <- m:
		return true
	}
}

// ToAgentCh — канал для чтения gRPC-стороной (отправка агенту).
func (s *Session) ToAgentCh() <-chan *pb.RemoteDesktopServerMsg { return s.toAgent }

// SignalHello доставляет RDHello WebSocket-стороне, ждущей в WaitHello. Идемпотентно.
func (s *Session) SignalHello(h *pb.RDHello) {
	select {
	case s.hello <- h:
	default:
	}
}

// WaitHello блокируется до прихода RDHello от агента, отмены ctx или Close.
func (s *Session) WaitHello(ctx context.Context) (*pb.RDHello, bool) {
	select {
	case h := <-s.hello:
		return h, true
	case <-ctx.Done():
		return nil, false
	case <-s.done:
		return nil, false
	}
}

// Done закрывается при завершении сессии (любой стороной).
func (s *Session) Done() <-chan struct{} { return s.done }

// Close завершает сессию идемпотентно, разблокируя обе стороны.
func (s *Session) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// Bridge — реестр активных сессий, общий для WebSocket-хендлера (api) и
// gRPC-хендлера RemoteDesktop (gateway).
type Bridge struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// New создаёт пустой мост.
func New() *Bridge {
	return &Bridge{sessions: make(map[string]*Session)}
}

// Create регистрирует новую сессию для устройства и возвращает её (со свежим
// случайным session_id). Вызывается WebSocket-стороной при открытии соединения.
func (b *Bridge) Create(deviceID string) *Session {
	id := newSessionID()
	s := newSession(id, deviceID)
	b.mu.Lock()
	b.sessions[id] = s
	b.mu.Unlock()
	return s
}

// AttachAgent привязывает пришедший gRPC-стрим агента к ожидающей сессии по
// session_id. Проверяет, что сессия принадлежит тому же устройству (deviceID из
// mTLS-серта хелпера обязан совпасть с тем, для которого сессия создана) — чужой
// агент не подключится к чужой сессии. Возвращает сессию и true при успехе.
func (b *Bridge) AttachAgent(sessionID, deviceID string) (*Session, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sessions[sessionID]
	if !ok || s.DeviceID != deviceID || s.agentAttached {
		return nil, false
	}
	s.agentAttached = true
	return s, true
}

// Get возвращает сессию по id.
func (b *Bridge) Get(sessionID string) (*Session, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sessions[sessionID]
	return s, ok
}

// Remove закрывает и удаляет сессию из реестра. Идемпотентно.
func (b *Bridge) Remove(sessionID string) {
	b.mu.Lock()
	s, ok := b.sessions[sessionID]
	if ok {
		delete(b.sessions, sessionID)
	}
	b.mu.Unlock()
	if ok {
		s.Close()
	}
}

func newSessionID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
