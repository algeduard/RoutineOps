import { useEffect, useState } from "react"
import { AppWindow } from "lucide-react"
import api, { AppUsageResponse } from "@/lib/api"
import { HBar, RangeToggle } from "@/components/charts"
import { Button } from "@/components/ui/button"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"

type Range = "7d" | "30d"

// fmtDuration — секунды → «Xч Yм» / «Yм» / «Zс».
function fmtDuration(sec: number): string {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (h > 0) return `${h}ч ${m}м`
  if (m > 0) return `${m}м`
  return `${sec}с`
}

// dayLabel — «2026-07-21» → «21.07» (компактно для оси).
function dayLabel(day: string): string {
  const p = day.split("-")
  return p.length === 3 ? `${p[2]}.${p[1]}` : day
}

// DeviceActivity — секция «Активность»: топ приложений по времени и активное время
// за ПК по дням. ЧУВСТВИТЕЛЬНЫЕ данные: сбор выключен по умолчанию, включается
// тумблером (только it_admin, с аудитом на сервере). Poll каждые 15с.
export default function DeviceActivity({ deviceId, isAdmin }: { deviceId: string; isAdmin: boolean }) {
  const [data, setData] = useState<AppUsageResponse | null>(null)
  const [range, setRange] = useState<Range>("7d")
  const [loading, setLoading] = useState(true)
  const [toggling, setToggling] = useState(false)
  const [confirmEnable, setConfirmEnable] = useState(false)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const r = await api.get<AppUsageResponse>(`/devices/${deviceId}/app-usage?range=${range}`)
        if (!cancelled) setData(r.data)
      } catch {
        /* фоновый поллинг — молча */
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    const iv = setInterval(load, 15000)
    return () => {
      cancelled = true
      clearInterval(iv)
    }
  }, [deviceId, range])

  async function setEnabled(enabled: boolean) {
    setToggling(true)
    try {
      await api.put(`/devices/${deviceId}/telemetry-config`, { app_usage_enabled: enabled })
      setData((d) => (d ? { ...d, app_usage_enabled: enabled } : d))
      toast({
        title: enabled ? "Сбор аналитики приложений включён" : "Сбор аналитики приложений выключен",
        variant: "success",
      })
    } catch {
      /* тост об ошибке покажет перехватчик api */
    } finally {
      setToggling(false)
    }
  }

  const enabled = data?.app_usage_enabled ?? false
  const apps = (data?.apps ?? []).slice(0, 10)
  const days = data?.days ?? []
  const maxApp = apps.length > 0 ? apps[0].foreground_seconds : 0
  const maxActive = days.reduce((mx, d) => Math.max(mx, d.active_seconds), 0)
  const totalActive = days.reduce((sum, d) => sum + d.active_seconds, 0)
  const hasData = apps.length > 0 || days.length > 0

  return (
    <div className="glass px-5 py-[18px]">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-[15px] font-semibold text-foreground">
          <AppWindow className="h-[17px] w-[17px] text-muted-foreground" strokeWidth={2} />
          Активность
        </h2>
        <div className="flex items-center gap-2">
          <RangeToggle value={range} onChange={setRange} options={[["7d", "7 дней"], ["30d", "30 дней"]]} />
          {isAdmin && (
            enabled ? (
              <Button variant="outline" size="sm" disabled={toggling} onClick={() => setEnabled(false)}>
                Выключить сбор
              </Button>
            ) : (
              <Button variant="outline" size="sm" disabled={toggling} onClick={() => setConfirmEnable(true)}>
                Включить сбор
              </Button>
            )
          )}
        </div>
      </div>

      {!enabled && (
        <p className="mb-3 rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          Сбор аналитики приложений и времени за ПК выключен (по умолчанию). Это чувствительные данные о работе
          сотрудника — включение фиксируется в журнале аудита.
          {isAdmin ? " Включите сбор кнопкой выше." : ""}
        </p>
      )}

      {loading ? (
        <p className="text-sm text-muted-foreground">Загрузка...</p>
      ) : !hasData ? (
        <p className="text-sm text-muted-foreground">
          {enabled ? "Данные ещё не накоплены — появятся по мере работы устройства." : "Нет данных за выбранный период."}
        </p>
      ) : (
        <div className="flex flex-col gap-5">
          {apps.length > 0 && (
            <div>
              <p className="mb-2 text-xs font-medium text-muted-foreground">Топ приложений по времени на переднем плане</p>
              <div className="flex flex-col gap-2">
                {apps.map((a) => (
                  <HBar
                    key={`${a.day}-${a.app_name}`}
                    label={a.app_name}
                    value={a.foreground_seconds}
                    max={maxApp}
                    valueLabel={fmtDuration(a.foreground_seconds)}
                    color="#3b82f6"
                  />
                ))}
              </div>
            </div>
          )}

          {days.length > 0 && (
            <div>
              <p className="mb-2 text-xs font-medium text-muted-foreground">
                Активное время за ПК по дням {totalActive > 0 && <span className="text-foreground">· всего {fmtDuration(totalActive)}</span>}
              </p>
              <div className="flex flex-col gap-2">
                {days.map((d) => (
                  <HBar
                    key={d.day}
                    label={dayLabel(d.day)}
                    value={d.active_seconds}
                    max={maxActive}
                    valueLabel={fmtDuration(d.active_seconds)}
                    color="#22c55e"
                  />
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmEnable}
        onOpenChange={setConfirmEnable}
        title="Включить сбор аналитики приложений?"
        description="Агент начнёт собирать имена активных приложений и активное/простойное время за ПК. Это чувствительные данные о работе сотрудника; убедитесь, что сбор согласован с политикой конфиденциальности. Действие фиксируется в журнале аудита."
        confirmLabel="Включить сбор"
        onConfirm={() => setEnabled(true)}
      />
    </div>
  )
}
