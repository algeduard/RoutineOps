import { useEffect, useRef, useState, type ElementType, type CSSProperties } from "react"
import { useNavigate } from "react-router-dom"
import { Monitor, FileCode2, Shield, Bell, ChevronRight, ShieldAlert, KeyRound, UserCog } from "lucide-react"
import api, { Device, Script, PolicyRule, Alert, DEVICE_STATUS } from "@/lib/api"
import { formatDistanceToNow } from "@/lib/time"
import { toast } from "@/lib/toast"
import SpotlightCard from "@/components/SpotlightCard"
import { useT, type Msg } from "@/lib/i18n"

interface AuditEntry {
  id: string
  user_email: string
  action: string
  target_type: string
  created_at: string
}

const ONLINE_THRESHOLD_MS = 5 * 60 * 1000

// Счётчик читается «Активных: 12», а бейдж на карточке — «Активен». Одна карта на оба
// падежа звучала бы криво в одном из мест, поэтому здесь только форма для счётчиков;
// цвет и порядок по-прежнему берутся из общей DEVICE_STATUS.
const STATUS_PLURAL: Record<string, Msg> = {
  active:           { ru: "Активных", en: "Active" },
  enrolled:         { ru: "Зарегистрированных", en: "Enrolled" },
  pending:          { ru: "Ожидающих", en: "Pending" },
  pending_approval: { ru: "Ожидают одобрения", en: "Pending approval" },
  rejected:         { ru: "Отклонённых", en: "Rejected" },
  blocked:          { ru: "Заблокированных", en: "Blocked" },
  decommissioned:   { ru: "Выведенных из эксплуатации", en: "Decommissioned" },
}

const ACTION_LABELS: Record<string, Msg> = {
  block_device:          { ru: "заблокировал устройство", en: "blocked a device" },
  unblock_device:        { ru: "разблокировал устройство", en: "unblocked a device" },
  approve_admin_request: { ru: "одобрил заявку на права", en: "approved an access request" },
  reject_admin_request:  { ru: "отклонил заявку на права", en: "rejected an access request" },
  revoke_admin_request:  { ru: "отозвал права", en: "revoked access" },
  create_device:         { ru: "добавил устройство", en: "added a device" },
  delete_device:         { ru: "удалил устройство", en: "deleted a device" },
  approve_device:        { ru: "одобрил устройство", en: "approved a device" },
  reject_device:         { ru: "отклонил устройство", en: "rejected a device" },
  approve_pending_bulk:  { ru: "одобрил очередь энроллмента", en: "approved the enrollment queue" },
  reject_pending_bulk:   { ru: "отклонил очередь энроллмента", en: "rejected the enrollment queue" },
  create_bulk_token:     { ru: "выпустил массовый токен", en: "issued a bulk token" },
  decommission_device:   { ru: "вывел устройство из эксплуатации", en: "decommissioned a device" },
  create_api_token:      { ru: "выпустил API-токен", en: "issued an API token" },
  revoke_api_token:      { ru: "отозвал API-токен", en: "revoked an API token" },
  reenroll_device:       { ru: "перерегистрировал устройство", en: "re-enrolled a device" },
  lock_device:           { ru: "заблокировал экран устройства", en: "locked a device screen" },
  unlock_device:         { ru: "разблокировал экран устройства", en: "unlocked a device screen" },
  create_script:         { ru: "создал скрипт", en: "created a script" },
  update_script:         { ru: "изменил скрипт", en: "updated a script" },
  delete_script:         { ru: "удалил скрипт", en: "deleted a script" },
  create_policy:         { ru: "создал политику", en: "created a policy" },
  delete_policy:         { ru: "удалил политику", en: "deleted a policy" },
  run_script:            { ru: "запустил скрипт", en: "ran a script" },
  run_script_on_group:   { ru: "запустил скрипт на группе", en: "ran a script on a group" },
  create_script_policy:  { ru: "создал скрипт-политику", en: "created a script policy" },
  delete_script_policy:  { ru: "удалил скрипт-политику", en: "deleted a script policy" },
  enable_script_policy:  { ru: "включил скрипт-политику", en: "enabled a script policy" },
  disable_script_policy: { ru: "выключил скрипт-политику", en: "disabled a script policy" },
  acknowledge_alert:     { ru: "подтвердил алерт", en: "acknowledged an alert" },
  login:                 { ru: "вошёл в систему", en: "signed in" },
  logout:                { ru: "вышел из системы", en: "signed out" },
  login_failed:          { ru: "неудачная попытка входа", en: "failed sign-in attempt" },
  change_password:       { ru: "сменил пароль", en: "changed password" },
  password_reset_requested: { ru: "запросил сброс пароля", en: "requested a password reset" },
  password_reset:        { ru: "сбросил пароль", en: "reset password" },
  invite_user:           { ru: "пригласил пользователя", en: "invited a user" },
  accept_invite:         { ru: "принял приглашение", en: "accepted an invitation" },
  create_device_group:   { ru: "создал группу устройств", en: "created a device group" },
  update_device_group:   { ru: "изменил группу устройств", en: "updated a device group" },
  delete_device_group:   { ru: "удалил группу устройств", en: "deleted a device group" },
  add_device_to_group:   { ru: "добавил устройство в группу", en: "added a device to a group" },
  remove_device_from_group: { ru: "убрал устройство из группы", en: "removed a device from a group" },
  assign_policy_to_group:   { ru: "назначил группе политику", en: "assigned a policy to a group" },
  unassign_policy_from_group: { ru: "снял с группы политику", en: "unassigned a policy from a group" },
  assign_software_policy_to_group:   { ru: "назначил группе политику ПО", en: "assigned a software policy to a group" },
  unassign_software_policy_from_group: { ru: "снял с группы политику ПО", en: "unassigned a software policy from a group" },
}

