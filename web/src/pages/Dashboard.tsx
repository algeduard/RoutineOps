import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Monitor, FileCode2, Shield, Bell, ChevronRight, Activity } from "lucide-react"
import api, { Device, Script, PolicyRule } from "@/lib/api"
import { formatDistanceToNow } from "@/lib/time"
import { toast } from "@/lib/toast"
import SpotlightCard from "@/components/SpotlightCard"

interface AuditEntry {
  id: string
  user_email: string
  action: string
  target_type: string
  created_at: string
}

interface Alert {
  id: string
  acknowledged: boolean
  created_at: string
  message?: string
  device_hostname?: string
}

const ONLINE_THRESHOLD_MS = 5 * 60 * 1000

const ACTION_LABELS: Record<string, string> = {
  block_device:          "заблокировал устройство",
  unblock_device:        "разблокировал устройство",
  approve_admin_request: "одобрил заявку на права",
  reject_admin_request:  "отклонил заявку на права",
  revoke_admin_request:  "отозвал права",
  create_device:         "добавил устройство",
  reenroll_device:       "перерегистрировал устройство",
  create_script:         "создал скрипт",
  update_script:         "изменил скрипт",
  delete_script:         "удалил скрипт",
  create_policy:         "создал политику",
  delete_policy:         "удалил политику",
  run_script:            "запустил скрипт",
}

function osFamily(os: string): "macOS" | "Windows" | "Linux" {
  const l = os.toLowerCase()
  if (l.includes("mac") || l.includes("darwin")) return "macOS"
  if (l.includes("win")) return "Windows"
  // всё остальное (linux/ubuntu/debian/centos/прочие дистрибутивы) — Linux-парк
  return "Linux"
}

const OS_COLOR: Record<string, string> = {
  macOS:   "bg-blue-500",
  Windows: "bg-violet-500",
  Linux:   "bg-amber-500",
}

