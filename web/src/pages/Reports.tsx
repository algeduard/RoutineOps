import { useState } from "react"
import { Download, Printer, FileSpreadsheet } from "lucide-react"
import api from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useCapabilities } from "@/lib/useCapabilities"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

const M = {
  title: { ru: "Отчёты", en: "Reports" },
  intro: {
    ru: "Экспортируемые отчёты по уже собранным данным. CSV — для выгрузки в таблицы (Excel/Sheets); «Печать (PDF)» открывает печатный HTML-отчёт в новой вкладке — сохраните его в PDF через печать браузера (Ctrl/Cmd+P → «Сохранить как PDF»). Новых данных от агента не требуется.",
    en: "Exportable reports over already-collected data. CSV is for spreadsheets (Excel/Sheets); \"Print (PDF)\" opens a printable HTML report in a new tab — save it as PDF via your browser's print dialog (Ctrl/Cmd+P → \"Save as PDF\"). No new agent data required.",
  },
  unavailableTitle: { ru: "Отчёты недоступны в этой редакции", en: "Reports are not available in this edition" },
  unavailableBody: {
    ru: "Экспортируемые отчёты — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Exportable reports are an Enterprise-edition feature. They require an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  periodTitle: { ru: "Период", en: "Period" },
  periodHint: {
    ru: "Применяется к отчёту «Журнал аудита». Пусто = без ограничения. Формат ГГГГ-ММ-ДД.",
    en: "Applies to the Audit log report. Empty = no bound. Format YYYY-MM-DD.",
  },
  from: { ru: "С", en: "From" },
  to: { ru: "По", en: "To" },
  csv: { ru: "Скачать CSV", en: "Download CSV" },
  print: { ru: "Печать (PDF)", en: "Print (PDF)" },
  exportErr: { ru: "Не удалось выгрузить отчёт", en: "Failed to export the report" },

  devicesName: { ru: "Устройства", en: "Devices" },
  devicesDesc: { ru: "Парк: статус, ОС, шифрование диска, владелец, последняя активность.", en: "Fleet: status, OS, disk encryption, owner, last activity." },
  inventoryName: { ru: "Инвентарь ПО", en: "Software inventory" },
  inventoryDesc: { ru: "Сводка по ПО в парке: имя, версия, число устройств.", en: "Software across the fleet: name, version, device count." },
  auditName: { ru: "Журнал аудита", en: "Audit log" },
  auditDesc: { ru: "Действия в консоли за выбранный период.", en: "Console actions over the selected period." },
  alertsName: { ru: "Алерты", en: "Alerts" },
  alertsDesc: { ru: "Журнал алертов: устройство, тип, критичность, детали.", en: "Alerts log: device, type, severity, details." },
}

type ReportType = "devices" | "inventory" | "audit" | "alerts"

const REPORTS: { type: ReportType; name: Msg; desc: Msg; usesPeriod: boolean }[] = [
  { type: "devices", name: M.devicesName, desc: M.devicesDesc, usesPeriod: false },
  { type: "inventory", name: M.inventoryName, desc: M.inventoryDesc, usesPeriod: false },
  { type: "audit", name: M.auditName, desc: M.auditDesc, usesPeriod: true },
  { type: "alerts", name: M.alertsName, desc: M.alertsDesc, usesPeriod: false },
]

export default function Reports() {
  const t = useT()
  const { caps, loading } = useCapabilities()
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")
  const [busy, setBusy] = useState<string | null>(null)

  // periodParams добавляет from/to только когда заданы (пустые границы = без ограничения).
  function periodParams(): Record<string, string> {
    const p: Record<string, string> = {}
    if (from.trim()) p.from = from.trim()
    if (to.trim()) p.to = to.trim()
    return p
  }

  async function downloadCsv(type: ReportType) {
    setBusy(type + ":csv")
    try {
      // responseType: blob — нужен файл, а не JSON. Скачивание через объектную ссылку.
      const r = await api.get(`/reports/${type}`, { params: { format: "csv", ...periodParams() }, responseType: "blob" })
      const url = URL.createObjectURL(r.data as Blob)
      const a = document.createElement("a")
      a.href = url
      a.download = `${type}-report.csv`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch {
      toast({ title: t(M.exportErr), variant: "destructive" })
    } finally {
      setBusy(null)
    }
  }

  // openPrint открывает печатный HTML-отчёт в новой вкладке ПРЯМОЙ ссылкой (авторизация —
  // httpOnly cookie, поэтому GET-навигация проходит без axios). Пользователь печатает в PDF.
  function openPrint(type: ReportType) {
    const qs = new URLSearchParams({ format: "pdf", ...periodParams() }).toString()
    window.open(`/api/v1/reports/${type}?${qs}`, "_blank", "noopener")
  }

  if (loading) return <p className="text-muted-foreground text-sm">{t(M.loading)}</p>

  if (!caps.reports) {
    return (
      <div className="flex flex-col gap-5 max-w-2xl">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <div className="glass px-5 py-[18px] space-y-2">
          <div className="flex items-center gap-2">
            <Badge variant="secondary">Free</Badge>
            <span className="text-[15px] font-semibold text-foreground">{t(M.unavailableTitle)}</span>
          </div>
          <p className="text-sm text-muted-foreground">{t(M.unavailableBody)}</p>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <p className="text-sm text-muted-foreground mt-1 max-w-3xl">{t(M.intro)}</p>
      </div>

      {/* Период (для audit) */}
      <div className="glass px-5 py-[18px] space-y-3">
        <div className="text-[15px] font-semibold text-foreground">{t(M.periodTitle)}</div>
        <div className="flex flex-wrap items-end gap-4">
          <div className="space-y-1">
            <Label htmlFor="rep-from">{t(M.from)}</Label>
            <Input id="rep-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="w-[170px]" />
          </div>
          <div className="space-y-1">
            <Label htmlFor="rep-to">{t(M.to)}</Label>
            <Input id="rep-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} className="w-[170px]" />
          </div>
        </div>
        <p className="text-xs text-muted-foreground">{t(M.periodHint)}</p>
      </div>

      {/* Список отчётов */}
      <div className="grid gap-3 sm:grid-cols-2">
        {REPORTS.map((rep) => (
          <div key={rep.type} className="glass px-5 py-[18px] flex flex-col gap-3">
            <div className="flex items-start gap-2.5">
              <FileSpreadsheet className="h-[18px] w-[18px] text-brand flex-shrink-0 mt-0.5" />
              <div className="min-w-0">
                <div className="text-[15px] font-semibold text-foreground">{t(rep.name)}</div>
                <p className="text-sm text-muted-foreground mt-0.5">{t(rep.desc)}</p>
              </div>
            </div>
            <div className="flex flex-wrap gap-2 mt-auto">
              <Button variant="outline" size="sm" onClick={() => downloadCsv(rep.type)} disabled={busy === rep.type + ":csv"}>
                <Download className="h-4 w-4 mr-1.5" />
                {t(M.csv)}
              </Button>
              <Button variant="outline" size="sm" onClick={() => openPrint(rep.type)}>
                <Printer className="h-4 w-4 mr-1.5" />
                {t(M.print)}
              </Button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