// Таксономия событий ленты: security должно цепляться взглядом сразу,
// остальные категории различаются иконкой и сдержанным цветовым акцентом.
type EventCategory = "security" | "auth" | "admin" | "device" | "content"

const ACTION_CATEGORY: Record<string, EventCategory> = {
  login_failed: "security", block_device: "security", lock_device: "security",
  login: "auth", logout: "auth", change_password: "auth",
  password_reset: "auth", password_reset_requested: "auth",
  invite_user: "admin", accept_invite: "admin",
  approve_admin_request: "admin", reject_admin_request: "admin", revoke_admin_request: "admin",
  create_device: "device", delete_device: "device", reenroll_device: "device",
  unblock_device: "device", unlock_device: "device",
  // Выпуск токена и одобрение — выдача доступа к парку, это security, а не «контент»:
  // без явной категории они падали в content и рисовались нейтральной иконкой.
  create_bulk_token: "security", approve_device: "security", approve_pending_bulk: "security",
  create_api_token: "security", revoke_api_token: "security",
  reject_device: "device", reject_pending_bulk: "device", decommission_device: "device",
  // Запуск скрипта — исполнение кода на устройстве/парке, не правка контента.
  run_script: "device", run_script_on_group: "device",
  create_device_group: "device", update_device_group: "device", delete_device_group: "device",
  add_device_to_group: "device", remove_device_from_group: "device",
  // всё остальное (скрипты/политики/алерты) — content по умолчанию
}

const CATEGORY_STYLE: Record<EventCategory, { icon: ElementType; fg: string; bg: string }> = {
  // red-700 в светлой теме: red-500 на белом даёт 3.57:1 — ниже AA для text-xs.
  security: { icon: ShieldAlert, fg: "text-red-700 dark:text-red-400",         bg: "bg-red-500/10" },
  auth:     { icon: KeyRound,    fg: "text-sky-600 dark:text-sky-400",         bg: "bg-sky-500/10" },
  admin:    { icon: UserCog,     fg: "text-violet-600 dark:text-violet-400",   bg: "bg-violet-500/10" },
  device:   { icon: Monitor,     fg: "text-emerald-600 dark:text-emerald-400", bg: "bg-emerald-500/10" },
  content:  { icon: FileCode2,   fg: "text-muted-foreground",                  bg: "bg-muted" },
}