export default function Dashboard() {
  const navigate = useNavigate()
  const [devices, setDevices]   = useState<Device[]>([])
  const [scripts, setScripts]   = useState<Script[]>([])
  const [policies, setPolicies] = useState<PolicyRule[]>([])
  const [activity, setActivity] = useState<AuditEntry[]>([])
  const [alerts, setAlerts]     = useState<Alert[]>([])
  const [loading, setLoading]   = useState(true)

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
      toast({ title: "Не удалось загрузить данные", variant: "destructive" })
    }).finally(() => setLoading(false))
  }, [])

  const now = Date.now()
  const active   = devices.filter((d) => d.status === "active").length
  const enrolled = devices.filter((d) => d.status === "enrolled").length
  const pending  = devices.filter((d) => d.status === "pending").length
  const online   = devices.filter((d) => d.last_seen_at && now - new Date(d.last_seen_at).getTime() < ONLINE_THRESHOLD_MS).length
  const unackedAlerts = alerts.filter((a) => !a.acknowledged).length

  const osCounts = devices.reduce<Record<string, number>>((acc, d) => {
    const fam = osFamily(d.os)
    acc[fam] = (acc[fam] ?? 0) + 1
    return acc
  }, {})
  const osEntries = Object.entries(osCounts).sort((a, b) => b[1] - a[1])
  const maxOS = Math.max(...osEntries.map(([, n]) => n), 1)

  if (loading) {
    return <div className="flex items-center justify-center h-48 text-muted-foreground text-sm">Загрузка...</div>
  }

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold">Обзор</h1>

      {/* Stat cards */}
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        {/* Цветом метим только то, что требует внимания: непрочитанные алерты. */}
        {[
          { label: "Всего устройств", value: devices.length, icon: Monitor,   sub: `${online} онлайн`, accent: "border-t-brand",     iconColor: "text-brand",            onClick: () => navigate("/devices")  },
          { label: "Скриптов",        value: scripts.length,  icon: FileCode2, sub: "в библиотеке",     accent: "border-t-brand",     iconColor: "text-brand",            onClick: () => navigate("/scripts")  },
          { label: "Политик",         value: policies.length, icon: Shield,    sub: "правил ПО",        accent: "border-t-brand",     iconColor: "text-brand",            onClick: () => navigate("/policies") },
          { label: "Алертов",         value: unackedAlerts,   icon: Bell,      sub: "без ответа",       accent: unackedAlerts > 0 ? "border-t-destructive" : "border-t-border", iconColor: unackedAlerts > 0 ? "text-destructive" : "text-muted-foreground", onClick: () => navigate("/alerts") },
        ].map(({ label, value, icon: Icon, sub, accent, iconColor, onClick }) => (
          <SpotlightCard
            as="button"
            type="button"
            key={label}
            onClick={onClick}
            // Акцентная кромка идёт ПОСЛЕ `border-border`: className проходит через
            // tailwind-merge внутри SpotlightCard, и более поздний класс выигрывает.
            className={`rounded-lg border border-border border-t-2 ${accent} bg-card p-4 text-left hover:bg-accent/50 transition-colors`}
          >
            <div className="flex items-center justify-between mb-3">
              <span className="text-xs text-muted-foreground">{label}</span>
              <Icon className={`h-4 w-4 ${iconColor}`} />
            </div>
            <p className="text-2xl font-semibold tabular-nums">{value}</p>
            <p className="text-xs text-muted-foreground mt-1">{sub}</p>
          </SpotlightCard>
        ))}
      </div>

      {/* Two-column section */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-5">

        {/* Left: Devices by OS + status breakdown */}
        <div className="lg:col-span-2 space-y-4">
          <div className="rounded-lg border bg-card p-4">
            <h2 className="text-sm font-medium mb-4">Устройства по ОС</h2>
            {osEntries.length === 0 ? (
              <p className="text-xs text-muted-foreground">Нет данных</p>
            ) : (
              <div className="space-y-3">
                {osEntries.map(([os, count]) => (
                  <div key={os}>
                    <div className="flex items-center justify-between mb-1">
                      <span className="text-xs text-muted-foreground">{os}</span>
                      <span className="text-xs font-medium">{count}</span>
                    </div>
                    <div className="h-2 w-full rounded-full bg-muted overflow-hidden">
                      <div
                        className={`h-full rounded-full ${OS_COLOR[os] ?? "bg-zinc-500"} transition-all`}
                        style={{ width: `${(count / maxOS) * 100}%` }}
                      />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Status breakdown */}
          <div className="rounded-lg border bg-card p-4">
            <h2 className="text-sm font-medium mb-3">Статусы</h2>
            <div className="space-y-2">
              {[
                { label: "Активных",           count: active,   dot: "bg-emerald-500" },
                { label: "Зарегистрированных", count: enrolled, dot: "bg-blue-500"    },
                { label: "Ожидающих",          count: pending,  dot: "bg-amber-500"   },
                { label: "Заблокированных",    count: devices.filter((d) => d.status === "blocked").length, dot: "bg-red-500" },
              ].map(({ label, count, dot }) => (
                <div key={label} className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className={`h-2 w-2 rounded-full ${dot}`} />
                    <span className="text-xs text-muted-foreground">{label}</span>
                  </div>
                  <span className="text-xs font-medium">{count}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* Right: Activity feed */}
        <div className="lg:col-span-3 rounded-lg border bg-card">
          <div className="flex items-center justify-between px-4 py-3 border-b">
            <div className="flex items-center gap-2">
              <Activity className="h-4 w-4 text-muted-foreground" />
              <h2 className="text-sm font-medium">Активность</h2>
            </div>
            <button
              type="button"
              onClick={() => navigate("/audit-log")}
              className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
            >
              Все события <ChevronRight className="h-3 w-3" />
            </button>
          </div>
          <div className="divide-y">
            {activity.length === 0 && (
              <p className="text-xs text-muted-foreground px-4 py-6 text-center">Нет событий</p>
            )}
            {activity.map((e) => (
              <div key={e.id} className="flex items-start gap-3 px-4 py-3">
                <div className="mt-0.5 h-6 w-6 rounded-full bg-muted flex items-center justify-center flex-shrink-0">
                  <span className="text-[10px] font-semibold text-muted-foreground uppercase">
                    {e.user_email.slice(0, 2)}
                  </span>
                </div>
                <div className="min-w-0">
                  <p className="text-xs leading-relaxed">
                    <span className="font-medium">{e.user_email}</span>
                    {" "}
                    <span className="text-muted-foreground">{ACTION_LABELS[e.action] ?? e.action}</span>
                  </p>
                  <p className="text-[11px] text-muted-foreground mt-0.5">{formatDistanceToNow(e.created_at)}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Recent devices */}
      <div className="rounded-lg border bg-card">
        <div className="flex items-center justify-between px-4 py-3 border-b">
          <h2 className="text-sm font-medium">Последние устройства</h2>
          <button
            type="button"
            onClick={() => navigate("/devices")}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            Все устройства <ChevronRight className="h-3 w-3" />
          </button>
        </div>
        <div className="divide-y">
          {devices.slice(0, 5).map((d) => {
            const statusColor: Record<Device["status"], string> = {
              active:   "bg-emerald-500",
              enrolled: "bg-blue-500",
              pending:  "bg-amber-500",
              blocked:  "bg-red-500",
            }
            return (
              <button
                type="button"
                key={d.id}
                onClick={() => navigate(`/devices/${d.id}`)}
                className="w-full flex items-center justify-between px-4 py-3 hover:bg-accent/50 transition-colors text-left"
              >
                <div className="flex items-center gap-3 min-w-0">
                  <span className={`h-2 w-2 rounded-full flex-shrink-0 ${statusColor[d.status]}`} />
                  <div className="min-w-0">
                    <p className="text-sm font-medium truncate">{d.hostname}</p>
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
