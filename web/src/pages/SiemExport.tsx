import { useEffect, useState, FormEvent } from "react"
import api, { SIEMExportConfig, errStatus } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

const M = {
  title: { ru: "SIEM-экспорт аудита", en: "Audit SIEM export" },
  intro: {
    ru: "Форвардинг событий журнала аудита на внешний webhook (SIEM). Новые события отправляются батчами; тело подписывается HMAC-SHA256 в заголовке X-RoutineOps-Signature. Курсор durable — при обрыве события не теряются и не дублируются в пропусках.",
    en: "Forwards audit-log events to an external webhook (SIEM). New events are sent in batches; the body is signed with HMAC-SHA256 in the X-RoutineOps-Signature header. The cursor is durable — events are not lost on interruption.",
  },
  unavailableTitle: { ru: "SIEM-экспорт недоступен в этой редакции", en: "SIEM export is not available in this edition" },
  unavailableBody: {
    ru: "Форвардинг аудита в SIEM — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Audit forwarding to a SIEM is an Enterprise-edition feature. It requires an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить настройку", en: "Failed to load the configuration" },
  statusLabel: { ru: "Статус:", en: "Status:" },
  badgeOn: { ru: "Включён", en: "Enabled" },
  badgeOff: { ru: "Выключен", en: "Disabled" },
  currentUrl: { ru: "Webhook: ", en: "Webhook: " },
  secretSet: { ru: "Секрет подписи задан", en: "Signing secret set" },
  secretUnset: { ru: "Секрет подписи не задан (тело не подписывается)", en: "No signing secret (body is not signed)" },
  enabledLabel: { ru: "Включить форвардинг", en: "Enable forwarding" },
  urlLabel: { ru: "URL webhook (http/https)", en: "Webhook URL (http/https)" },
  urlPlaceholder: { ru: "https://siem.example.com/ingest", en: "https://siem.example.com/ingest" },
  secretLabel: { ru: "Секрет HMAC", en: "HMAC secret" },
  secretPlaceholder: { ru: "оставьте пустым, чтобы не менять", en: "leave blank to keep unchanged" },
  save: { ru: "Сохранить", en: "Save" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  saved: { ru: "Настройка сохранена", en: "Configuration saved" },
  hint: {
    ru: "При включении события отправляются с текущего момента (история не выгружается). Внутренние адреса разрешены — SIEM часто в закрытом контуре.",
    en: "When enabled, events are sent starting from now (history is not backfilled). Internal addresses are allowed — a SIEM is often on a private network.",
  },
}

export default function SiemExport() {
  const t = useT()
  const [cfg, setCfg] = useState<SIEMExportConfig | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)

  const [enabled, setEnabled] = useState(false)
  const [url, setUrl] = useState("")
  const [secret, setSecret] = useState("")
  const [saving, setSaving] = useState(false)

  async function load() {
    setLoadError(false)
    try {
      const r = await api.get<SIEMExportConfig>("/siem/config")
      setCfg(r.data)
      setEnabled(r.data.enabled)
      setUrl(r.data.webhook_url)
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
      const r = await api.post<SIEMExportConfig>("/siem/config", {
        enabled,
        webhook_url: url.trim(),
        hmac_secret: secret, // пусто = не менять
      })
      setCfg(r.data)
      setSecret("")
      toast({ title: t(M.saved), variant: "success" })
    } catch {
      // авто-тост интерцептора (400 «valid url» / 402 и т.п.)
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
    <div className="flex flex-col gap-5 max-w-2xl">
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
          <div className="flex items-center gap-2">
            <span className="text-soft">{t(M.statusLabel)}</span>
            {cfg?.enabled ? <Badge variant="success">{t(M.badgeOn)}</Badge> : <Badge variant="secondary">{t(M.badgeOff)}</Badge>}
          </div>
          {cfg?.webhook_url && (
            <div className="text-foreground break-all"><span className="text-soft">{t(M.currentUrl)}</span>{cfg.webhook_url}</div>
          )}
          <div className="text-muted-foreground">{cfg?.has_secret ? t(M.secretSet) : t(M.secretUnset)}</div>
        </div>
      )}

      <form onSubmit={save} className="glass px-5 py-[18px] space-y-4">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          <span className="text-foreground">{t(M.enabledLabel)}</span>
        </label>
        <div className="space-y-1.5">
          <Label htmlFor="siem-url" className="text-soft">{t(M.urlLabel)}</Label>
          <Input id="siem-url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder={t(M.urlPlaceholder)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="siem-secret" className="text-soft">{t(M.secretLabel)}</Label>
          <Input id="siem-secret" type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder={t(M.secretPlaceholder)} />
        </div>
        <p className="text-xs text-muted-foreground">{t(M.hint)}</p>
        <Button type="submit" disabled={saving || (enabled && !url.trim())}>
          {saving ? t(M.saving) : t(M.save)}
        </Button>
      </form>
    </div>
  )
}
