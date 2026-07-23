import { useEffect, useState, FormEvent } from "react"
import { Trash2 } from "lucide-react"
import api, { AlertRoutingRule, AlertSeverity, errStatus } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select } from "@/components/ui/select"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

const M = {
  title: { ru: "Маршрутизация алертов", en: "Alert routing" },
  intro: {
    ru: "Правила доставки алертов по уровню критичности. Алерт с severity не ниже порога правила отправляется в его канал (Telegram-чат или webhook). Эскалация повторно доставляет непринятые критичные алерты старше заданного времени.",
    en: "Rules that deliver alerts by severity. An alert whose severity is at or above the rule threshold is sent to its channel (a Telegram chat or a webhook). Escalation re-delivers unacknowledged critical alerts older than the set time.",
  },
  unavailableTitle: { ru: "Маршрутизация алертов недоступна в этой редакции", en: "Alert routing is not available in this edition" },
  unavailableBody: {
    ru: "Маршрутизация и эскалация алертов — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Alert routing and escalation is an Enterprise-edition feature. It requires an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить правила", en: "Failed to load rules" },
  addTitle: { ru: "Новое правило", en: "New rule" },
  minSeverity: { ru: "Мин. критичность", en: "Min. severity" },
  channel: { ru: "Канал", en: "Channel" },
  target: { ru: "Назначение", en: "Target" },
  targetChatId: { ru: "chat_id Telegram (напр. -1001234567)", en: "Telegram chat_id (e.g. -1001234567)" },
  targetUrl: { ru: "https://hooks.example.com/alerts", en: "https://hooks.example.com/alerts" },
  escalate: { ru: "Эскалация через (мин, 0 — выкл.)", en: "Escalate after (min, 0 = off)" },
  enabled: { ru: "Включено", en: "Enabled" },
  add: { ru: "Добавить правило", en: "Add rule" },
  adding: { ru: "Добавление...", en: "Adding..." },
  added: { ru: "Правило добавлено", en: "Rule added" },
  noRules: { ru: "Правил пока нет", en: "No rules yet" },
  sevInfo: { ru: "Инфо", en: "Info" },
  sevWarning: { ru: "Важный", en: "Warning" },
  sevCritical: { ru: "Критический", en: "Critical" },
  chTelegram: { ru: "Telegram", en: "Telegram" },
  chWebhook: { ru: "Webhook", en: "Webhook" },
  colChannel: { ru: "Канал", en: "Channel" },
  colTarget: { ru: "Назначение", en: "Target" },
  colEscalate: { ru: "Эскалация", en: "Escalation" },
  escMinutes: { ru: "{n} мин", en: "{n} min" },
  escOff: { ru: "—", en: "—" },
  disabled: { ru: "Выключено", en: "Disabled" },
  deleteTitle: { ru: "Удалить правило?", en: "Delete rule?" },
  deleteBody: { ru: "Доставка по этому правилу прекратится.", en: "Delivery by this rule will stop." },
  delete: { ru: "Удалить", en: "Delete" },
  deleted: { ru: "Правило удалено", en: "Rule deleted" },
}

const severityBadge: Record<AlertSeverity, { label: Msg; className: string }> = {
  critical: { label: M.sevCritical, className: "border-red-500/20 bg-red-500/15 text-red-700 dark:border-red-400/25 dark:bg-red-400/15 dark:text-red-300" },
  warning:  { label: M.sevWarning, className: "border-amber-500/20 bg-amber-500/15 text-amber-800 dark:border-amber-400/25 dark:bg-amber-400/15 dark:text-amber-300" },
  info:     { label: M.sevInfo, className: "border-blue-500/20 bg-blue-500/15 text-blue-700 dark:border-blue-400/25 dark:bg-blue-400/15 dark:text-blue-300" },
}

