// Package command реализует Command Listener агента: приём Task из Connect-стрима,
// подтверждение доставки (AckTaskReceived), выполнение скрипта и отправка
// результата (ReportTaskResult).
//
// Идемпотентность: доставка Task — at-least-once (Asynq), агент обязан не
// выполнять одну задачу дважды. Здесь это in-memory дедуп по task_id; он НЕ
// переживает рестарт агента — персистентность задач (важна для выдачи прав,
// Этап 4) добавится позже.
package command

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Floodww/RoutineOps/internal/agent/transport"
	pb "github.com/Floodww/RoutineOps/proto"
)

// callTimeout — потолок на unary-вызовы ack/report.
const callTimeout = 30 * time.Second

// maxRuntime — жёсткий потолок на выполнение одного скрипта задачи (захардкожен).
const maxRuntime = 5 * time.Minute

// shutdownGrace — сколько ждать завершения текущих задач при остановке агента.
// Равен потолку выполнения: запущенная задача гарантированно успевает закончиться
// (она и так ограничена maxRuntime).
const shutdownGrace = maxRuntime

// Executor выполняет задачи, пришедшие от сервера.
type Executor struct {
	dialer *transport.Dialer
	log    *slog.Logger

	// execCtx — контекст выполнения задач. НЕ сигнальный: при SIGTERM задачи
	// продолжаются до завершения (грейс), а не убиваются мгновенно. Отменяется
	// в Shutdown по истечении грейса.
	execCtx context.Context
	cancel  context.CancelFunc

	wg sync.WaitGroup // учёт задач «в полёте» для graceful shutdown

	mu        sync.Mutex // защищает accepting
	accepting bool       // принимаем ли новые задачи
	seen      *seenSet   // идемпотентность по task_id (персистентная)

	// connect возвращает gRPC-клиента и функцию закрытия соединения. Поле (а не
	// прямой dial), чтобы тесты подставляли фейкового клиента. По умолчанию —
	// dialAndClient на основе dialer.
	connect func() (pb.AgentServiceClient, func(), error)

	locker  LockApplier      // применяет команды блокировки устройства (nil = выключено)
	revoker FileVaultRevoker // деструктивный FileVault revoke-chaining (nil = недоступен на этой ОС/сборке)
}

// LockApplier применяет команды блокировки/разблокировки устройства (реализуется
// lock.Manager). Вынесен интерфейсом, чтобы executor не зависел от пакета lock и
// тестировался с фейком.
type LockApplier interface {
	Lock(requestID, hash, reason string) error
	Unlock() error
}

// FileVaultRevoker выполняет ДЕСТРУКТИВНУЮ dynamic-revoke-цепочку (G1/G2/G3)
// вместо обычного оверлей-лока — только
// когда LockCommand.lock_mode == LOCK_MODE_FILEVAULT (fail-safe: 0/unknown
// всегда трактуется как OVERLAY, см. proto LockMode doc). Реализуется
// filevault.Chain.RevokeAndShutdown (revoke → durable-доставленный отчёт →
// H3 forced shutdown, строго в этом порядке); вынесен интерфейсом по тому же
// паттерну, что LockApplier.
type FileVaultRevoker interface {
	RevokeAndShutdown(ctx context.Context, requestID string) (pb.LockState, error)
}

// SetLocker подключает применятель блокировок. Вызывать до старта приёма задач.
func (e *Executor) SetLocker(l LockApplier) { e.locker = l }

// SetFileVaultRevoker подключает FileVault revoke-chaining. nil (по умолчанию)
// означает, что lock_mode=FILEVAULT будет отклонён с ошибкой, а не тихо
// выполнен как overlay — см. handleLock.
func (e *Executor) SetFileVaultRevoker(r FileVaultRevoker) { e.revoker = r }

// NewExecutor creates an executor. statePath — файл для персистентной идемпотентности (""=только память).
func NewExecutor(dialer *transport.Dialer, log *slog.Logger, statePath string) *Executor {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Executor{
		dialer:    dialer,
		log:       log,
		execCtx:   ctx,
		cancel:    cancel,
		accepting: true,
		seen:      loadSeenSet(statePath),
	}
	e.connect = e.dialAndClient
	return e
}

