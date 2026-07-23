import { useEffect, useState } from "react"
import api, { ComplianceReport, ComplianceCheck, errStatus } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

const M = {
  title: { ru: "Соответствие", en: "Compliance" },
  intro: {
    ru: "Автоматический скоринг соответствия (CIS/SOC2) по уже собранным данным: шифрование дисков, MFA, актуальность устройств, политики ПО и скриптов, целостность аудита. Новых данных от агента не требуется.",
    en: "Automated compliance scoring (CIS/SOC2) over already-collected data: disk encryption, MFA, device check-in, software and script policies, audit integrity. No new agent data required.",
  },
  unavailableTitle: { ru: "Отчёты соответствия недоступны в этой редакции", en: "Compliance reporting is not available in this edition" },
  unavailableBody: {
    ru: "Compliance-дашборды и отчёты — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Compliance dashboards and reports are an Enterprise-edition feature. They require an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить отчёт", en: "Failed to load the report" },
  retry: { ru: "Повторить", en: "Retry" },
  scoreLabel: { ru: "Общий скор", en: "Overall score" },
  generatedAt: { ru: "Сформирован: {ts}", en: "Generated: {ts}" },
  export: { ru: "Экспорт CSV", en: "Export CSV" },
  exporting: { ru: "Экспорт...", en: "Exporting..." },
  exportErr: { ru: "Не удалось выгрузить CSV", en: "Failed to export CSV" },
  colCheck: { ru: "Проверка", en: "Check" },
  colCategory: { ru: "Категория", en: "Category" },
  colStatus: { ru: "Статус", en: "Status" },
  colCoverage: { ru: "Охват", en: "Coverage" },
  colDetail: { ru: "Детали", en: "Detail" },
  passed: { ru: "Пройдено", en: "Passed" },
  warned: { ru: "Внимание", en: "Warn" },
  failed: { ru: "Провал", en: "Fail" },
  summary: {
    ru: "Пройдено {pass} · внимание {warn} · провалов {fail} из {total} проверок.",
    en: "{pass} passing · {warn} warning · {fail} failing of {total} checks.",
  },
}

// Локализованные названия категорий (бэкенд отдаёт технический ключ).
const CATEGORY: Record<string, Msg> = {
  CIS: { ru: "CIS", en: "CIS" },
  SOC2: { ru: "SOC2", en: "SOC2" },
  access: { ru: "Доступ", en: "Access" },
  inventory: { ru: "Инвентарь", en: "Inventory" },
  audit: { ru: "Аудит", en: "Audit" },
  summary: { ru: "Итог", en: "Summary" },
}

// Локализованные заголовки проверок по id (бэкенд отдаёт англ. title как fallback).
const CHECK_TITLE: Record<string, Msg> = {
  admin_mfa: { ru: "MFA у аккаунтов администраторов", en: "Admin accounts with MFA enabled" },
  mfa_adoption: { ru: "MFA у пользователей консоли", en: "Console users with MFA enabled" },
  disk_encryption: { ru: "Шифрование дисков на активных устройствах", en: "Full-disk encryption on active devices" },
  device_checkin: { ru: "Активные устройства на связи за 14 дней", en: "Active devices seen in the last 14 days" },
  device_ownership: { ru: "У активных устройств назначен владелец", en: "Active devices with an assigned owner" },
  enrollment_backlog: { ru: "Заявки на энроллмент решены за 7 дней", en: "Enrollment requests resolved within 7 days" },
  stale_admin_access: { ru: "Заявки на admin-права в пределах таймаута", en: "Pending admin-access requests within timeout" },
  software_policy: { ru: "Нет запрещённого ПО в парке", en: "No forbidden software across the fleet" },
  script_policy: { ru: "Скрипт-политики проходят на устройствах", en: "Script policies passing on assigned devices" },
  audit_tamper_evident: { ru: "Tamper-evident журнал аудита", en: "Tamper-evident audit log" },
}

function statusBadge(status: ComplianceCheck["status"], t: ReturnType<typeof useT>) {
  if (status === "pass") return <Badge variant="success">{t(M.passed)}</Badge>
  if (status === "warn") return <Badge variant="secondary">{t(M.warned)}</Badge>
  return <Badge variant="destructive">{t(M.failed)}</Badge>
}

