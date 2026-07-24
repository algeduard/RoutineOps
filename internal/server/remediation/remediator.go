//go:build enterprise

// Package remediation авто-устраняет запрещённое ПО (enterprise-фича FeatureAutoRemediation).
// Фоновый цикл периодически ищет forbidden-нарушения по инвентарю (ListForbiddenViolations) и,
// если авто-устранение включено, ставит задачу удаления ПО, ПЕРЕИСПОЛЬЗУЯ существующий путь
// (CreateRemoveSoftwareTask → task_type='remove_software'). Новый механизм деинсталляции здесь
// НЕ пишется. Гейт по лицензии — снаружи (licensed()), чтобы пакет не зависел от internal/license.
//
// Доставку созданной задачи берёт на себя существующий реконсайлер pending-задач (cmd/server:
// он enqueue'ит pending-задачи подключённых устройств и переставляет их при реконнекте) —
// поэтому пакету не нужен asynq-клиент, как и siem/alertrouting. Отставание ≤ минуты для
// авто-удаления некритично (само оно деструктивно и намеренно не срочное).
//
// БЕЗОПАСНОСТЬ. Авто-устранение по умолчанию ВЫКЛючено (config.Enabled=false): включать
// деструктив должен осознанно администратор. Режим dry_run логирует, что удалил бы, не создавая
// задач. Дедуп не даёт спамить: пока по паре (устройство, ПО) висит незавершённая
// remove_software-задача, новую не создаём; повторная ремедиация происходит только если ПО
// снова появится в инвентаре (переустановлено) — тогда прежней незавершённой задачи уже нет.
package remediation

import (
	"context"
	"log/slog"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// tickInterval — как часто искать forbidden-нарушения. Реже, чем у siem/alertrouting (15–20с):
// действие деструктивно и не срочно, а больший интервал ещё и оставляет агенту время переслать
// инвентарь без удалённого ПО, прежде чем следующий проход мог бы что-то перепроверить.
const tickInterval = 5 * time.Minute

// Remediator — фоновый исполнитель авто-устранения. licensed() гейтит по лицензии (пустой тик,
// если FeatureAutoRemediation не активна), чтобы пакет не зависел от internal/license.
type Remediator struct {
	db       *storage.DB
	licensed func() bool
	logger   *slog.Logger
}

func NewRemediator(db *storage.DB, licensed func() bool, logger *slog.Logger) *Remediator {
	return &Remediator{db: db, licensed: licensed, logger: logger}
}

// Run крутит цикл устранения до завершения процесса (как прочие фоновые циклы cmd/server).
func (r *Remediator) Run() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for range ticker.C {
		r.tick()
	}
}

// tick — один проход устранения. Молча пустой, если лицензия не покрывает фичу или
// авто-устранение выключено конфигом.
func (r *Remediator) tick() {
	if !r.licensed() {
		return // лицензия не покрывает авто-устранение — ничего не делаем
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := r.db.GetAutoRemediationConfig(ctx)
	if err != nil {
		r.logger.Error("auto-remediation: чтение конфига", "err", err)
		return
	}
	if !cfg.Enabled {
		return // выключено (дефолт) — авто-удаление деструктивно, включается осознанно
	}

	violations, err := r.db.ListForbiddenViolations(ctx)
	if err != nil {
		r.logger.Error("auto-remediation: выборка нарушений", "err", err)
		return
	}
	for _, v := range violations {
		r.remediate(ctx, v, cfg.DryRun)
	}
}

// remediate обрабатывает одно нарушение: дедупит, создаёт задачу удаления (или логирует dry_run)
// и пишет аудит. Ошибка по одному нарушению логируется, но не прерывает обработку остальных.
func (r *Remediator) remediate(ctx context.Context, v storage.ForbiddenViolation, dryRun bool) {
	// Дедуп: по паре (устройство, ПО) уже висит незавершённая задача удаления — ничего не
	// делаем и в dry_run тоже (реальное удаление уже в процессе, нечего «предсказывать»).
	open, err := r.db.HasOpenRemoveSoftwareTask(ctx, v.DeviceID, v.SoftwareName)
	if err != nil {
		r.logger.Error("auto-remediation: проверка висящей задачи", "device_id", v.DeviceID, "software", v.SoftwareName, "err", err)
		return
	}
	if open {
		return
	}

	if dryRun {
		// Дедуп dry_run: эту пару уже логировали — не пишем ту же «удалил бы» строку на каждом
		// тике, пока нарушение висит.
		logged, err := r.db.HasDryRunRemediationLog(ctx, v.DeviceID, v.SoftwareName)
		if err != nil {
			r.logger.Error("auto-remediation: проверка dry-run лога", "device_id", v.DeviceID, "software", v.SoftwareName, "err", err)
			return
		}
		if logged {
			return
		}
		if _, err := r.db.AddRemediationLog(ctx, v.DeviceID, v.SoftwareName, "", "dry_run"); err != nil {
			r.logger.Error("auto-remediation: запись dry-run лога", "device_id", v.DeviceID, "software", v.SoftwareName, "err", err)
			return
		}
		r.audit(ctx, v, "", true)
		r.logger.Info("auto-remediation: dry-run, задача удаления НЕ создана", "device_id", v.DeviceID, "software", v.SoftwareName)
		return
	}

	// Реальный режим: переиспользуем существующий путь удаления ПО. Задача создаётся pending;
	// доставку берёт реконсайлер pending-задач (cmd/server) — enqueue тут не нужен.
	task, err := r.db.CreateRemoveSoftwareTask(ctx, v.DeviceID, v.SoftwareName, v.Version)
	if err != nil {
		r.logger.Error("auto-remediation: создание задачи удаления", "device_id", v.DeviceID, "software", v.SoftwareName, "err", err)
		return
	}
	if _, err := r.db.AddRemediationLog(ctx, v.DeviceID, v.SoftwareName, task.ID, "removed"); err != nil {
		// Задача уже создана; лог — вспомогательная история. Не откатываем, только логируем.
		r.logger.Error("auto-remediation: запись лога ремедиации", "device_id", v.DeviceID, "software", v.SoftwareName, "task_id", task.ID, "err", err)
	}
	r.audit(ctx, v, task.ID, false)
	r.logger.Info("auto-remediation: задача удаления запрещённого ПО поставлена", "device_id", v.DeviceID, "software", v.SoftwareName, "task_id", task.ID)
}

// audit пишет запись аудита о срабатывании авто-устранения. Актор — системный (авто-действие
// без пользователя): user_id пуст (NULL), user_email — метка 'auto-remediation'. Best-effort:
// ошибка логируется, но не влияет на уже сделанную ремедиацию.
func (r *Remediator) audit(ctx context.Context, v storage.ForbiddenViolation, taskID string, dryRun bool) {
	if err := r.db.WriteAuditLog(context.WithoutCancel(ctx), "", "auto-remediation",
		"auto_remediation_triggered", "device", v.DeviceID,
		map[string]any{"software": v.SoftwareName, "version": v.Version, "task_id": taskID, "dry_run": dryRun}); err != nil {
		r.logger.Error("auto-remediation: запись аудита", "device_id", v.DeviceID, "err", err)
	}
}
