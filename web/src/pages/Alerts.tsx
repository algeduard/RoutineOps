import { useEffect, useState } from "react"
import { AlertTriangle, ChevronDown, ChevronRight } from "lucide-react"
import api, { Alert } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { formatDistanceToNow } from "@/lib/time"
import { toast } from "@/lib/toast"
import { useMe } from "@/lib/useMe"

const alertTypeLabel: Record<string, string> = {
  forbidden_software:             "Запрещённое ПО",
  unauthorized_install:           "Неавторизованная установка",
  unauthorized_settings_change:   "Изменение настроек",
  agent_unreachable:              "Агент недоступен",
}

const alertTypeColor: Record<string, string> = {
  forbidden_software:           "text-red-500",
  unauthorized_install:         "text-amber-500",
  unauthorized_settings_change: "text-orange-500",
  agent_unreachable:            "text-blue-500",
}

// TYPE_ORDER — порядок секций. Типы вне списка (сервер хранит alert_type свободным
// TEXT, без enum) уезжают в конец по алфавиту, а не пропадают.
const TYPE_ORDER = [
  "forbidden_software",
  "unauthorized_install",
  "unauthorized_settings_change",
  "agent_unreachable",
]

type AlertGroup = { type: string; alerts: Alert[]; unacked: number }

// groupByType сохраняет порядок алертов внутри секции (сервер отдаёт их created_at DESC).
function groupByType(alerts: Alert[]): AlertGroup[] {
  const buckets = new Map<string, Alert[]>()
  for (const a of alerts) {
    const list = buckets.get(a.alert_type)
    if (list) list.push(a)
    else buckets.set(a.alert_type, [a])
  }
  const rank = (t: string) => {
    const i = TYPE_ORDER.indexOf(t)
    return i === -1 ? TYPE_ORDER.length : i
  }
  return [...buckets.entries()]
    .map(([type, list]) => ({ type, alerts: list, unacked: list.filter((a) => !a.acknowledged_at).length }))
    .sort((a, b) => rank(a.type) - rank(b.type) || a.type.localeCompare(b.type))
}

