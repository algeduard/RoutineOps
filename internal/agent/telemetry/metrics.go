// Package telemetry реализует сбор телеметрии устройства агентом (Этап 6+):
//
//   - Метрики ресурсов (CPU/RAM/диск/сеть) — кросс-платформенно через gopsutil,
//     сэмплируются по таймеру и репортятся батчами (ReportResourceMetrics).
//   - Аналитика активности приложений и времени за ПК — суточные агрегаты,
//     ЧУВСТВИТЕЛЬНЫЕ данные, сбор гейтится флагом (ReportAppUsage; см. activity.go).
//
// Всё собирается АГЕНТОМ по таймеру и репортится unary-вызовами (как inventory/
// security, ADR-5); сервер не пушит. device_id не передаётся — сервер берёт его из
// mTLS-серта (ADR-1). Метрики допустимо терять при обрыве, поэтому идут прямым
// unary с ретраем на следующем тике, а не через durable-outbox.
package telemetry

import (
	"context"
	"math"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"

	pb "github.com/Floodww/RoutineOps/proto"
)

// resourceSampler снимает мгновенный срез ресурсов. Держит предыдущие сетевые
// счётчики, чтобы вычислять throughput как дельту между сэмплами.
type resourceSampler struct {
	diskPath string

	prevRx, prevTx uint64
	prevNetAt      time.Time
	haveNet        bool
}

func newResourceSampler() *resourceSampler {
	return &resourceSampler{diskPath: systemDiskPath()}
}

// systemDiskPath — корень системного тома для disk.Usage. На Windows это
// %SystemDrive% (обычно C:\), на unix — «/». Volatile-инвариант инвентаря сюда не
// распространяется: это отдельная time-series-подсистема.
func systemDiskPath() string {
	if runtime.GOOS == "windows" {
		return `C:\`
	}
	return "/"
}

// sample снимает один срез ресурсов на момент now. Ошибки отдельных проб не валят
// весь сэмпл — недоступная метрика остаётся нулевой (как «не знаю»). Первый вызов
// не даёт сетевого throughput (нет предыдущего счётчика для дельты) — вернёт 0.
func (s *resourceSampler) sample(ctx context.Context, now time.Time) *pb.ResourceSample {
	smp := &pb.ResourceSample{Ts: now.Unix()}

	// cpu.Percent(0, false) считает загрузку с ПРЕДЫДУЩЕГО вызова Percent, поэтому на
	// таймере каждый сэмпл — средняя загрузка за интервал. Первый сэмпл после старта
	// ~0 (базовой точки ещё нет) — приемлемо для периодического сэмплера.
	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		smp.CpuPercent = round2(pct[0])
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		smp.MemUsedBytes = int64(vm.Used)
		smp.MemTotalBytes = int64(vm.Total)
	}
	if du, err := disk.UsageWithContext(ctx, s.diskPath); err == nil {
		smp.DiskPercent = round2(du.UsedPercent)
	}
	// pernic=false → агрегированные счётчики по всем интерфейсам одной записью.
	if cs, err := gnet.IOCountersWithContext(ctx, false); err == nil && len(cs) > 0 {
		rx, tx := cs[0].BytesRecv, cs[0].BytesSent
		if s.haveNet {
			smp.NetRxBytesPerSec, smp.NetTxBytesPerSec = throughput(s.prevRx, s.prevTx, s.prevNetAt, rx, tx, now)
		}
		s.prevRx, s.prevTx, s.prevNetAt, s.haveNet = rx, tx, now, true
	}
	return smp
}

// throughput вычисляет байт/с по дельте кумулятивных счётчиков между двумя
// сэмплами. Сброс счётчика (ребут/reset интерфейса: cur < prev) даёт 0, а не
// огромный отрицательный/переполненный всплеск. Нулевой/отрицательный интервал — 0.
func throughput(prevRx, prevTx uint64, prevAt time.Time, curRx, curTx uint64, curAt time.Time) (rxbps, txbps int64) {
	elapsed := curAt.Sub(prevAt).Seconds()
	if elapsed <= 0 {
		return 0, 0
	}
	return perSecond(prevRx, curRx, elapsed), perSecond(prevTx, curTx, elapsed)
}

func perSecond(prev, cur uint64, elapsed float64) int64 {
	if cur < prev {
		return 0 // счётчик сброшен (ребут/reset интерфейса)
	}
	return int64(float64(cur-prev) / elapsed)
}

// round2 округляет до двух знаков — проценты не нуждаются в большей точности, а так
// они читаемее в JSON/логах.
func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}
