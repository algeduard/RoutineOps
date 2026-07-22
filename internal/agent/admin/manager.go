// Package admin реализует агентскую сторону временных прав администратора (Этап 4):
// запрос прав сотрудником, поллинг статуса (FetchAdminStatus), применение/снятие
// прав через PrivilegeManager и отчёт серверу (ReportAdminAccess).
//
// Применение прав изолировано за интерфейсом PrivilegeManager (платформенные
// реализации dseditgroup/net localgroup в priv_*.go), чтобы логику можно было
// тестировать без изменения реальной системы.
package admin

import (
	"context"
	"log/slog"
	"time"

	"github.com/Floodww/RoutineOps/internal/agent/collector"
	"github.com/Floodww/RoutineOps/internal/agent/outbox"
	"github.com/Floodww/RoutineOps/internal/agent/transport"
	pb "github.com/Floodww/RoutineOps/proto"
	"google.golang.org/protobuf/proto"
)

const reportTimeout = 30 * time.Second

// PrivilegeManager выдаёт/снимает админ-права пользователю ОС.
//
// IsAdmin сообщает, состоит ли пользователь в группе администраторов ПРЯМО СЕЙЧАС —
// нужен, чтобы при выдаче временных прав снять снимок прежнего состояния и НЕ снять
// у пользователя его собственные постоянные права при истечении гранта.
type PrivilegeManager interface {
	Grant(user string) error
	Revoke(user string) error
	IsAdmin(user string) (bool, error)
}

// dryRunPriv — не трогает систему, только логирует (для тестов/демо без root).
type dryRunPriv struct{ log *slog.Logger }

func (d dryRunPriv) Grant(user string) error {
	d.log.Info("admin(dry-run): выдача прав пропущена", slog.String("user", user))
	return nil
}
func (d dryRunPriv) Revoke(user string) error {
	d.log.Info("admin(dry-run): снятие прав пропущено", slog.String("user", user))
	return nil
}
func (dryRunPriv) IsAdmin(string) (bool, error) { return false, nil }

// Manager поллит статус прав и применяет его локально.
type Manager struct {
	interval    time.Duration
	log         *slog.Logger
	priv        PrivilegeManager
	consoleUser func() string                                               // текущий вошедший пользователь
	fetch       func(context.Context) (*pb.FetchAdminStatusResponse, error) // статус с сервера
	report      func(context.Context, *pb.ReportAdminAccessRequest) error   // отчёт серверу

	// snapshot снимает текущий список установленного ПО — для аудита дельты за
	// сессию админ-прав. Инъекция ради тестируемости; прод — collector.InstalledSoftware.
	snapshot func() []*pb.SoftwareItem

	// Состояние выданных прав.
	grantedUser     string
	grantedExpires  time.Time
	grantedWasAdmin bool   // был ли пользователь админом ДО гранта — тогда права при отзыве НЕ снимаем
	lastReqID       string // последняя заявка, которую уже обработали (выдали) — не выдавать повторно
	// grantBaseline — снимок ПО на момент выдачи прав; на снятии diff с текущим даёт
	// что установлено/удалено за сессию. Только в памяти (рестарт агента ре-базлайнит).
	grantBaseline []*pb.SoftwareItem
}

// EnqueueFunc ставит отчёт в устойчивую очередь доставки (outbox).
type EnqueueFunc func(kind string, data []byte) error

