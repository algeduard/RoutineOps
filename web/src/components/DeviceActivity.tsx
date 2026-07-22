import { useEffect, useState } from "react"
import { AppWindow, Globe } from "lucide-react"
import api, { AppUsageResponse } from "@/lib/api"
import { HBar, RangeToggle } from "@/components/charts"
import { Button } from "@/components/ui/button"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { useT, getLang } from "@/lib/i18n"

type Range = "7d" | "30d"

// fmtDuration — секунды → «Xч Yм» / «Yм» / «Zс».
function fmtDuration(sec: number): string {
  const en = getLang() === "en"
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (h > 0) return en ? `${h}h ${m}m` : `${h}ч ${m}м`
  if (m > 0) return en ? `${m}m` : `${m}м`
  return en ? `${sec}s` : `${sec}с`
}

// dayLabel — «2026-07-21» → «21.07» (компактно для оси).
function dayLabel(day: string): string {
  const p = day.split("-")
  return p.length === 3 ? `${p[2]}.${p[1]}` : day
}

// aggregate — суммирует секунды по ключу (имя приложения или заголовок окна),
// сортирует по убыванию и берёт top-N.
function aggregate<R extends { foreground_seconds: number }, T extends string>(
  rows: R[],
  keyOf: (r: R) => T,
  topN: number,
): { key: T; seconds: number }[] {
  const m = new Map<T, number>()
  for (const r of rows) {
    const k = keyOf(r)
    if (k === undefined || k === null || k === "") continue
    m.set(k, (m.get(k) ?? 0) + r.foreground_seconds)
  }
  return [...m.entries()]
    .map(([key, seconds]) => ({ key, seconds }))
    .sort((a, b) => b.seconds - a.seconds)
    .slice(0, topN)
}