export default function AlertRouting() {
  const t = useT()
  const [rules, setRules] = useState<AlertRoutingRule[]>([])
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)

  const [minSeverity, setMinSeverity] = useState<AlertSeverity>("warning")
  const [channel, setChannel] = useState<"telegram" | "webhook">("telegram")
  const [target, setTarget] = useState("")
  const [escalate, setEscalate] = useState("0")
  const [enabled, setEnabled] = useState(true)
  const [saving, setSaving] = useState(false)
  const [toDelete, setToDelete] = useState<AlertRoutingRule | null>(null)

  async function load() {
    setLoadError(false)
    try {
      const r = await api.get<AlertRoutingRule[]>("/alert-routing-rules")
      setRules(r.data ?? [])
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

  async function add(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    try {
      await api.post("/alert-routing-rules", {
        min_severity: minSeverity,
        channel,
        target: target.trim(),
        enabled,
        escalate_after_minutes: Math.max(0, parseInt(escalate, 10) || 0),
      })
      setTarget("")
      setEscalate("0")
      toast({ title: t(M.added), variant: "success" })
      await load()
    } catch {
      // авто-тост интерцептора (400 валидация / 402 и т.п.)
    } finally {
      setSaving(false)
    }
  }

  async function remove(rule: AlertRoutingRule) {
    try {
      await api.delete(`/alert-routing-rules/${rule.id}`)
      toast({ title: t(M.deleted), variant: "success" })
      await load()
    } catch {
      // авто-тост интерцептора
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

      <form onSubmit={add} className="glass px-5 py-[18px] space-y-4">
        <span className="text-[15px] font-semibold text-foreground">{t(M.addTitle)}</span>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.minSeverity)}</Label>
            <Select
              value={minSeverity}
              onChange={(v) => setMinSeverity(v as AlertSeverity)}
              options={[
                { value: "info", label: t(M.sevInfo) },
                { value: "warning", label: t(M.sevWarning) },
                { value: "critical", label: t(M.sevCritical) },
              ]}
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.channel)}</Label>
            <Select
              value={channel}
              onChange={(v) => setChannel(v as "telegram" | "webhook")}
              options={[
                { value: "telegram", label: t(M.chTelegram) },
                { value: "webhook", label: t(M.chWebhook) },
              ]}
            />
          </div>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="ar-target" className="text-soft">{t(M.target)}</Label>
          <Input
            id="ar-target"
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder={channel === "telegram" ? t(M.targetChatId) : t(M.targetUrl)}
          />
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="ar-escalate" className="text-soft">{t(M.escalate)}</Label>
            <Input
              id="ar-escalate"
              type="number"
              min={0}
              value={escalate}
              onChange={(e) => setEscalate(e.target.value)}
            />
          </div>
          <label className="flex items-center gap-2 text-sm sm:mt-7">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            <span className="text-foreground">{t(M.enabled)}</span>
          </label>
        </div>
        <Button type="submit" disabled={saving || !target.trim()}>
          {saving ? t(M.adding) : t(M.add)}
        </Button>
      </form>

      {loadError ? (
        <div className="glass px-5 py-[18px] text-sm">
          <p className="text-destructive">{t(M.loadErr)}</p>
          <Button variant="outline" size="sm" className="mt-2" onClick={load}>{t(M.loading)}</Button>
        </div>
      ) : rules.length === 0 ? (
        <div className="glass py-10 text-center text-sm text-muted-foreground">{t(M.noRules)}</div>
      ) : (
        <div className="glass overflow-hidden">
          {rules.map((r) => (
            <div key={r.id} className="flex items-center gap-3 border-b border-border px-5 py-3 last:border-b-0">
              <span className={`inline-flex rounded-md border px-2 py-0.5 text-xs font-semibold ${severityBadge[r.min_severity]?.className ?? severityBadge.warning.className}`}>
                {t(severityBadge[r.min_severity]?.label ?? M.sevWarning)}
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-foreground">
                  {r.channel === "telegram" ? t(M.chTelegram) : t(M.chWebhook)}
                  {!r.enabled && <span className="ml-2 text-xs text-muted-foreground">({t(M.disabled)})</span>}
                </p>
                <p className="truncate text-xs text-soft font-mono">{r.target}</p>
              </div>
              <span className="hidden whitespace-nowrap text-xs text-muted-foreground sm:block">
                {r.escalate_after_minutes > 0 ? t(M.escMinutes, { n: r.escalate_after_minutes }) : t(M.escOff)}
              </span>
              <Button
                size="sm"
                variant="outline"
                onClick={() => setToDelete(r)}
                className="text-destructive hover:text-destructive"
                aria-label={t(M.delete)}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <ConfirmDialog
        open={!!toDelete}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={t(M.deleteTitle)}
        description={t(M.deleteBody)}
        confirmLabel={t(M.delete)}
        destructive
        onConfirm={() => { if (toDelete) remove(toDelete) }}
      />
    </div>
  )
}
