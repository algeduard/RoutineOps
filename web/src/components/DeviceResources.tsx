import { useEffect, useState } from "react"
import { Activity, Cpu, HardDrive, MemoryStick, Network, type LucideIcon } from "lucide-react"
import api, { ResourceMetric } from "@/lib/api"
import { MiniAreaChart, RangeToggle } from "@/components/charts"

type Range = "1h" | "24h"

// fmtBytes — человекочитаемый размер (Б/КБ/МБ/ГБ/ТБ, десятичный на 1024).
function fmtBytes(n: number): string {
  if (n < 1024) return `${Math.round(n)} Б`
  const units = ["КБ", "МБ", "ГБ", "ТБ"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 10 ? 0 : 1)} ${units[i]}`
}

const fmtBps = (n: number) => `${fmtBytes(n)}/с`
const fmtPct = (n: number) => `${Math.round(n)}%`

function MetricCard({ icon: Icon, label, value, sub, series, color, max }: {
  icon: LucideIcon
  label: string
  value: string
  sub?: string
  series: number[]
  color: string
  max?: number
}) {
  return (
    <div className="rounded-xl border border-border bg-background/40 px-4 py-3">
      <div className="flex items-center gap-2">
        <Icon className="h-3.5 w-3.5 text-muted-foreground" strokeWidth={2} />
        <span className="text-xs text-muted-foreground">{label}</span>
      </div>
      <p className="mt-1 text-lg font-semibold tabular-nums text-foreground">{value}</p>
      <p className="h-4 text-[11px] text-muted-foreground truncate">{sub ?? ""}</p>
      <div className="mt-2">
        <MiniAreaChart values={series} color={color} max={max} />
      </div>
    </div>
  )
}

// DeviceResources — секция «Ресурсы»: живые значения CPU/RAM/диск/сеть + SVG-графики
// истории за 1ч/24ч. Poll каждые 10с (как остальная карточка устройства).
export default function DeviceResources({ deviceId }: { deviceId: string }) {
  const [latest, setLatest] = useState<ResourceMetric | null>(null)
  const [history, setHistory] = useState<ResourceMetric[]>([])
  const [range, setRange] = useState<Range>("1h")
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const [l, h] = await Promise.all([
          api.get<ResourceMetric | null>(`/devices/${deviceId}/metrics/latest`),
          api.get<ResourceMetric[]>(`/devices/${deviceId}/metrics?range=${range}`),
        ])
        if (cancelled) return
        setLatest(l.data)
        setHistory(h.data ?? [])
      } catch {
        /* фоновый поллинг — молча */
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    const iv = setInterval(load, 10000)
    return () => {
      cancelled = true
      clearInterval(iv)
    }
  }, [deviceId, range])

  const memPct = latest && latest.mem_total_bytes > 0 ? (latest.mem_used_bytes / latest.mem_total_bytes) * 100 : 0
  const hasData = history.length > 0 || latest !== null

  const cpuSeries = history.map((m) => m.cpu_percent)
  const memSeries = history.map((m) => (m.mem_total_bytes > 0 ? (m.mem_used_bytes / m.mem_total_bytes) * 100 : 0))
  const diskSeries = history.map((m) => m.disk_percent)
  const netSeries = history.map((m) => m.net_rx_bps + m.net_tx_bps)

  return (
    <div className="glass px-5 py-[18px]">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="flex items-center gap-2 text-[15px] font-semibold text-foreground">
          <Activity className="h-[17px] w-[17px] text-muted-foreground" strokeWidth={2} />
          Ресурсы
        </h2>
        <RangeToggle value={range} onChange={setRange} options={[["1h", "1ч"], ["24h", "24ч"]]} />
      </div>

      {loading && !latest ? (
        <p className="text-sm text-muted-foreground">Загрузка...</p>
      ) : !hasData ? (
        <p className="text-sm text-muted-foreground">
          Метрики ещё не поступали. Агент собирает их по таймеру — данные появятся через минуту после подключения.
        </p>
      ) : (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <MetricCard
            icon={Cpu}
            label="CPU"
            value={latest ? fmtPct(latest.cpu_percent) : "—"}
            series={cpuSeries}
            color="#3b82f6"
            max={100}
          />
          <MetricCard
            icon={MemoryStick}
            label="RAM"
            value={latest ? fmtPct(memPct) : "—"}
            sub={latest ? `${fmtBytes(latest.mem_used_bytes)} / ${fmtBytes(latest.mem_total_bytes)}` : ""}
            series={memSeries}
            color="#8b5cf6"
            max={100}
          />
          <MetricCard
            icon={HardDrive}
            label="Диск (система)"
            value={latest ? fmtPct(latest.disk_percent) : "—"}
            series={diskSeries}
            color="#eab308"
            max={100}
          />
          <MetricCard
            icon={Network}
            label="Сеть"
            value={latest ? fmtBps(latest.net_rx_bps + latest.net_tx_bps) : "—"}
            sub={latest ? `↓ ${fmtBps(latest.net_rx_bps)}  ↑ ${fmtBps(latest.net_tx_bps)}` : ""}
            series={netSeries}
            color="#22c55e"
          />
        </div>
      )}
    </div>
  )
}
