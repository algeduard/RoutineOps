import { useEffect, useState, FormEvent } from "react"
import api, { AutoRemediationConfig, RemediationLogEntry, errStatus } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

const M = {
  title: { ru: "Авто-устранение запрещённого ПО", en: "Auto-remediation of forbidden software" },
  intro: {
    ru: "Автоматически удаляет запрещённое ПО (software-политика «forbidden»), обнаруженное в инвентаре устройств. Переиспользует штатное удаление ПО: на нарушение ставится задача тихой деинсталляции. Проверка идёт фоново; повторные задачи по одному и тому же ПО не создаются, пока прежняя не завершится.",
    en: "Automatically removes forbidden software (a 'forbidden' software policy) found in device inventory. It reuses the standard software-removal path: a silent uninstall task is queued for each violation. The check runs in the background; duplicate tasks for the same software are not created while a previous one is still open.",
  },
  unavailableTitle: { ru: "Авто-устранение недоступно в этой редакции", en: "Auto-remediation is not available in this edition" },
  unavailableBody: {
    ru: "Автоматическое удаление запрещённого ПО — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Automatic removal of forbidden software is an Enterprise-edition feature. It requires an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить настройку", en: "Failed to load the configuration" },
  statusLabel: { ru: "Статус:", en: "Status:" },
  badgeOn: { ru: "Включено", en: "Enabled" },
  badgeOff: { ru: "Выключено", en: "Disabled" },
  badgeDryRun: { ru: "Режим обкатки (dry-run)", en: "Dry-run mode" },
  enabledLabel: { ru: "Включить авто-удаление запрещённого ПО", en: "Enable automatic removal of forbidden software" },
  dryRunLabel: { ru: "Режим обкатки (dry-run): только логировать, ничего не удалять", en: "Dry-run: only log, remove nothing" },
  warning: {
    ru: "Внимание: авто-удаление деструктивно. Запрещённое ПО будет тихо деинсталлировано с устройств без подтверждения оператора. Рекомендуется сначала включить режим обкатки (dry-run) и проверить лог ниже — там будет видно, что удалилось бы.",
    en: "Warning: automatic removal is destructive. Forbidden software will be silently uninstalled from devices without operator confirmation. It is recommended to enable dry-run first and review the log below — it shows what would be removed.",
  },
  save: { ru: "Сохранить", en: "Save" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  saved: { ru: "Настройка сохранена", en: "Configuration saved" },
  logTitle: { ru: "Лог ремедиаций", en: "Remediation log" },
  logEmpty: { ru: "Ремедиаций пока не было", en: "No remediations yet" },
  colDevice: { ru: "Устройство", en: "Device" },
  colSoftware: { ru: "ПО", en: "Software" },
  colAction: { ru: "Действие", en: "Action" },
  colWhen: { ru: "Когда", en: "When" },
  actRemoved: { ru: "Удалено (задача)", en: "Removed (task)" },
  actDryRun: { ru: "Удалил бы (dry-run)", en: "Would remove (dry-run)" },
}

export default function AutoRemediation() {
  const t = useT()
  const [cfg, setCfg] = useState<AutoRemediationConfig | null>(null)
  const [logEntries, setLogEntries] = useState<RemediationLogEntry[]>([])
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)

  const [enabled, setEnabled] = useState(false)
  const [dryRun, setDryRun] = useState(false)
  const [saving, setSaving] = useState(false)

  async function load() {
    setLoadError(false)
    try {
      const r = await api.get<AutoRemediationConfig>("/auto-remediation/config")
      setCfg(r.data)
      setEnabled(r.data.enabled)
      setDryRun(r.data.dry_run)
      // Лог грузим отдельно; его сбой не должен прятать конфиг.
      try {
        const l = await api.get<RemediationLogEntry[]>("/auto-remediation/log")
        setLogEntries(l.data ?? [])
      } catch {
        // лог опционален — молча оставляем пустым
      }
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

  async function save(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    try {
      const r = await api.put<AutoRemediationConfig>("/auto-remediation/config", {
        enabled,
        dry_run: dryRun,
      })
      setCfg(r.data)
      toast({ title: t(M.saved), variant: "success" })
      await load()
    } catch {
      // авто-тост интерцептора (402 и т.п.)
    } finally {
      setSaving(false)
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

  return (
    <div className="flex flex-col gap-5 max-w-3xl">
      <div>
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <p className="text-sm text-muted-foreground mt-1">{t(M.intro)}</p>
      </div>

      {loadError ? (
        <div className="glass px-5 py-[18px] text-sm">
          <p className="text-destructive">{t(M.loadErr)}</p>
          <Button variant="outline" size="sm" className="mt-2" onClick={load}>{t(M.loading)}</Button>
        </div>
      ) : (
        <div className="glass px-5 py-[18px] space-y-2 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-soft">{t(M.statusLabel)}</span>
            {cfg?.enabled ? <Badge variant="success">{t(M.badgeOn)}</Badge> : <Badge variant="secondary">{t(M.badgeOff)}</Badge>}
            {cfg?.enabled && cfg?.dry_run && <Badge variant="default">{t(M.badgeDryRun)}</Badge>}
          </div>
        </div>
      )}

      <form onSubmit={save} className="glass px-5 py-[18px] space-y-4">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          <span className="text-foreground">{t(M.enabledLabel)}</span>
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} />
          <span className="text-foreground">{t(M.dryRunLabel)}</span>
        </label>
        {/* Явное предупреждение про деструктивность (краснится, когда включён реальный режим). */}
        <p className={`rounded-md border px-3 py-2 text-xs ${enabled && !dryRun
          ? "border-red-500/25 bg-red-500/10 text-red-700 dark:text-red-300"
          : "border-border bg-muted/30 text-muted-foreground"}`}>
          {t(M.warning)}
        </p>
        <Button type="submit" disabled={saving}>
          {saving ? t(M.saving) : t(M.save)}
        </Button>
      </form>

      <div>
        <h2 className="text-[15px] font-semibold text-foreground mb-2">{t(M.logTitle)}</h2>
        {logEntries.length === 0 ? (
          <div className="glass py-10 text-center text-sm text-muted-foreground">{t(M.logEmpty)}</div>
        ) : (
          <div className="glass overflow-hidden">
            <div className="hidden sm:grid grid-cols-[1.4fr_1.6fr_1fr_1.2fr] gap-3 border-b border-border px-5 py-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground/70">
              <span>{t(M.colDevice)}</span>
              <span>{t(M.colSoftware)}</span>
              <span>{t(M.colAction)}</span>
              <span>{t(M.colWhen)}</span>
            </div>
            {logEntries.map((l) => (
              <div key={l.id} className="grid grid-cols-1 sm:grid-cols-[1.4fr_1.6fr_1fr_1.2fr] gap-1 sm:gap-3 border-b border-border px-5 py-3 last:border-b-0 text-sm">
                <span className="truncate text-foreground" title={l.hostname || l.device_id}>{l.hostname || l.device_id}</span>
                <span className="truncate text-foreground" title={l.software_name}>{l.software_name}</span>
                <span>
                  {l.action === "removed"
                    ? <Badge variant="destructive">{t(M.actRemoved)}</Badge>
                    : <Badge variant="secondary">{t(M.actDryRun)}</Badge>}
                </span>
                <span className="text-muted-foreground">{new Date(l.created_at).toLocaleString()}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