// scoreTone — цвет числа скора: зелёный ≥90, янтарный ≥75, иначе красный.
function scoreTone(score: number): string {
  if (score >= 90) return "text-emerald-600 dark:text-emerald-400"
  if (score >= 75) return "text-amber-600 dark:text-amber-400"
  return "text-red-600 dark:text-red-400"
}

export default function Compliance() {
  const t = useT()
  const [report, setReport] = useState<ComplianceReport | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [exporting, setExporting] = useState(false)

  async function load() {
    setLoading(true)
    setLoadError(false)
    try {
      const r = await api.get<ComplianceReport>("/compliance/report")
      setReport(r.data)
    } catch (e) {
      if (errStatus(e) === 404 || errStatus(e) === 402) setUnavailable(true)
      else {
        setLoadError(true)
        toast({ title: t(M.loadErr), variant: "destructive" })
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  async function exportCsv() {
    setExporting(true)
    try {
      const r = await api.get("/compliance/report", { params: { format: "csv" }, responseType: "blob" })
      const url = URL.createObjectURL(r.data as Blob)
      const a = document.createElement("a")
      a.href = url
      a.download = "compliance-report.csv"
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch {
      toast({ title: t(M.exportErr), variant: "destructive" })
    } finally {
      setExporting(false)
    }
  }

  if (loading) return <p className="text-muted-foreground text-sm">{t(M.loading)}</p>

  if (unavailable) {
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

  if (loadError || !report) {
    return (
      <div className="flex flex-col gap-5 max-w-2xl">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <div className="glass px-5 py-[18px] text-sm">
          <p className="text-destructive">{t(M.loadErr)}</p>
          <Button variant="outline" size="sm" className="mt-2" onClick={load}>{t(M.retry)}</Button>
        </div>
      </div>
    )
  }

  const passCount = report.checks.filter((c) => c.status === "pass").length
  const warnCount = report.checks.filter((c) => c.status === "warn").length
  const failCount = report.checks.filter((c) => c.status === "fail").length
  const generated = new Date(report.generated_at).toLocaleString()

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-3xl">{t(M.intro)}</p>
        </div>
        <Button variant="outline" size="sm" onClick={exportCsv} disabled={exporting}>
          {exporting ? t(M.exporting) : t(M.export)}
        </Button>
      </div>

      {/* Скор-карта */}
      <div className="glass px-5 py-[18px] flex flex-wrap items-center gap-6">
        <div className="flex flex-col">
          <span className="text-xs uppercase tracking-wider text-muted-foreground/70">{t(M.scoreLabel)}</span>
          <span className={`text-5xl font-bold leading-tight ${scoreTone(report.score)}`}>{report.score}<span className="text-2xl text-muted-foreground">%</span></span>
        </div>
        <div className="flex-1 min-w-[220px] space-y-2">
          <div className="h-2.5 w-full rounded-full bg-muted overflow-hidden">
            <div
              className={`h-full rounded-full ${report.score >= 90 ? "bg-emerald-500" : report.score >= 75 ? "bg-amber-500" : "bg-red-500"}`}
              style={{ width: `${report.score}%` }}
            />
          </div>
          <p className="text-sm text-muted-foreground">
            {t(M.summary, { pass: passCount, warn: warnCount, fail: failCount, total: report.checks.length })}
          </p>
          <p className="text-xs text-muted-foreground/70">{t(M.generatedAt, { ts: generated })}</p>
        </div>
      </div>

      {/* Таблица проверок */}
      <div className="glass overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t(M.colCheck)}</TableHead>
              <TableHead>{t(M.colCategory)}</TableHead>
              <TableHead>{t(M.colStatus)}</TableHead>
              <TableHead>{t(M.colCoverage)}</TableHead>
              <TableHead>{t(M.colDetail)}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {report.checks.map((c) => (
              <TableRow key={c.id}>
                <TableCell className="font-medium text-foreground">
                  {CHECK_TITLE[c.id] ? t(CHECK_TITLE[c.id]) : c.title}
                </TableCell>
                <TableCell>
                  <Badge variant="outline">{CATEGORY[c.category] ? t(CATEGORY[c.category]) : c.category}</Badge>
                </TableCell>
                <TableCell>{statusBadge(c.status, t)}</TableCell>
                <TableCell className="text-muted-foreground tabular-nums whitespace-nowrap">
                  {c.total > 0 ? `${c.passed} / ${c.total}` : "—"}
                </TableCell>
                <TableCell className="text-muted-foreground max-w-[420px]">{c.detail}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
