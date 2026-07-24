import { useEffect, useState } from "react"
import api, { SCIMConfig, SCIMToken, SCIMRoleMapping, errStatus } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

const M = {
  title: { ru: "SCIM-провижининг", en: "SCIM provisioning" },
  intro: {
    ru: "Автоматическая синхронизация пользователей из каталога IdP (Okta, Azure AD, OneLogin) по SCIM 2.0. IdP создаёт и деактивирует учётки RoutineOps через защищённый bearer-токеном канал. Роль присваивается по маппингу групп ниже (по умолчанию «Наблюдатель»).",
    en: "Automatic user synchronization from your IdP directory (Okta, Azure AD, OneLogin) via SCIM 2.0. The IdP creates and deactivates RoutineOps accounts over a bearer-token-protected channel. The role is assigned by the group mapping below (Viewer by default).",
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
  mappingTitle: { ru: "Маппинг групп на роли", en: "Group-to-role mapping" },
  mappingIntro: {
    ru: "Роль SCIM-юзера вычисляется из групп, которые IdP присылает в SCIM User (поле groups). Пользователь в admin-группе получает «IT-администратор», остальные — роль по умолчанию. На обновлении роль пересчитывается: отзыв admin-группы в IdP понижает права.",
    en: "A SCIM user's role is derived from the groups the IdP sends in the SCIM User (groups field). A user in an admin group gets “IT admin”, everyone else gets the default role. On update the role is recomputed: revoking the admin group in the IdP downgrades the user.",
  },
  adminGroupsLabel: { ru: "Admin-группы (it_admin)", en: "Admin groups (it_admin)" },
  adminGroupsPlaceholder: { ru: "Admins, Platform-Ops", en: "Admins, Platform-Ops" },
  adminGroupsHint: {
    ru: "Список значений групп через запятую (value или display из SCIM). Совпадение любой из них даёт «IT-администратор». Пустой список = «IT-администратор» через SCIM никому не выдаётся (безопасно по умолчанию).",
    en: "Comma-separated group values (SCIM value or display). Matching any of them grants “IT admin”. An empty list means “IT admin” is never granted via SCIM (secure by default).",
  },
  defaultRoleLabel: { ru: "Роль по умолчанию", en: "Default role" },
  defaultRoleHint: {
    ru: "Роль для пользователей без admin-группы. «IT-администратор» здесь запрещён — повышение прав только явной admin-группой.",
    en: "Role for users without an admin group. “IT admin” is not allowed here — elevation only via an explicit admin group.",
  },
  roleViewer: { ru: "Наблюдатель", en: "Viewer" },
  save: { ru: "Сохранить маппинг", en: "Save mapping" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  saved: { ru: "Маппинг сохранён", en: "Mapping saved" },
  saveErr: { ru: "Не удалось сохранить маппинг", en: "Failed to save the mapping" },
}

export default function Scim() {
  const t = useT()
  const [cfg, setCfg] = useState<SCIMConfig | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [rotating, setRotating] = useState(false)
  const [newToken, setNewToken] = useState<string | null>(null)
  const [adminGroups, setAdminGroups] = useState("")
  const [defaultRole, setDefaultRole] = useState("viewer")
  const [savingMapping, setSavingMapping] = useState(false)

  async function load() {
    setLoadError(false)
    try {
      const r = await api.get<SCIMConfig>("/scim/config")
      setCfg(r.data)
      // Маппинг за той же лицензией, что и /scim/config: грузим следом. Мягкая ошибка —
      // оставляем дефолты (viewer, без admin-групп), страницу это не роняет.
      try {
        const m = await api.get<SCIMRoleMapping>("/scim/role-mapping")
        setAdminGroups(m.data.admin_group_values || "")
        setDefaultRole(m.data.default_role || "viewer")
      } catch { /* оставляем дефолты */ }
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

  async function saveMapping() {
    setSavingMapping(true)
    try {
      const r = await api.put<SCIMRoleMapping>("/scim/role-mapping", {
        admin_group_values: adminGroups,
        default_role: defaultRole,
      })
      setAdminGroups(r.data.admin_group_values || "")
      setDefaultRole(r.data.default_role || "viewer")
      toast({ title: t(M.saved) })
    } catch {
      toast({ title: t(M.saveErr), variant: "destructive" })
    } finally {
      setSavingMapping(false)
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

      {/* Маппинг групп SCIM на роли RoutineOps (it_admin через allowlist, иначе default). */}
      <div className="glass px-5 py-[18px] space-y-4">
        <div>
          <span className="text-[15px] font-semibold text-foreground">{t(M.mappingTitle)}</span>
          <p className="text-sm text-muted-foreground mt-1">{t(M.mappingIntro)}</p>
        </div>

        <div className="space-y-1.5">
          <label className="text-soft text-sm" htmlFor="scim-admin-groups">{t(M.adminGroupsLabel)}</label>
          <Input
            id="scim-admin-groups"
            value={adminGroups}
            onChange={(e) => setAdminGroups(e.target.value)}
            placeholder={t(M.adminGroupsPlaceholder)}
          />
          <p className="text-xs text-muted-foreground">{t(M.adminGroupsHint)}</p>
        </div>

        <div className="space-y-1.5">
          <label className="text-soft text-sm">{t(M.defaultRoleLabel)}</label>
          <Select
            value={defaultRole}
            onChange={setDefaultRole}
            options={[{ value: "viewer", label: t(M.roleViewer) }]}
            className="max-w-xs"
          />
          <p className="text-xs text-muted-foreground">{t(M.defaultRoleHint)}</p>
        </div>

        <div>
          <Button onClick={saveMapping} disabled={savingMapping}>
            {savingMapping ? t(M.saving) : t(M.save)}
          </Button>
        </div>
      </div>
    </div>
  )
}
