package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Floodww/RoutineOps/internal/agent/transport"
	pb "github.com/Floodww/RoutineOps/proto"
)

// reportTimeout — потолок на один unary-вызов телеметрии.
const reportTimeout = 30 * time.Second

// initialDelay — задержка перед первой активностью, чтобы heartbeat успел
// зарегистрировать устройство (сервер скоупит телеметрию по уже существующей
// строке). Var, чтобы тесты могли сократить ожидание.
var initialDelay = 3 * time.Second

// Reporter собирает телеметрию устройства по таймерам и репортит её серверу:
//   - метрики ресурсов — сэмплирует каждые SampleInterval, шлёт батч раз в
//     ReportInterval (ReportResourceMetrics);
//   - app-usage — сэмплирует foreground/idle (только если сбор РАЗРЕШЁН локально И
//     ВКЛЮЧЁН сервером), шлёт суточные дельты раз в ReportInterval (ReportAppUsage);
//   - конфиг — поллит FetchTelemetryConfig каждые ConfigPollInterval, чтобы узнать
//     серверный флаг app_usage_enabled.
type Reporter struct {
	Dialer *transport.Dialer
	Log    *slog.Logger

	SampleInterval     time.Duration
	ReportInterval     time.Duration
	ConfigPollInterval time.Duration
	IdleThreshold      time.Duration
	// AppUsageAllowed — локальный разрешающий флаг (privacy kill-switch). Даже при
	// true сбор идёт ТОЛЬКО когда сервер включил app_usage_enabled для устройства
	// (AND-семантика). false — сбор аналитики приложений выключен жёстко.
	AppUsageAllowed bool
	// BatchMax — потолок буфера сэмплов ресурсов между отправками (защита памяти при
	// длительном оффлайне). При переполнении отбрасываются самые старые.
	BatchMax int

	// serverAppUsage — серверный флаг app_usage_enabled (обновляется configLoop).
	serverAppUsage atomic.Bool
	// serverCaptureTitles — серверный флаг capture_window_titles (configLoop): собирать
	// ли заголовки активных окон. Отдельный, более строгий privacy-гейт.
	serverCaptureTitles atomic.Bool

	// Тест-швы: продакшн-значения проставляются в Run.
	sendMetrics   func(ctx context.Context, r *pb.ResourceMetricsReport) error
	sendAppUsage  func(ctx context.Context, r *pb.AppUsageReport) error
	fetchConfig   func(ctx context.Context) (*pb.FetchTelemetryConfigResponse, error)
	foregroundApp func(withTitle bool) (app, title string, err error)
	idleDuration  func() (time.Duration, error)
	now           func() time.Time
}

// Run запускает петли сбора/отправки и блокирует до отмены ctx.
func (r *Reporter) Run(ctx context.Context) {
	if r.SampleInterval <= 0 {
		r.SampleInterval = 15 * time.Second
	}
	if r.ReportInterval <= 0 {
		r.ReportInterval = time.Minute
	}
	if r.ConfigPollInterval <= 0 {
		r.ConfigPollInterval = 5 * time.Minute
	}
	if r.IdleThreshold <= 0 {
		r.IdleThreshold = time.Minute
	}
	if r.BatchMax <= 0 {
		r.BatchMax = 240
	}
	if r.sendMetrics == nil {
		r.sendMetrics = r.reportMetrics
	}
	if r.sendAppUsage == nil {
		r.sendAppUsage = r.reportAppUsage
	}
	if r.fetchConfig == nil {
		r.fetchConfig = r.fetchTelemetryConfig
	}
	if r.foregroundApp == nil {
		r.foregroundApp = foregroundApp
	}
	if r.idleDuration == nil {
		r.idleDuration = idleDuration
	}
	if r.now == nil {
		r.now = time.Now
	}

	// initialDelay даёт heartbeat зарегистрировать устройство до первой телеметрии.
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	done := make(chan struct{}, 3)
	go func() { r.metricsLoop(ctx); done <- struct{}{} }()
	go func() { r.configLoop(ctx); done <- struct{}{} }()
	// Сэмплер активности крутим только там, где app-usage вообще реализован и локально
	// разрешён; иначе горутина бессмысленна (серверный флаг её всё равно не включит).
	loops := 2
	if r.AppUsageAllowed && appUsageSupported() {
		loops = 3
		go func() { r.activityLoop(ctx); done <- struct{}{} }()
	} else if r.AppUsageAllowed && !appUsageSupported() {
		r.Log.Info("telemetry: app-usage не поддерживается на этой платформе — сбор аналитики приложений отключён")
	}

	for i := 0; i < loops; i++ {
		<-done
	}
}