const M = {
  activity: { ru: "Активность", en: "Activity" },
  days7: { ru: "7 дней", en: "7 days" },
  days30: { ru: "30 дней", en: "30 days" },
  captureTitlesHint: { ru: "Собирать заголовки активных окон (напр. вкладки браузера)", en: "Capture titles of active windows (e.g. browser tabs)" },
  windowTitles: { ru: "Заголовки окон", en: "Window titles" },
  on: { ru: "вкл", en: "on" },
  off: { ru: "выкл", en: "off" },
  disableCollection: { ru: "Выключить сбор", en: "Disable collection" },
  enableCollection: { ru: "Включить сбор", en: "Enable collection" },
  collectionOff: {
    ru: "Сбор аналитики приложений и времени за ПК выключен (по умолчанию). Это чувствительные данные о работе сотрудника — включение фиксируется в журнале аудита.",
    en: "Collection of app analytics and screen time is disabled (by default). This is sensitive data about an employee's work — enabling it is recorded in the audit log.",
  },
  enableHint: { ru: "Включите сбор кнопкой выше.", en: "Enable collection with the button above." },
  titlesWarning: {
    ru: "Собираются заголовки активных окон (включая вкладки браузера — грубо видно, какие сайты открыты). Заголовки могут содержать личные данные сотрудника — убедитесь, что это согласовано и сотрудник уведомлён.",
    en: "Titles of active windows are being collected (including browser tabs — roughly revealing which sites are open). Titles may contain an employee's personal data — make sure this is agreed and the employee is notified.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  noDataYet: { ru: "Данные ещё не накоплены — появятся по мере работы устройства.", en: "No data collected yet — it will appear as the device is used." },
  noDataPeriod: { ru: "Нет данных за выбранный период.", en: "No data for the selected period." },
  topApps: { ru: "Топ приложений по времени на переднем плане", en: "Top apps by foreground time" },
  topWindows: { ru: "Топ окон и сайтов по времени", en: "Top windows and sites by time" },
  activeTimeByDay: { ru: "Активное время за ПК по дням", en: "Active screen time by day" },
  total: { ru: "всего", en: "total" },
  toastAppOn: { ru: "Сбор аналитики приложений включён", en: "App analytics collection enabled" },
  toastAppOff: { ru: "Сбор аналитики приложений выключен", en: "App analytics collection disabled" },
  toastTitlesOn: { ru: "Сбор заголовков окон включён", en: "Window title collection enabled" },
  toastTitlesOff: { ru: "Сбор заголовков окон выключен", en: "Window title collection disabled" },
  confirmEnableTitle: { ru: "Включить сбор аналитики приложений?", en: "Enable app analytics collection?" },
  confirmEnableDesc: {
    ru: "Агент начнёт собирать имена активных приложений и активное/простойное время за ПК. Это чувствительные данные о работе сотрудника; убедитесь, что сбор согласован с политикой конфиденциальности. Действие фиксируется в журнале аудита.",
    en: "The agent will start collecting names of active apps and active/idle screen time. This is sensitive data about an employee's work; make sure the collection complies with your privacy policy. The action is recorded in the audit log.",
  },
  confirmTitlesTitle: { ru: "Собирать заголовки окон?", en: "Collect window titles?" },
  confirmTitlesDesc: {
    ru: "Агент начнёт собирать заголовки активных окон, включая вкладки браузера — по ним грубо видно, на каких сайтах и в каких документах работает сотрудник. Это ОСОБО чувствительные данные: заголовок может содержать личную информацию. Во многих юрисдикциях такой сбор требует предварительного уведомления работника. Действие фиксируется в журнале аудита.",
    en: "The agent will start collecting titles of active windows, including browser tabs — roughly revealing which sites and documents the employee is working in. This is ESPECIALLY sensitive data: a title may contain personal information. In many jurisdictions such collection requires prior notice to the employee. The action is recorded in the audit log.",
  },
  confirmTitlesLabel: { ru: "Включить сбор заголовков", en: "Enable title collection" },
}

// DeviceActivity — секция «Активность»: топ приложений по времени, топ окон/сайтов
// (если включён сбор заголовков) и активное время за ПК по дням. ЧУВСТВИТЕЛЬНЫЕ
// данные: оба сбора выключены по умолчанию, включаются тумблерами (только it_admin,
// с аудитом на сервере). Poll каждые 15с.
export default function DeviceActivity({ deviceId, isAdmin }: { deviceId: string; isAdmin: boolean }) {
  const t = useT()
  const [data, setData] = useState<AppUsageResponse | null>(null)
  const [range, setRange] = useState<Range>("7d")
  const [loading, setLoading] = useState(true)
  const [toggling, setToggling] = useState(false)
  const [confirmEnable, setConfirmEnable] = useState(false)
  const [confirmTitles, setConfirmTitles] = useState(false)

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

  async function setEnabled(v: boolean) {
    setToggling(true)
    try {
      // Выключая сбор аналитики, заодно выключаем и заголовки (они его подмножество).
      const body = v ? { app_usage_enabled: true } : { app_usage_enabled: false, capture_window_titles: false }
      await api.put(`/devices/${deviceId}/telemetry-config`, body)
      setData((d) => (d ? { ...d, app_usage_enabled: v, capture_window_titles: v ? d.capture_window_titles : false } : d))
      toast({ title: v ? t(M.toastAppOn) : t(M.toastAppOff), variant: "success" })
    } catch {
      /* тост об ошибке покажет перехватчик api */
    } finally {
      setToggling(false)
    }
  }

  async function setTitles(v: boolean) {
    setToggling(true)
    try {
      await api.put(`/devices/${deviceId}/telemetry-config`, { capture_window_titles: v })
      setData((d) => (d ? { ...d, capture_window_titles: v } : d))
      toast({ title: v ? t(M.toastTitlesOn) : t(M.toastTitlesOff), variant: "success" })
    } catch {
      /* тост об ошибке покажет перехватчик api */
    } finally {
      setToggling(false)
    }
  }

  const enabled = data?.app_usage_enabled ?? false
  const titlesEnabled = data?.capture_window_titles ?? false
  const rows = data?.apps ?? []
  const apps = aggregate(rows, (r) => r.app_name as string, 10)
  const titles = aggregate(rows, (r) => r.window_title as string, 12)
  const days = data?.days ?? []
  const maxApp = apps.length > 0 ? apps[0].seconds : 0
  const maxTitle = titles.length > 0 ? titles[0].seconds : 0
  const maxActive = days.reduce((mx, d) => Math.max(mx, d.active_seconds), 0)
  const totalActive = days.reduce((sum, d) => sum + d.active_seconds, 0)
  const hasData = apps.length > 0 || days.length > 0

  return (
    <div className="glass px-5 py-[18px]">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-[15px] font-semibold text-foreground">
          <AppWindow className="h-[17px] w-[17px] text-muted-foreground" strokeWidth={2} />
          {t(M.activity)}
        </h2>
        <div className="flex flex-wrap items-center gap-2">
          <RangeToggle value={range} onChange={setRange} options={[["7d", t(M.days7)], ["30d", t(M.days30)]]} />
          {isAdmin && (
            enabled ? (
              <>
                <Button
                  variant={titlesEnabled ? "default" : "outline"}
                  size="sm"
                  disabled={toggling}
                  onClick={() => (titlesEnabled ? setTitles(false) : setConfirmTitles(true))}
                  title={t(M.captureTitlesHint)}
                >
                  <Globe className="mr-1 h-3.5 w-3.5" />
                  {t(M.windowTitles)}: {titlesEnabled ? t(M.on) : t(M.off)}
                </Button>
                <Button variant="outline" size="sm" disabled={toggling} onClick={() => setEnabled(false)}>
                  {t(M.disableCollection)}
                </Button>
              </>
            ) : (
              <Button variant="outline" size="sm" disabled={toggling} onClick={() => setConfirmEnable(true)}>
                {t(M.enableCollection)}
              </Button>
            )
          )}
        </div>
      </div>

      {!enabled && (
        <p className="mb-3 rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          {t(M.collectionOff)}
          {isAdmin ? ` ${t(M.enableHint)}` : ""}
        </p>
      )}
      {enabled && titlesEnabled && (
        <p className="mb-3 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
          {t(M.titlesWarning)}
        </p>
      )}

      {loading ? (
        <p className="text-sm text-muted-foreground">{t(M.loading)}</p>
      ) : !hasData ? (
        <p className="text-sm text-muted-foreground">
          {enabled ? t(M.noDataYet) : t(M.noDataPeriod)}
        </p>
      ) : (
        <div className="flex flex-col gap-5">
          {apps.length > 0 && (
            <div>
              <p className="mb-2 text-xs font-medium text-muted-foreground">{t(M.topApps)}</p>
              <div className="flex flex-col gap-2">
                {apps.map((a) => (
                  <HBar key={a.key} label={a.key} value={a.seconds} max={maxApp} valueLabel={fmtDuration(a.seconds)} color="#3b82f6" />
                ))}
              </div>
            </div>
          )}

          {titles.length > 0 && (
            <div>
              <p className="mb-2 text-xs font-medium text-muted-foreground">{t(M.topWindows)}</p>
              <div className="flex flex-col gap-2">
                {titles.map((t) => (
                  <HBar key={t.key} label={t.key} value={t.seconds} max={maxTitle} valueLabel={fmtDuration(t.seconds)} color="#8b5cf6" />
                ))}
              </div>
            </div>
          )}

          {days.length > 0 && (
            <div>
              <p className="mb-2 text-xs font-medium text-muted-foreground">
                {t(M.activeTimeByDay)} {totalActive > 0 && <span className="text-foreground">· {t(M.total)} {fmtDuration(totalActive)}</span>}
              </p>
              <div className="flex flex-col gap-2">
                {days.map((d) => (
                  <HBar key={d.day} label={dayLabel(d.day)} value={d.active_seconds} max={maxActive} valueLabel={fmtDuration(d.active_seconds)} color="#22c55e" />
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmEnable}
        onOpenChange={setConfirmEnable}
        title={t(M.confirmEnableTitle)}
        description={t(M.confirmEnableDesc)}
        confirmLabel={t(M.enableCollection)}
        onConfirm={() => setEnabled(true)}
      />
      <ConfirmDialog
        open={confirmTitles}
        onOpenChange={setConfirmTitles}
        title={t(M.confirmTitlesTitle)}
        description={t(M.confirmTitlesDesc)}
        confirmLabel={t(M.confirmTitlesLabel)}
        onConfirm={() => setTitles(true)}
      />
    </div>
  )
}