// Count-up цифр карточек при первой загрузке. Уважает prefers-reduced-motion.
// Анимирует от последнего показанного значения, не от нуля — чтобы при
// будущем рефреше данных цифра не прыгала в 0.
function useCountUp(target: number, duration = 600): number {
  const [value, setValue] = useState(0)
  const lastRef = useRef(0)
  useEffect(() => {
    const from = lastRef.current
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches || from === target) {
      lastRef.current = target
      setValue(target)
      return
    }
    let raf = 0
    const start = performance.now()
    const tick = (t: number) => {
      const p = Math.min((t - start) / duration, 1)
      const v = Math.round(from + (target - from) * (1 - Math.pow(1 - p, 3)))
      lastRef.current = v
      setValue(v)
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [target, duration])
  return value
}

function StatValue({ value, className }: { value: number; className?: string }) {
  // 30px/300 из хендоффа: крупная тонкая цифра — главный якорь плитки.
  return <p className={`text-[30px] font-light leading-[1.1] tabular-nums mt-0.5 ${className ?? "text-foreground"}`}>{useCountUp(value)}</p>
}

function osFamily(os: string): "macOS" | "Windows" | "Linux" {
  const l = os.toLowerCase()
  if (l.includes("mac") || l.includes("darwin")) return "macOS"
  if (l.includes("win")) return "Windows"
  // всё остальное (linux/ubuntu/debian/centos/прочие дистрибутивы) — Linux-парк
  return "Linux"
}


const M = {
  loadError: { ru: "Не удалось загрузить данные", en: "Failed to load data" },
  loading: { ru: "Загрузка...", en: "Loading..." },
  title: { ru: "Обзор", en: "Overview" },
  statDevices: { ru: "Всего устройств", en: "Total devices" },
  statScripts: { ru: "Скриптов", en: "Scripts" },
  statPolicies: { ru: "Политик", en: "Policies" },
  statAlerts: { ru: "Алертов", en: "Alerts" },
  online: { ru: "{n} онлайн", en: "{n} online" },
  inLibrary: { ru: "в библиотеке", en: "in library" },
  softwareRules: { ru: "правил ПО", en: "software rules" },
  unacknowledged: { ru: "неподтверждённых", en: "unacknowledged" },
  ctaAddDevice: { ru: "Подключить устройство", en: "Connect a device" },
  ctaAddScript: { ru: "Добавить скрипт", en: "Add a script" },
  ctaAddPolicy: { ru: "Добавить политику", en: "Add a policy" },
  devicesByOs: { ru: "Устройства по ОС", en: "Devices by OS" },
  totalN: { ru: "Всего {n}", en: "Total {n}" },
  noData: { ru: "Нет данных", en: "No data" },
  statuses: { ru: "Статусы", en: "Statuses" },
  fleetDistribution: { ru: "Распределение парка", en: "Fleet distribution" },
  noDevices: { ru: "Нет устройств", en: "No devices" },
  activity: { ru: "Активность", en: "Activity" },
  recentEvents: { ru: "Последние события", en: "Recent events" },
  allEvents: { ru: "Все события", en: "All events" },
  noEvents: { ru: "Нет событий", en: "No events" },
  recentDevices: { ru: "Последние устройства", en: "Recent devices" },
  recentlyOnline: { ru: "Недавно на связи", en: "Recently online" },
  allDevices: { ru: "Все устройства", en: "All devices" },
}

export default function Dashboard() {
  const t = useT()
  const navigate = useNavigate()
  const [devices, setDevices]   = useState<Device[]>([])
  const [scripts, setScripts]   = useState<Script[]>([])
  const [policies, setPolicies] = useState<PolicyRule[]>([])
  const [activity, setActivity] = useState<AuditEntry[]>([])
  const [alerts, setAlerts]     = useState<Alert[]>([])
  const [loading, setLoading]   = useState(true)
  // При ошибке загрузки нули — не «пустой парк», CTA показывать нечестно.
  const [loadFailed, setLoadFailed] = useState(false)

  useEffect(() => {
    Promise.all([
      api.get<Device[]>("/devices"),
      api.get<Script[]>("/scripts"),
      api.get<PolicyRule[]>("/policies"),
      api.get<AuditEntry[]>("/audit-log?limit=12"),
      api.get<Alert[]>("/alerts"),
    ]).then(([d, s, p, a, al]) => {
      setDevices(d.data ?? [])
      setScripts(s.data ?? [])
      setPolicies(p.data ?? [])
      setActivity(a.data ?? [])
      setAlerts(al.data ?? [])
    }).catch(() => {
      setLoadFailed(true)
      toast({ title: t(M.loadError), variant: "destructive" })
    }).finally(() => setLoading(false))
  }, [])

  const now = Date.now()
  // Считаем ПО ФАКТУ, а не перечислением статусов руками: раньше карточка знала четыре
  // строки, сумма молча расходилась с «Всего устройств», а строка «Ожидающих» вообще
  // была мёртвой — считала литеральный 'pending', который сервер не отдаёт
  // (ListEnrolledDevices режет его в SQL). Порядок строк — как в DEVICE_STATUS.
  const statusCounts = devices.reduce<Record<string, number>>((acc, d) => {
    acc[d.status] = (acc[d.status] ?? 0) + 1
    return acc
  }, {})
  const statusOrder = Object.keys(DEVICE_STATUS)
  const statusRows = Object.entries(statusCounts)
    .sort((a, b) => statusOrder.indexOf(a[0]) - statusOrder.indexOf(b[0]))
    .map(([status, count]) => ({
      label: STATUS_PLURAL[status] ? t(STATUS_PLURAL[status]) : (DEVICE_STATUS[status as keyof typeof DEVICE_STATUS]?.label ?? status),
      dot: DEVICE_STATUS[status as keyof typeof DEVICE_STATUS]?.dot ?? "bg-muted-foreground/40",
      count,
    }))
  const online   = devices.filter((d) => d.last_seen_at && now - new Date(d.last_seen_at).getTime() < ONLINE_THRESHOLD_MS).length
  // API отдаёт acknowledged_at (timestamp | null), поля `acknowledged` не существует —
  // старый фильтр по нему считал ВСЕ алерты непринятыми.
  const unackedAlerts = alerts.filter((a) => !a.acknowledged_at).length

  const osCounts = devices.reduce<Record<string, number>>((acc, d) => {
    const fam = osFamily(d.os)
    acc[fam] = (acc[fam] ?? 0) + 1
    return acc
  }, {})
  const osEntries = Object.entries(osCounts).sort((a, b) => b[1] - a[1])
  // Доли part-to-whole: масштаб от общего числа устройств, не от максимума —
  // иначе самая крупная ОС всегда рисуется на 100% и полосы визуально врут.
  const totalDevices = Math.max(devices.length, 1)

  if (loading) {
    return <div className="flex items-center justify-center h-48 text-muted-foreground text-sm">{t(M.loading)}</div>
  }

  return (
    <div className="flex flex-col gap-5">
      <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>

      {/* Stat cards */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {/* Цветом метим только то, что требует внимания: непрочитанные алерты
            (красно-оранжевая колонка и цифра). Нулевые счётчики превращаем в CTA —
            три нуля подряд читаются как «заброшенный продукт». */}
        {[
          { label: t(M.statDevices), value: devices.length, icon: Monitor,   sub: t(M.online, { n: online }), cta: t(M.ctaAddDevice), onClick: () => navigate("/devices")  },
          { label: t(M.statScripts),        value: scripts.length,  icon: FileCode2, sub: t(M.inLibrary),     cta: t(M.ctaAddScript),       onClick: () => navigate("/scripts")  },
          { label: t(M.statPolicies),         value: policies.length, icon: Shield,    sub: t(M.softwareRules),        cta: t(M.ctaAddPolicy),     onClick: () => navigate("/policies") },
          { label: t(M.statAlerts),         value: unackedAlerts,   icon: Bell,      sub: t(M.unacknowledged), cta: "",                      onClick: () => navigate("/alerts"), alert: true },
        ].map(({ label, value, icon: Icon, sub, cta, onClick, alert }) => (
          <SpotlightCard
            as="button"
            type="button"
            key={label}
            onClick={onClick}
            className="glass glass-hover flex min-h-[104px] overflow-hidden text-left"
          >
            {/* Градиентная колонка 64px с иконкой — единственный цветной элемент
                плитки; у алертов она красно-оранжевая. */}
            <div className={`w-16 flex-shrink-0 flex items-center justify-center text-white ${alert && value > 0 ? "alert-gradient" : "brand-gradient"}`}>
              <Icon className="h-[26px] w-[26px]" strokeWidth={2} />
            </div>
            <div className="flex-1 min-w-0 flex flex-col px-4 py-3.5">
              <span className="text-[13px] text-muted-foreground truncate">{label}</span>
              <StatValue value={value} className={alert && value > 0 ? "text-[hsl(0_62%_45%)] dark:text-[hsl(0_72%_66%)]" : "text-foreground"} />
              {value === 0 && cta && !loadFailed ? (
                // В светлой теме --brand (52%) даёт ~4:1 на белом — ниже AA для
                // text-xs, поэтому CTA затемнён той же тональностью (~6.8:1).
                <p className="text-xs font-medium text-[hsl(220_65%_42%)] dark:text-brand mt-auto">{cta} →</p>
              ) : sub ? (
                <p className="text-xs text-muted-foreground mt-auto truncate">{sub}</p>
              ) : null}
            </div>
          </SpotlightCard>
        ))}
      </div>

      {/* Two-column section: 2fr / 3fr. Именно так, а не grid-cols-5 + col-span:
          при пяти равных колонках gap считается иначе и пропорция уезжает. */}
      <div className="grid grid-cols-1 gap-5 lg:grid-cols-[2fr_3fr]">

        {/* Left: Devices by OS + status breakdown */}
        <div className="flex flex-col gap-5">
          <div className="glass px-5 py-[18px]">
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.devicesByOs)}</h2>
            <p className="text-xs text-muted-foreground mb-4">{t(M.totalN, { n: devices.length })}</p>
            {osEntries.length === 0 ? (
              <p className="text-xs text-muted-foreground">{t(M.noData)}</p>
            ) : (
              <div className="flex flex-col gap-3.5">
                {osEntries.map(([os, count]) => (
                  <div key={os}>
                    <div className="flex items-center justify-between mb-1.5">
                      <span className="text-[13px] text-soft">{os}</span>
                      <span className="text-[13px] text-foreground tabular-nums">
                        {count}
                        <span className="text-muted-foreground"> · {Math.round((count / totalDevices) * 100)}%</span>
                      </span>
                    </div>
                    {/* Полосы одного фирменного градиента: доля читается длиной,
                        разноцветные ОС добавляли смысл, которого нет. */}
                    <div className="h-2 w-full rounded-full bg-muted overflow-hidden">
                      <div
                        className="h-full rounded-full brand-gradient-h transition-all"
                        style={{ width: `${(count / totalDevices) * 100}%` }}
                      />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Status breakdown */}
          <div className="glass px-5 py-[18px]">
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.statuses)}</h2>
            <p className="text-xs text-muted-foreground mb-3.5">{t(M.fleetDistribution)}</p>
            <div className="flex flex-col gap-2.5">
              {statusRows.length === 0 && (
                <p className="text-xs text-muted-foreground">{t(M.noDevices)}</p>
              )}
              {statusRows.map(({ label, count, dot }) => (
                <div key={label} className="flex items-center justify-between">
                  <div className="flex items-center gap-2.5">
                    <span className={`h-2 w-2 rounded-full ${dot}`} />
                    <span className="text-[13px] text-soft">{label}</span>
                  </div>
                  <span className="text-[13px] font-semibold text-foreground tabular-nums">{count}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* Right: Activity feed */}
        <div className="glass flex flex-col">
          <div className="flex items-center justify-between px-5 pt-4 pb-3">
            <div>
              <h2 className="text-[15px] font-semibold text-foreground">{t(M.activity)}</h2>
              <p className="text-xs text-muted-foreground">{t(M.recentEvents)}</p>
            </div>
            <button
              type="button"
              onClick={() => navigate("/audit-log")}
              className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
            >
              {t(M.allEvents)} <ChevronRight className="h-3.5 w-3.5" />
            </button>
          </div>
          <div>
            {activity.length === 0 && (
              <p className="text-xs text-muted-foreground px-5 py-6 text-center">{t(M.noEvents)}</p>
            )}
            {activity.map((e, i) => {
              const cat = ACTION_CATEGORY[e.action] ?? "content"
              const { icon: CatIcon, fg, bg } = CATEGORY_STYLE[cat]
              return (
                <div
                  key={e.id}
                  // last:rounded-b-2xl обязателен: у security-строки есть красная
                  // подложка, и без скругления она заливала бы нижние углы карты.
                  className={`feed-item flex items-start gap-3 px-5 py-2.5 border-t border-border last:rounded-b-2xl ${cat === "security" ? "bg-red-500/[0.06]" : ""}`}
                  style={{ "--i": i } as CSSProperties}
                >
                  <div className={`mt-px h-[26px] w-[26px] rounded-full ${bg} flex items-center justify-center flex-shrink-0`}>
                    <CatIcon className={`h-3.5 w-3.5 ${fg}`} />
                  </div>
                  <div className="min-w-0">
                    <p className="text-[13px] leading-snug text-soft">
                      <span className="font-medium text-foreground">{e.user_email}</span>
                      {" "}
                      <span className={cat === "security" ? fg : "text-muted-foreground"}>
                        {ACTION_LABELS[e.action] ? t(ACTION_LABELS[e.action]) : e.action}
                      </span>
                    </p>
                    <p className="text-[11px] text-muted-foreground mt-0.5">{formatDistanceToNow(e.created_at)}</p>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </div>

      {/* Recent devices */}
      <div className="glass">
        <div className="flex items-center justify-between px-5 pt-4 pb-3">
          <div>
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.recentDevices)}</h2>
            <p className="text-xs text-muted-foreground">{t(M.recentlyOnline)}</p>
          </div>
          <button
            type="button"
            onClick={() => navigate("/devices")}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            {t(M.allDevices)} <ChevronRight className="h-3.5 w-3.5" />
          </button>
        </div>
        <div>
          {devices.length === 0 && (
            <p className="text-xs text-muted-foreground px-5 py-6 text-center">{t(M.noDevices)}</p>
          )}
          {devices.slice(0, 5).map((d) => {
            // Фолбэк не декоративный: без него неизвестный статус давал className
            // "... undefined" — точка молча становилась невидимой.
            const dot = DEVICE_STATUS[d.status]?.dot ?? "bg-muted-foreground/40"
            return (
              <button
                type="button"
                key={d.id}
                onClick={() => navigate(`/devices/${d.id}`)}
                className="w-full flex items-center justify-between px-5 py-3 border-t border-border glass-hover text-left last:rounded-b-2xl"
              >
                <div className="flex items-center gap-3 min-w-0">
                  <span className={`h-2 w-2 rounded-full flex-shrink-0 ${dot}`} />
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-foreground truncate">{d.hostname}</p>
                    <p className="text-xs text-muted-foreground">{d.os}</p>
                  </div>
                </div>
                <div className="flex items-center gap-4 flex-shrink-0 ml-4">
                  <span className="text-xs text-muted-foreground hidden sm:block">
                    {d.ip_address || "—"}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {d.last_seen_at ? formatDistanceToNow(d.last_seen_at) : "—"}
                  </span>
                  <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
                </div>
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}