// metricsLoop сэмплирует ресурсы и шлёт батчи. Метрики допустимо терять: буфер
// ограничен BatchMax (старые отбрасываются), при сбое отправки батч остаётся и
// до-сылается на следующем тике.
func (r *Reporter) metricsLoop(ctx context.Context) {
	sampler := newResourceSampler()
	buf := make([]*pb.ResourceSample, 0, r.BatchMax)

	sampleT := time.NewTicker(r.SampleInterval)
	defer sampleT.Stop()
	reportT := time.NewTicker(r.ReportInterval)
	defer reportT.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sampleT.C:
			buf = append(buf, sampler.sample(ctx, r.now()))
			if len(buf) > r.BatchMax {
				buf = buf[len(buf)-r.BatchMax:]
			}
		case <-reportT.C:
			if len(buf) == 0 {
				continue
			}
			if err := r.sendMetrics(ctx, &pb.ResourceMetricsReport{Samples: buf}); err != nil {
				r.Log.Warn("telemetry: отправка метрик ресурсов", slog.Any("error", err))
				continue // батч остаётся, повтор на следующем тике
			}
			r.Log.Debug("telemetry: метрики ресурсов отправлены", slog.Int("samples", len(buf)))
			buf = buf[:0]
		}
	}
}

// activityLoop сэмплирует foreground/idle и шлёт суточные дельты. Накопление идёт
// ТОЛЬКО когда сервер включил app_usage_enabled; при выключенном сборе аккумулятор
// сбрасывается (данные до отключения не утекают — privacy).
func (r *Reporter) activityLoop(ctx context.Context) {
	agg := newActivityAggregator()
	lastAt := r.now()
	// Потолок на одно окно: пробуждение из сна даёт огромный elapsed — не приписываем
	// время сна активности/простою.
	maxElapsed := int64(2 * r.SampleInterval.Seconds())

	sampleT := time.NewTicker(r.SampleInterval)
	defer sampleT.Stop()
	reportT := time.NewTicker(r.ReportInterval)
	defer reportT.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sampleT.C:
			n := r.now()
			elapsed := int64(n.Sub(lastAt).Seconds())
			lastAt = n
			if !r.serverAppUsage.Load() {
				agg.reset() // сбор выключен — накопленное до отключения выбрасываем
				continue
			}
			if elapsed <= 0 {
				continue
			}
			if elapsed > maxElapsed {
				elapsed = int64(r.SampleInterval.Seconds())
			}
			app, title, err := r.foregroundApp(r.serverCaptureTitles.Load())
			if err != nil {
				r.Log.Debug("telemetry: foreground-приложение", slog.Any("error", err))
			}
			idle, err := r.idleDuration()
			if err != nil {
				r.Log.Debug("telemetry: idle-время", slog.Any("error", err))
			}
			active := idle < r.IdleThreshold
			agg.record(n.Format("2006-01-02"), app, title, active, elapsed)
		case <-reportT.C:
			if !r.serverAppUsage.Load() || agg.empty() {
				continue
			}
			apps, days := agg.drain()
			if err := r.sendAppUsage(ctx, &pb.AppUsageReport{Apps: apps, Days: days}); err != nil {
				r.Log.Warn("telemetry: отправка app-usage", slog.Any("error", err))
				agg.restore(apps, days) // вернуть дельты, повтор на следующем тике
				continue
			}
			r.Log.Debug("telemetry: app-usage отправлен", slog.Int("apps", len(apps)), slog.Int("days", len(days)))
		}
	}
}

// configLoop поллит серверный конфиг телеметрии (флаг app_usage_enabled). Первый
// опрос — сразу, затем по тикеру.
func (r *Reporter) configLoop(ctx context.Context) {
	poll := func() {
		cfg, err := r.fetchConfig(ctx)
		if err != nil {
			r.Log.Debug("telemetry: получение конфига", slog.Any("error", err))
			return
		}
		if r.serverAppUsage.Swap(cfg.GetAppUsageEnabled()) != cfg.GetAppUsageEnabled() {
			r.Log.Info("telemetry: серверный флаг app_usage_enabled изменён",
				slog.Bool("enabled", cfg.GetAppUsageEnabled()))
		}
		if r.serverCaptureTitles.Swap(cfg.GetCaptureWindowTitles()) != cfg.GetCaptureWindowTitles() {
			r.Log.Info("telemetry: серверный флаг capture_window_titles изменён",
				slog.Bool("enabled", cfg.GetCaptureWindowTitles()))
		}
	}
	poll()
	t := time.NewTicker(r.ConfigPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			poll()
		}
	}
}

func (r *Reporter) reportMetrics(ctx context.Context, report *pb.ResourceMetricsReport) error {
	conn, err := r.Dialer.Dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	ack, err := pb.NewAgentServiceClient(conn).ReportResourceMetrics(ctx, report)
	if err != nil {
		return err
	}
	if !ack.GetReceived() {
		return errors.New("server did not accept resource metrics")
	}
	return nil
}

func (r *Reporter) reportAppUsage(ctx context.Context, report *pb.AppUsageReport) error {
	conn, err := r.Dialer.Dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	ack, err := pb.NewAgentServiceClient(conn).ReportAppUsage(ctx, report)
	if err != nil {
		return err
	}
	if !ack.GetReceived() {
		return errors.New("server did not accept app usage")
	}
	return nil
}

func (r *Reporter) fetchTelemetryConfig(ctx context.Context) (*pb.FetchTelemetryConfigResponse, error) {
	conn, err := r.Dialer.Dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	return pb.NewAgentServiceClient(conn).FetchTelemetryConfig(ctx, &pb.FetchTelemetryConfigRequest{})
}