export default function Alerts() {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [loading, setLoading] = useState(true)
  const [onlyNew, setOnlyNew] = useState(false)
  const [query, setQuery] = useState("")
  const [submitting, setSubmitting] = useState<string | null>(null)
  const [selectedAlert, setSelectedAlert] = useState<Alert | null>(null)
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const { isAdmin } = useMe()

  function toggleGroup(type: string) {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(type)) next.delete(type)
      else next.add(type)
      return next
    })
  }

  async function load() {
    try {
      const r = await api.get<Alert[]>("/alerts")
      setAlerts(r.data ?? [])
    } catch {
      toast({ title: "Не удалось загрузить алерты", variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  async function acknowledge(id: string, e?: React.MouseEvent) {
    e?.stopPropagation()
    setSubmitting(id)
    try {
      await api.post(`/alerts/${id}/acknowledge`, {})
      await load()
      if (selectedAlert?.id === id) setSelectedAlert(null)
    } catch {
      toast({ title: "Не удалось принять алерт", variant: "destructive" })
    } finally {
      setSubmitting(null)
    }
  }

  if (loading) return <p className="text-muted-foreground text-sm">Загрузка...</p>

  const unacked = alerts.filter((a) => !a.acknowledged_at)
  const q = query.trim().toLowerCase()
  const base = onlyNew ? unacked : alerts
  const visible = q
    ? base.filter((a) =>
        (a.device_hostname ?? "").toLowerCase().includes(q) || a.device_id.toLowerCase().includes(q),
      )
    : base
  const groups = groupByType(visible)

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold">Алерты</h1>
        {unacked.length > 0 && (
          <span className="flex items-center gap-1 rounded-full bg-red-500/15 px-2 py-0.5 text-xs font-semibold text-red-600 dark:text-red-400">
            <AlertTriangle className="h-3 w-3" />
            {unacked.length} новых
          </span>
        )}
        <button
          type="button"
          className={`ml-auto text-xs px-3 py-1 rounded-md border transition-colors ${onlyNew ? "bg-destructive/10 border-destructive/30 text-destructive" : "border-input text-muted-foreground hover:text-foreground"}`}
          onClick={() => setOnlyNew(!onlyNew)}
        >
          {onlyNew ? "Показать все" : "Только новые"}
        </button>
      </div>

      <Input
        placeholder="Поиск по устройству..."
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="max-w-sm"
      />

      {groups.length === 0 && (
        <div className="rounded-lg border py-10 text-center text-sm text-muted-foreground">
          Нет алертов
        </div>
      )}

      {/* Алерты сгруппированы по типу: «агент недоступен» и «запрещённое ПО» — разные
          инциденты, и разбирают их разные люди. Секции сворачиваются. */}
      {groups.map((g) => {
        const isCollapsed = collapsed.has(g.type)
        const color = alertTypeColor[g.type] ?? "text-foreground"
        return (
          <div key={g.type} className="rounded-lg border overflow-hidden">
            <button
              type="button"
              onClick={() => toggleGroup(g.type)}
              className="flex w-full items-center gap-2 px-4 py-2.5 text-left transition-colors hover:bg-muted/50"
            >
              {isCollapsed ? (
                <ChevronRight className="h-4 w-4 text-muted-foreground" />
              ) : (
                <ChevronDown className="h-4 w-4 text-muted-foreground" />
              )}
              <AlertTriangle className={`h-4 w-4 ${color}`} />
              <span className={`text-sm font-semibold ${color}`}>
                {alertTypeLabel[g.type] ?? g.type}
              </span>
              <span className="text-xs text-muted-foreground">{g.alerts.length}</span>
              {g.unacked > 0 && (
                <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-xs font-semibold text-red-600 dark:text-red-400">
                  {g.unacked} новых
                </span>
              )}
            </button>

            {!isCollapsed && (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Устройство</TableHead>
                    <TableHead>Детали</TableHead>
                    <TableHead>Создан</TableHead>
                    <TableHead>Статус</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {g.alerts.map((a) => (
                    <TableRow
                      key={a.id}
                      className={`cursor-pointer transition-colors ${!a.acknowledged_at ? "border-l-2 border-l-red-500" : ""}`}
                      onClick={() => setSelectedAlert(a)}
                    >
                      <TableCell className="text-sm font-medium">
                        {a.device_hostname || <span className="font-mono text-xs text-muted-foreground">{a.device_id.slice(0, 8)}</span>}
                      </TableCell>
                      <TableCell className="text-sm max-w-md truncate text-muted-foreground">{a.details || "—"}</TableCell>
                      <TableCell className="text-xs text-muted-foreground whitespace-nowrap">{formatDistanceToNow(a.created_at)}</TableCell>
                      <TableCell>
                        {a.acknowledged_at ? (
                          <Badge variant="secondary">Принято</Badge>
                        ) : (
                          <Badge variant="destructive">Новый</Badge>
                        )}
                      </TableCell>
                      <TableCell>
                        {isAdmin && !a.acknowledged_at && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={submitting === a.id}
                            onClick={(e) => acknowledge(a.id, e)}
                          >
                            {submitting === a.id ? "..." : "Принять"}
                          </Button>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        )
      })}

      {/* Alert detail dialog */}
      <Dialog open={!!selectedAlert} onOpenChange={(o) => !o && setSelectedAlert(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className={`h-4 w-4 ${selectedAlert ? (alertTypeColor[selectedAlert.alert_type] ?? "text-foreground") : ""}`} />
              {selectedAlert ? (alertTypeLabel[selectedAlert.alert_type] ?? selectedAlert.alert_type) : ""}
            </DialogTitle>
          </DialogHeader>
          {selectedAlert && (
            <div className="space-y-4 pt-1">
              <div className="grid grid-cols-2 gap-4 text-sm">
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">Устройство</p>
                  <p className="font-medium">{selectedAlert.device_hostname || selectedAlert.device_id.slice(0, 8)}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">Создан</p>
                  <p>{formatDistanceToNow(selectedAlert.created_at)}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">Статус</p>
                  {selectedAlert.acknowledged_at ? (
                    <Badge variant="secondary">Принято</Badge>
                  ) : (
                    <Badge variant="destructive">Новый</Badge>
                  )}
                </div>
              </div>

              {selectedAlert.details && (
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">Детали</p>
                  <div className="rounded-md border bg-muted px-3 py-2.5 text-sm font-mono break-all">
                    {selectedAlert.details}
                  </div>
                </div>
              )}

              {isAdmin && !selectedAlert.acknowledged_at && (
                <Button
                  className="w-full"
                  onClick={() => acknowledge(selectedAlert.id)}
                  disabled={submitting === selectedAlert.id}
                >
                  {submitting === selectedAlert.id ? "Принятие..." : "Принять алерт"}
                </Button>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