// dialAndClient — продакшн-реализация connect: dial + gRPC-клиент.
func (e *Executor) dialAndClient() (pb.AgentServiceClient, func(), error) {
	conn, err := e.dialer.Dial()
	if err != nil {
		return nil, nil, err
	}
	return pb.NewAgentServiceClient(conn), func() { conn.Close() }, nil
}

// Submit принимает Task из Connect-стрима и обрабатывает асинхронно, чтобы не
// блокировать heartbeat. Подходит как heartbeat.OnTask.
func (e *Executor) Submit(task *pb.Task) {
	if task.GetTaskId() == "" {
		e.log.Warn("получена задача без task_id — пропуск")
		return
	}
	e.mu.Lock()
	if !e.accepting {
		e.mu.Unlock()
		e.log.Warn("агент останавливается — задача отклонена", slog.String("task_id", task.GetTaskId()))
		return
	}
	e.wg.Add(1)
	e.mu.Unlock()

	go func() {
		defer e.wg.Done()
		e.handle(task)
	}()
}

// Shutdown прекращает приём новых задач и ждёт завершения уже запущенных до
// shutdownGrace; по истечении грейса — прерывает их (cancel execCtx).
func (e *Executor) Shutdown() {
	e.mu.Lock()
	e.accepting = false
	e.mu.Unlock()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		e.cancel()
	case <-time.After(shutdownGrace):
		e.log.Warn("graceful shutdown: задачи не успели за грейс — прерываю",
			slog.Duration("grace", shutdownGrace))
		e.cancel()
		e.wg.Wait()
	}
}

func (e *Executor) handle(task *pb.Task) {
	id := task.GetTaskId()

	client, closeConn, err := e.connect()
	if err != nil {
		// seen не помечаем — пусть сервер передоставит (at-least-once).
		e.log.Error("task: соединение", slog.String("task_id", id), slog.Any("error", err))
		return
	}
	defer closeConn()

	// Идемпотентность: повторную доставку подтверждаем (ack мог потеряться),
	// но скрипт второй раз не запускаем. Набор персистентный — переживает рестарт.
	newTask := e.seen.markIfNew(id)
	e.ack(client, id)
	if !newTask {
		e.log.Warn("повторная доставка задачи — выполнение пропущено", slog.String("task_id", id))
		return
	}

	// Команда блокировки устройства приезжает в Task.lock (а не как скрипт).
	if lc := task.GetLock(); lc != nil {
		e.handleLock(client, id, lc)
		return
	}

	e.log.Info("выполняю задачу", slog.String("task_id", id), slog.String("platform", task.GetPlatform()))
	runCtx, cancel := context.WithTimeout(e.execCtx, maxRuntime)
	defer cancel()
	stdout, stderr, runErr := runScript(runCtx, task.GetPlatform(), task.GetScriptContent())

	result := &pb.TaskResult{TaskId: id, Output: stdout}
	if runErr != nil {
		result.Status = pb.TaskStatus_TASK_STATUS_ERROR
		result.ErrorLog = combineErr(stderr, runErr)
		e.log.Warn("задача завершилась ошибкой", slog.String("task_id", id), slog.Any("error", runErr))
	} else {
		result.Status = pb.TaskStatus_TASK_STATUS_SUCCESS
		e.log.Info("задача выполнена успешно", slog.String("task_id", id))
	}
	e.report(client, result)
}