// NewManager собирает Manager с боевыми зависимостями (gRPC через dialer, ОС-права).
// dryRun=true — права не применяются к системе (логируются), остальной флоу полный.
//
// FetchAdminStatus — поллинг: при обрыве просто повторяется на следующем тике,
// поэтому идёт напрямую через dialer. ReportAdminAccess (аудит выдачи/снятия
// прав) терять нельзя — он durably ставится в outbox и до-сылается после связи.
func NewManager(dialer *transport.Dialer, enqueue EnqueueFunc, interval time.Duration, log *slog.Logger, dryRun bool) *Manager {
	priv := newOSPrivilegeManager()
	if dryRun {
		priv = dryRunPriv{log: log}
	}
	return &Manager{
		interval:    interval,
		log:         log,
		priv:        priv,
		consoleUser: osConsoleUser,
		snapshot:    defaultSoftwareSnapshot,
		fetch: func(ctx context.Context) (*pb.FetchAdminStatusResponse, error) {
			conn, err := dialer.Dial()
			if err != nil {
				return nil, err
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(ctx, reportTimeout)
			defer cancel()
			return pb.NewAgentServiceClient(conn).FetchAdminStatus(ctx, &pb.FetchAdminStatusRequest{})
		},
		report: func(_ context.Context, req *pb.ReportAdminAccessRequest) error {
			data, err := proto.Marshal(req)
			if err != nil {
				return err
			}
			return enqueue(outbox.KindAdmin, data)
		},
	}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

func (m *Manager) poll(ctx context.Context) {
	// 1) Локальные причины снять права — работают даже без связи с сервером.
	if m.grantedUser != "" {
		if m.consoleUser() != m.grantedUser {
			m.revoke(ctx, "пользователь вышел из системы")
		} else if !m.grantedExpires.IsZero() && time.Now().After(m.grantedExpires) {
			m.revoke(ctx, "истёк срок прав")
		}
	}

	resp, err := m.fetch(ctx)
	if err != nil {
		m.log.Error("admin: FetchAdminStatus", slog.Any("error", err))
		return
	}
	status := resp.GetStatus()
	reqID := resp.GetRequestId()
	// expires_at==0 — бессрочная заявка (действует до логаута). Держим её как
	// нулевое время, иначе time.Unix(0,0)=1970 и локальная проверка истечения
	// ниже сняла бы права на следующем же тике (флип-флоп).
	var expires time.Time
	if resp.GetExpiresAt() != 0 {
		expires = time.Unix(resp.GetExpiresAt(), 0)
	}
	// Заявка одобрена и действует прямо сейчас.
	approvedNow := status == pb.AdminAccessStatus_ADMIN_ACCESS_STATUS_APPROVED &&
		reqID != "" && (expires.IsZero() || time.Now().Before(expires))

	// Сервер больше не подтверждает нашу выданную заявку: закрыта/истекла/заменена
	// ИЛИ активной заявки нет вовсе (status=UNSPECIFIED, request_id="") — снимаем права.
	if m.grantedUser != "" && (!approvedNow || reqID != m.lastReqID) {
		m.revoke(ctx, "сервер не подтверждает права (status="+status.String()+")")
	}

	// Новая одобренная заявка → выдаём.
	if approvedNow && reqID != m.lastReqID {
		m.grant(ctx, reqID, expires)
	}
}

func (m *Manager) grant(ctx context.Context, reqID string, expires time.Time) {
	user := m.consoleUser()
	if user == "" {
		m.log.Warn("admin: нет вошедшего пользователя — права не выданы", slog.String("request_id", reqID))
		return
	}
	// Снимок прежнего членства ДО выдачи: если пользователь уже был админом (например,
	// это основная учётка машины), при истечении гранта его права снимать НЕЛЬЗЯ.
	wasAdmin, err := m.priv.IsAdmin(user)
	if err != nil {
		// Не смогли определить прежнее состояние — безопаснее считать, что пользователь
		// уже был админом, и НЕ снимать права при отзыве. Лучше оставить лишний грант,
		// чем демоутнуть легитимного администратора.
		m.log.Warn("admin: не удалось определить прежнее членство — считаю пользователя админом (при отзыве прав не сниму)",
			slog.String("user", user), slog.Any("error", err))
		wasAdmin = true
	}
	if err := m.priv.Grant(user); err != nil {
		m.log.Error("admin: выдача прав", slog.String("user", user), slog.Any("error", err))
		return
	}
	m.grantedUser = user
	m.grantedExpires = expires
	m.grantedWasAdmin = wasAdmin
	m.lastReqID = reqID
	// Базовый снимок ПО — точка отсчёта для дельты за сессию (аудит JIT-доступа).
	if m.snapshot != nil {
		m.grantBaseline = m.snapshot()
	}
	m.log.Info("admin: права выданы", slog.String("user", user),
		slog.String("request_id", reqID), slog.Time("expires_at", expires))
	m.reportStatus(ctx, reqID, pb.AdminAccessStatus_ADMIN_ACCESS_STATUS_APPROVED, "applied on "+user, nil, nil)
}

func (m *Manager) revoke(ctx context.Context, reason string) {
	user := m.grantedUser
	reqID := m.lastReqID
	wasAdmin := m.grantedWasAdmin
	switch {
	case wasAdmin:
		// Пользователь был администратором ещё до выдачи гранта — его собственные права
		// не наши, снимать их нельзя. Грант считаем завершённым, из группы НЕ удаляем.
		m.log.Info("admin: пользователь был админом до гранта — из группы НЕ удаляю",
			slog.String("user", user), slog.String("reason", reason))
	default:
		if err := m.priv.Revoke(user); err != nil {
			m.log.Error("admin: снятие прав", slog.String("user", user), slog.Any("error", err))
			// Состояние всё равно очищаем: повторить снятие уже не выйдет, но не
			// зацикливаемся; ошибку залогировали.
		}
	}
	m.grantedUser = ""
	m.grantedExpires = time.Time{}
	m.grantedWasAdmin = false
	// Дельта ПО за сессию: что установлено/удалено, пока действовали админ-права.
	var added, removed []*pb.SoftwareItem
	if m.snapshot != nil {
		added, removed = diffSoftware(m.grantBaseline, m.snapshot())
	}
	m.grantBaseline = nil
	m.log.Info("admin: права сняты", slog.String("user", user), slog.String("reason", reason),
		slog.Int("software_added", len(added)), slog.Int("software_removed", len(removed)))
	m.reportStatus(ctx, reqID, pb.AdminAccessStatus_ADMIN_ACCESS_STATUS_REVOKED, reason, added, removed)
}

func (m *Manager) reportStatus(ctx context.Context, reqID string, status pb.AdminAccessStatus, details string, added, removed []*pb.SoftwareItem) {
	err := m.report(ctx, &pb.ReportAdminAccessRequest{
		RequestId:       reqID,
		Status:          status,
		OccurredAt:      time.Now().Unix(),
		Details:         details,
		SoftwareAdded:   added,
		SoftwareRemoved: removed,
	})
	if err != nil {
		m.log.Error("admin: ReportAdminAccess", slog.String("request_id", reqID), slog.Any("error", err))
	}
}

// defaultSoftwareSnapshot — прод-реализация snapshot: список установленного ПО
// (collector.InstalledSoftware, маппится в SoftwareItem).
func defaultSoftwareSnapshot() []*pb.SoftwareItem {
	sw := collector.InstalledSoftware()
	out := make([]*pb.SoftwareItem, 0, len(sw))
	for _, s := range sw {
		out = append(out, &pb.SoftwareItem{SoftwareName: s.Name, Version: s.Version})
	}
	return out
}

// diffSoftware сравнивает базовый и текущий списки ПО: added — есть в current, нет в
// baseline; removed — есть в baseline, нет в current. Ключ = имя+версия (смена версии
// = удаление старой + установка новой). Возвращает nil-срезы при отсутствии базового
// снимка (напр. рестарт агента посреди сессии).
func diffSoftware(baseline, current []*pb.SoftwareItem) (added, removed []*pb.SoftwareItem) {
	if baseline == nil {
		return nil, nil
	}
	key := func(s *pb.SoftwareItem) string { return s.GetSoftwareName() + "\x00" + s.GetVersion() }
	baseSet := make(map[string]struct{}, len(baseline))
	for _, s := range baseline {
		baseSet[key(s)] = struct{}{}
	}
	curSet := make(map[string]struct{}, len(current))
	for _, s := range current {
		curSet[key(s)] = struct{}{}
	}
	for _, s := range current {
		if _, ok := baseSet[key(s)]; !ok {
			added = append(added, s)
		}
	}
	for _, s := range baseline {
		if _, ok := curSet[key(s)]; !ok {
			removed = append(removed, s)
		}
	}
	return added, removed
}
