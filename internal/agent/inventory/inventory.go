// Package inventory реализует Data Collector агента: периодически собирает
// полную инвентаризацию устройства и шлёт её unary-вызовом ReportInventory
// (ОТДЕЛЬНО от heartbeat-стрима — ADR-5).
package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Floodww/RoutineOps/internal/agent/collector"
	"github.com/Floodww/RoutineOps/internal/agent/transport"
	pb "github.com/Floodww/RoutineOps/proto"
)

// initialDelay — задержка перед первым отчётом, чтобы heartbeat успел
// зарегистрировать устройство (сервер делает UpsertInventory по уже
// существующей записи, созданной первым heartbeat).
// Var (а не const), чтобы тесты могли сократить ожидание.
var initialDelay = 3 * time.Second

// reportTimeout — потолок на один unary-вызов ReportInventory.
const reportTimeout = 30 * time.Second

// Reporter периодически отправляет инвентаризацию.
type Reporter struct {
	Interval time.Duration
	Dialer   *transport.Dialer
	Log      *slog.Logger

	// Version — версия агентского бинаря (ldflags main.version), едет в
	// DeviceInfo.agent_version для видимости раскатки в админке.
	Version string

	// lastHash — хэш последнего успешно отправленного снимка. Если снимок не
	// изменился, ReportInventory не шлём (last_seen всё равно держит heartbeat).
	lastHash string

	// sendReport отправляет снимок на сервер. Поле (а не прямой dial+RPC), чтобы
	// тесты могли подставить фейковую отправку. По умолчанию — dialAndSend.
	sendReport func(ctx context.Context, report *pb.InventoryReport) (received bool, err error)
}

// Run шлёт отчёт через initialDelay, затем каждые Interval, пока ctx жив.
func (r *Reporter) Run(ctx context.Context) {
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.reportOnce(ctx)
			timer.Reset(r.Interval)
		}
	}
}

func (r *Reporter) reportOnce(ctx context.Context) {
	report := build(r.Version)
	h := hashReport(report)
	if h == r.lastHash {
		r.Log.Debug("inventory без изменений — пропуск отправки")
		return
	}

	if r.sendReport == nil {
		r.sendReport = r.dialAndSend
	}
	received, err := r.sendReport(ctx, report)
	if err != nil {
		r.Log.Error("inventory: отправка", slog.Any("error", err))
		return
	}
	r.lastHash = h // запоминаем только после успешной отправки
	r.Log.Info("inventory отправлен",
		slog.Bool("received", received),
		slog.Int("software", len(report.GetSoftware())),
		slog.String("os_version", report.GetDeviceInfo().GetOsVersion()))
}

// dialAndSend — продакшн-реализация sendReport: dial + unary ReportInventory.
func (r *Reporter) dialAndSend(ctx context.Context, report *pb.InventoryReport) (bool, error) {
	conn, err := r.Dialer.Dial()
	if err != nil {
		return false, err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	ack, err := pb.NewAgentServiceClient(conn).ReportInventory(ctx, report)
	if err != nil {
		return false, err
	}
	return ack.GetReceived(), nil
}

// hashReport — стабильный хэш снимка (поля устройства + отсортированный список ПО),
// чтобы пропускать отправку неизменившейся инвентаризации.
func hashReport(r *pb.InventoryReport) string {
	d := r.GetDeviceInfo()
	var sb strings.Builder
	// agent_version в хэше: после self-update версия меняется даже если прочий
	// снимок тот же — иначе новая версия не доехала бы до сервера до след. смены.
	fmt.Fprintf(&sb, "%s|%s|%s|%s|%d|%s|%s|%s|%s|%s\n",
		d.GetHostname(), d.GetOs(), d.GetOsVersion(), d.GetCpu(),
		d.GetRam(), d.GetDisk(), d.GetIpAddress(), d.GetMacAddress(),
		d.GetSerialNumber(), d.GetAgentVersion())

	lines := make([]string, 0, len(r.GetSoftware()))
	for _, s := range r.GetSoftware() {
		lines = append(lines, s.GetSoftwareName()+"\t"+s.GetVersion())
	}
	sort.Strings(lines)
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// build собирает proto.InventoryReport. device_id не заполняется — сервер берёт
// его из mTLS-сертификата (ADR-1). agentVersion — версия бинаря (ldflags).
func build(agentVersion string) *pb.InventoryReport {
	d := collector.Collect()
	sw := collector.InstalledSoftware()

	items := make([]*pb.SoftwareItem, 0, len(sw))
	for _, s := range sw {
		items = append(items, &pb.SoftwareItem{SoftwareName: s.Name, Version: s.Version})
	}
	return &pb.InventoryReport{
		DeviceInfo: &pb.DeviceInfo{
			Hostname:     d.Hostname,
			Os:           d.OS,
			OsVersion:    d.OSVersion,
			Cpu:          d.CPU,
			Ram:          d.RAMMegabytes, // МБ (колонка devices.ram — INTEGER)
			Disk:         d.Disk,
			IpAddress:    d.IP,
			MacAddress:   d.MAC,
			SerialNumber: d.SerialNumber,
			AgentVersion: agentVersion,
		},
		Software: items,
	}
}