// handleLock применяет команду блокировки/разблокировки устройства и отчитывается
// серверу (ReportLockStatus) для аудита и UI. lock.Manager идемпотентен по
// request_id, поэтому повторная доставка безопасна.
//
// Корреляционный id: сервер шлёт его в LockCommand.request_id, но приравнивает к
// task_id. Если поле пустое (сервер положил только task_id) — падаем на taskID,
// чтобы идемпотентность и ReportLockStatus всё равно корректно сходились с задачей.
//
// lock_mode == LOCK_MODE_FILEVAULT (и НЕ unlock) — ветвится на e.revoker вместо
// обычного оверлея (proto LockMode doc: fail-safe, 0/unknown всегда OVERLAY, так
// что этот branch срабатывает ТОЛЬКО на явном значении 2). e.revoker == nil →
// ошибка вместо тихой деградации в overlay — сервер уже проверил escrow.Enabled
// перед постановкой такой задачи (handler.go lockDevice), поэтому nil здесь
// означает рассинхрон сборки агента, а не штатный случай.
func (e *Executor) handleLock(client pb.AgentServiceClient, taskID string, lc *pb.LockCommand) {
	reqID := lc.GetRequestId()
	if reqID == "" {
		reqID = taskID
	}
	var (
		err   error
		state pb.LockState
	)
	switch {
	case lc.GetUnlock():
		if e.locker == nil {
			e.log.Error("lock: команда разблокировки получена, но locker не сконфигурирован", slog.String("task_id", taskID))
			return
		}
		err = e.locker.Unlock()
		state = pb.LockState_LOCK_STATE_UNLOCKED
	case lc.GetLockMode() == pb.LockMode_LOCK_MODE_FILEVAULT:
		if e.revoker == nil {
			// Misbuild: revoker недоступен, RevokeAndShutdown не отработает — executor
			// сам репортит FAILED (сервер: actual-state + аудит + алерт IT), чтобы факт
			// не потерялся тихо. Падаем в общий report-блок ниже.
			err = fmt.Errorf("filevault revoker не сконфигурирован на этом агенте (сборка/ОС не поддерживает FileVault-лок)")
			state = pb.LockState_LOCK_STATE_FILEVAULT_REVOKE_FAILED
		} else {
			// RevokeAndShutdown durably репортит САМ и успех (FILEVAULT_REVOKED), и
			// провал (FILEVAULT_REVOKE_FAILED) — блокирующий retry, report.go. Executor
			// НЕ пере-репортит: иначе дубль аудита/алерта + гонка с shutdown -h now
			//. Ошибку только логируем — отчёт уже за revoker'ом.
			if _, rerr := e.revoker.RevokeAndShutdown(e.execCtx, reqID); rerr != nil {
				e.log.Error("lock: filevault RevokeAndShutdown", slog.String("request_id", reqID), slog.Any("error", rerr))
			}
			return
		}
	default:
		if e.locker == nil {
			e.log.Error("lock: команда блокировки получена, но locker не сконфигурирован", slog.String("task_id", taskID))
			return
		}
		err = e.locker.Lock(reqID, lc.GetPasswordHash(), lc.GetReason())
		state = pb.LockState_LOCK_STATE_LOCKED
	}

	details := "ok"
	if err != nil {
		details = err.Error()
		e.log.Error("lock: не удалось применить команду", slog.String("request_id", reqID), slog.Any("error", err))
	}
	ctx, cancel := context.WithTimeout(e.execCtx, callTimeout)
	defer cancel()
	if _, rerr := client.ReportLockStatus(ctx, &pb.ReportLockStatusRequest{
		RequestId:  reqID,
		State:      state,
		OccurredAt: time.Now().Unix(),
		Details:    details,
	}); rerr != nil {
		e.log.Error("lock: ReportLockStatus", slog.String("request_id", reqID), slog.Any("error", rerr))
	}
}

func (e *Executor) ack(client pb.AgentServiceClient, id string) {
	ctx, cancel := context.WithTimeout(e.execCtx, callTimeout)
	defer cancel()
	if _, err := client.AckTaskReceived(ctx, &pb.TaskReceivedAck{TaskId: id, ReceivedAt: time.Now().Unix()}); err != nil {
		e.log.Error("task: AckTaskReceived", slog.String("task_id", id), slog.Any("error", err))
	}
}

func (e *Executor) report(client pb.AgentServiceClient, result *pb.TaskResult) {
	ctx, cancel := context.WithTimeout(e.execCtx, callTimeout)
	defer cancel()
	if _, err := client.ReportTaskResult(ctx, result); err != nil {
		e.log.Error("task: ReportTaskResult", slog.String("task_id", result.GetTaskId()), slog.Any("error", err))
	}
}

func combineErr(stderr string, err error) string {
	if stderr == "" {
		return err.Error()
	}
	return stderr + "\n" + err.Error()
}
