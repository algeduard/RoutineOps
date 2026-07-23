import { useEffect, useState } from "react"
import api, { SCIMConfig, SCIMToken, errStatus } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

const M = {
  title: { ru: "SCIM-провижининг", en: "SCIM provisioning" },
  intro: {
    ru: "Автоматическая синхронизация пользователей из каталога IdP (Okta, Azure AD, OneLogin) по SCIM 2.0. IdP создаёт и деактивирует учётки RoutineOps через защищённый bearer-токеном канал. Новые пользователи получают роль «Наблюдатель».",
    en: "Automatic user synchronization from your IdP directory (Okta, Azure AD, OneLogin) via SCIM 2.0. The IdP creates and deactivates RoutineOps accounts over a bearer-token-protected channel. New users get the Viewer role.",
  },
  unavailableTitle: { ru: "SCIM недоступен в этой редакции", en: "SCIM is not available in this edition" },
  unavailableBody: {
    ru: "SCIM 2.0 provisioning — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "SCIM 2.0 provisioning is an Enterprise-edition feature. It requires an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить настройку", en: "Failed to load the configuration" },
  statusLabel: { ru: "Статус:", en: "Status:" },
  badgeOn: { ru: "Токен активен", en: "Token active" },
  badgeOff: { ru: "Выключен", en: "Disabled" },
  baseUrlLabel: { ru: "Base URL для IdP", en: "Base URL for the IdP" },
  hintOff: {
    ru: "SCIM выключен: токен не сгенерирован, эндпоинты отвергают запросы (401). Сгенерируйте токен, чтобы включить провижининг.",
    en: "SCIM is off: no token generated, endpoints reject requests (401). Generate a token to enable provisioning.",
  },
  hintOn: {
    ru: "Токен установлен. При ротации предыдущий токен немедленно перестаёт работать — обновите его в настройках IdP.",
    en: "A token is set. Rotating it immediately invalidates the previous one — update it in your IdP settings.",
  },
  generate: { ru: "Сгенерировать токен", en: "Generate token" },
  rotate: { ru: "Ротировать токен", en: "Rotate token" },
  generating: { ru: "Генерация...", en: "Generating..." },
  genErr: { ru: "Не удалось сгенерировать токен", en: "Failed to generate token" },
  tokenTitle: { ru: "Новый SCIM bearer-токен", en: "New SCIM bearer token" },
  tokenOnce: {
    ru: "Скопируйте токен сейчас — он показывается один раз и больше не восстановим. Вставьте его в поле «API Token» / «Secret Token» настроек SCIM вашего IdP.",
    en: "Copy the token now — it is shown once and cannot be recovered. Paste it into the “API Token” / “Secret Token” field of your IdP's SCIM settings.",
  },
}

export default function Scim() {
  const t = useT()
  const [cfg, setCfg] = useState<SCIMConfig | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [rotating, setRotating] = useState(false)
  const [newToken, setNewToken] = useState<string | null>(null)

  async function load() {
    setLoadError(false)
    try {
      const r = await api.get<SCIMConfig>("/scim/config")
      setCfg(r.data)
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

  async function rotate() {
    setRotating(true)
    try {
      const r = await api.post<SCIMToken>("/scim/token", {})
      setNewToken(r.data.token)
      setCfg({ enabled: true, base_url: r.data.base_url })
    } catch {
      toast({ title: t(M.genErr), variant: "destructive" })
    } finally {
      setRotating(false)
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
        <div className="glass px-5 py-[18px] space-y-3 text-sm">
          <div className="flex items-center gap-2">
            <span className="text-soft">{t(M.statusLabel)}</span>
            {cfg?.enabled ? <Badge variant="success">{t(M.badgeOn)}</Badge> : <Badge variant="secondary">{t(M.badgeOff)}</Badge>}
          </div>
          {cfg?.base_url && (
            <div className="space-y-1">
              <div className="text-soft">{t(M.baseUrlLabel)}</div>
              <code className="block rounded-md border border-border bg-muted px-3 py-2 text-[13px] select-all break-all font-mono text-foreground">
                {cfg.base_url}
              </code>
            </div>
          )}
          <p className="text-xs text-muted-foreground">{cfg?.enabled ? t(M.hintOn) : t(M.hintOff)}</p>
        </div>
      )}

      {newToken && (
        <div className="glass px-5 py-[18px] space-y-2">
          <span className="text-[15px] font-semibold text-foreground">{t(M.tokenTitle)}</span>
          <code className="block rounded-md border border-border bg-muted px-3 py-2.5 text-sm select-all break-all font-mono text-foreground">
            {newToken}
          </code>
          <p className="text-xs text-amber-600 dark:text-amber-400">{t(M.tokenOnce)}</p>
        </div>
      )}

      <div>
        <Button onClick={rotate} disabled={rotating}>
          {rotating ? t(M.generating) : cfg?.enabled ? t(M.rotate) : t(M.generate)}
        </Button>
      </div>
    </div>
  )
}
