package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
	pb "github.com/Floodww/RoutineOps/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Телеметрия устройств: агент собирает по таймеру и репортит (как inventory/
// security, ADR-5). device_id скоупится по mTLS-серту (ADR-1), не из тела.

// ReportResourceMetrics принимает батч сэмплов ресурсов и пишет их в time-series.
// Метрики допустимо терять: неизвестный fingerprint или удалённое устройство —
// accept-and-drop (Received:true), реальный сбой БД — Unavailable (агент повторит).
func (g *Gateway) ReportResourceMetrics(ctx context.Context, req *pb.ResourceMetricsReport) (*pb.ResourceMetricsAck, error) {
	_, fingerprint, err := extractCertInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "cert: %v", err)
	}
	deviceID, err := g.db.GetDeviceIDByFingerprint(ctx, fingerprint)
	if err != nil {
		g.logger.Error("resource metrics: lookup device", "err", err)
		return nil, status.Errorf(codes.Unavailable, "lookup device: %v", err)
	}
	if deviceID == "" {
		g.logger.Warn("resource metrics from unknown device, dropping", "fingerprint", fingerprint)
		return &pb.ResourceMetricsAck{Received: true}, nil
	}
	if len(req.Samples) == 0 {
		return &pb.ResourceMetricsAck{Received: true}, nil
	}

	now := time.Now()
	samples := make([]storage.ResourceSampleInput, 0, len(req.Samples))
	for _, s := range req.Samples {
		samples = append(samples, storage.ResourceSampleInput{
			Ts:            clampSampleTime(s.Ts, now),
			CPUPercent:    s.CpuPercent,
			MemUsedBytes:  s.MemUsedBytes,
			MemTotalBytes: s.MemTotalBytes,
			DiskPercent:   s.DiskPercent,
			NetRxBps:      s.NetRxBytesPerSec,
			NetTxBps:      s.NetTxBytesPerSec,
		})
	}
	if err := g.db.InsertResourceMetrics(ctx, deviceID, samples); err != nil {
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			// Устройство удалено между lookup и вставкой (гонка с удалением/ретеншеном) —
			// терминально, accept-and-drop.
			g.logger.Warn("resource metrics reference deleted device, dropping", "device_id", deviceID, "err", err)
			return &pb.ResourceMetricsAck{Received: true}, nil
		}
		g.logger.Error("insert resource metrics", "device_id", deviceID, "err", err)
		return nil, status.Errorf(codes.Unavailable, "insert metrics: %v", err)
	}
	g.logger.Debug("resource metrics saved", "device_id", deviceID, "samples", len(samples))
	return &pb.ResourceMetricsAck{Received: true}, nil
}

// ReportAppUsage принимает суточные дельты активности приложений. Двойной гейт
// privacy: даже если агент отстал от смены флага, сервер отбрасывает данные при
// выключенном сборе (accept-and-drop, без ретрая). Аккумулируется existing + delta.
func (g *Gateway) ReportAppUsage(ctx context.Context, req *pb.AppUsageReport) (*pb.AppUsageAck, error) {
	_, fingerprint, err := extractCertInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "cert: %v", err)
	}
	deviceID, err := g.db.GetDeviceIDByFingerprint(ctx, fingerprint)
	if err != nil {
		g.logger.Error("app usage: lookup device", "err", err)
		return nil, status.Errorf(codes.Unavailable, "lookup device: %v", err)
	}
	if deviceID == "" {
		g.logger.Warn("app usage from unknown device, dropping", "fingerprint", fingerprint)
		return &pb.AppUsageAck{Received: true}, nil
	}

	enabled, err := g.db.GetAppUsageEnabledByFingerprint(ctx, fingerprint)
	if err != nil {
		g.logger.Error("app usage: check collection flag", "device_id", deviceID, "err", err)
		return nil, status.Errorf(codes.Unavailable, "check flag: %v", err)
	}
	if !enabled {
		// Серверный privacy-гейт: сбор выключен для устройства — данные не пишем.
		g.logger.Info("app usage report while collection disabled, dropping", "device_id", deviceID)
		return &pb.AppUsageAck{Received: true}, nil
	}

	apps := make([]storage.AppUsageInput, 0, len(req.Apps))
	for _, a := range req.Apps {
		apps = append(apps, storage.AppUsageInput{Day: a.Day, AppName: a.AppName, ForegroundSeconds: a.ForegroundSeconds})
	}
	days := make([]storage.DailyActivityInput, 0, len(req.Days))
	for _, d := range req.Days {
		days = append(days, storage.DailyActivityInput{Day: d.Day, ActiveSeconds: d.ActiveSeconds, IdleSeconds: d.IdleSeconds})
	}

	if err := g.db.UpsertAppUsage(ctx, deviceID, apps); err != nil {
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			g.logger.Warn("app usage references deleted device, dropping", "device_id", deviceID, "err", err)
			return &pb.AppUsageAck{Received: true}, nil
		}
		g.logger.Error("upsert app usage", "device_id", deviceID, "err", err)
		return nil, status.Errorf(codes.Unavailable, "upsert app usage: %v", err)
	}
	if err := g.db.UpsertDailyActivity(ctx, deviceID, days); err != nil {
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			g.logger.Warn("daily activity references deleted device, dropping", "device_id", deviceID, "err", err)
			return &pb.AppUsageAck{Received: true}, nil
		}
		g.logger.Error("upsert daily activity", "device_id", deviceID, "err", err)
		return nil, status.Errorf(codes.Unavailable, "upsert activity: %v", err)
	}
	g.logger.Debug("app usage saved", "device_id", deviceID, "apps", len(apps), "days", len(days))
	return &pb.AppUsageAck{Received: true}, nil
}

// FetchTelemetryConfig отдаёт агенту privacy-флаг сбора аналитики приложений
// (pull, как FetchPolicy). Неизвестный серт → false (сбор выключен).
func (g *Gateway) FetchTelemetryConfig(ctx context.Context, _ *pb.FetchTelemetryConfigRequest) (*pb.FetchTelemetryConfigResponse, error) {
	_, fingerprint, err := extractCertInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "cert: %v", err)
	}
	enabled, err := g.db.GetAppUsageEnabledByFingerprint(ctx, fingerprint)
	if err != nil {
		g.logger.Error("fetch telemetry config", "err", err)
		return nil, status.Errorf(codes.Internal, "fetch telemetry config: %v", err)
	}
	// metrics_sample_seconds=0 → агент использует свой дефолт (серверный оверрайд не задаём).
	return &pb.FetchTelemetryConfigResponse{AppUsageEnabled: enabled}, nil
}

// clampSampleTime защищает ts метрик от нулевых/будущих/слишком старых значений
// агента (кривые часы). В отличие от clampAgentTime не логирует — батч из сотен
// сэмплов иначе засыпал бы лог. Окно назад — 7 суток (глубина ретенции метрик).
func clampSampleTime(unix int64, now time.Time) time.Time {
	if unix == 0 {
		return now
	}
	t := time.Unix(unix, 0)
	if t.After(now.Add(5*time.Minute)) || t.Before(now.Add(-7*24*time.Hour)) {
		return now
	}
	return t
}
