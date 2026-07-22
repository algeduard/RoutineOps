import { useEffect, useState } from "react"
import api, { AdminAccessRequest, AdminSoftwareDelta } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
import { formatDistanceToNow } from "@/lib/time"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

// Границы совпадают с серверными (respondAdminRequest): 1 минута .. 30 суток.
const MIN_DURATION_SECONDS = 60
const MAX_DURATION_SECONDS = 30 * 24 * 3600

type DurationUnit = "minutes" | "hours"

const unitSeconds: Record<DurationUnit, number> = { minutes: 60, hours: 3600 }

const statusLabel: Record<string, Msg> = {
  pending: { ru: "Ожидает", en: "Pending" },
  approved: { ru: "Одобрено", en: "Approved" },
  rejected: { ru: "Отклонено", en: "Rejected" },
  expired: { ru: "Истекло", en: "Expired" },
  revoked: { ru: "Отозвано", en: "Revoked" },
}

const M = {
  loadErr: { ru: "Не удалось загрузить заявки", en: "Failed to load requests" },
  respondErr: { ru: "Не удалось обработать заявку", en: "Failed to process request" },
  revokeErr: { ru: "Не удалось отозвать права", en: "Failed to revoke access" },
  loading: { ru: "Загрузка...", en: "Loading..." },
  title: { ru: "Заявки на права", en: "Access requests" },
  cardTitle: { ru: "Запросы доступа", en: "Access requests" },
  cardSubtitle: { ru: "Временные права администратора на устройстве", en: "Temporary administrator access on a device" },
  searchPlaceholder: { ru: "Поиск по устройству...", en: "Search by device..." },
  thDevice: { ru: "Устройство", en: "Device" },
  thReason: { ru: "Причина", en: "Reason" },
  thRequested: { ru: "Запрошено", en: "Requested" },
  thExpires: { ru: "Истекает", en: "Expires" },
  thStatus: { ru: "Статус", en: "Status" },
  emptyNone: { ru: "Нет заявок", en: "No requests" },
  emptySearch: { ru: "Ничего не найдено", en: "Nothing found" },
  reasonFullHint: { ru: "Нажмите, чтобы увидеть полностью", en: "Click to view in full" },
  approve: { ru: "Одобрить", en: "Approve" },
  approveTitle: { ru: "Одобрить доступ", en: "Approve access" },
  deviceColon: { ru: "Устройство:", en: "Device:" },
  duration: { ru: "Срок действия", en: "Duration" },
  unitMinutes: { ru: "минут", en: "minutes" },
  unitHours: { ru: "часов", en: "hours" },
  durationHint: { ru: "От 1 минуты до 30 суток, целое число.", en: "From 1 minute to 30 days, whole number." },
  sending: { ru: "Отправка...", en: "Submitting..." },
  confirm: { ru: "Подтвердить", en: "Confirm" },
  reject: { ru: "Отклонить", en: "Reject" },
  revoke: { ru: "Отозвать", en: "Revoke" },
  reasonTitle: { ru: "Причина запроса", en: "Request reason" },
  sessionChanges: { ru: "Изменения ПО за сессию", en: "Software changes during session" },
  installed: { ru: "Установлено", en: "Installed" },
  removed: { ru: "Удалено", en: "Removed" },
  noChanges: { ru: "Изменений ПО не зафиксировано", en: "No software changes recorded" },
  inProgress: { ru: "Сессия ещё активна — дельта появится после снятия прав", en: "Session still active — the delta will appear once access is revoked" },
}

const statusVariant: Record<string, "default" | "secondary" | "success" | "destructive" | "outline"> = {
  pending: "secondary",
  approved: "success",
  rejected: "destructive",
  expired: "outline",
  revoked: "outline",
}

// Строки таблицы разделяются верхней границей (как ленты на «Обзоре»),
// поэтому border-b примитива гасится, а border-t проставляется явно.
const ROW = "hover:bg-transparent"

export default function AdminAccess() {
  const t = useT()
  const [requests, setRequests] = useState<AdminAccessRequest[]>([])
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(true)
  const [approveOpen, setApproveOpen] = useState<string | null>(null)
  const [durationValue, setDurationValue] = useState("1")
  const [durationUnit, setDurationUnit] = useState<DurationUnit>("hours")
  const [submitting, setSubmitting] = useState(false)
  const [reasonReq, setReasonReq] = useState<AdminAccessRequest | null>(null)
  const [delta, setDelta] = useState<AdminSoftwareDelta | null>(null)
  const [deltaLoading, setDeltaLoading] = useState(false)

  const durationSeconds = Number(durationValue) * unitSeconds[durationUnit]
  const durationValid =
    Number.isInteger(Number(durationValue)) &&
    durationSeconds >= MIN_DURATION_SECONDS &&
    durationSeconds <= MAX_DURATION_SECONDS

  async function load() {
    try {
      const r = await api.get<AdminAccessRequest[]>("/admin-access-requests")
      setRequests(r.data ?? [])
    } catch {
      toast({ title: t(M.loadErr), variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  // Дельта ПО за сессию — только у завершённых заявок (revoked/expired). Грузим при
  // открытии диалога заявки.
  useEffect(() => {
    setDelta(null)
    if (!reasonReq || (reasonReq.status !== "revoked" && reasonReq.status !== "expired")) return
    let cancelled = false
    setDeltaLoading(true)
    api.get<AdminSoftwareDelta>(`/admin-access-requests/${reasonReq.id}/software-delta`)
      .then((r) => { if (!cancelled) setDelta(r.data) })
      .catch(() => { })
      .finally(() => { if (!cancelled) setDeltaLoading(false) })
    return () => { cancelled = true }
  }, [reasonReq])

  async function respond(id: string, decision: "approved" | "rejected", durationSeconds?: number) {
    setSubmitting(true)
    try {
      await api.post(`/admin-access-requests/${id}/respond`, {
        decision,
        duration_seconds: durationSeconds,
      })
      setApproveOpen(null)
      await load()
    } catch {
      toast({ title: t(M.respondErr), variant: "destructive" })
    } finally {
      setSubmitting(false)
    }
  }

  async function revoke(id: string) {
    setSubmitting(true)
    try {
      await api.post(`/admin-access-requests/${id}/revoke`, {})
      await load()
    } catch {
      toast({ title: t(M.revokeErr), variant: "destructive" })
    } finally {
      setSubmitting(false)
    }
  }


  const pending = requests.filter((r) => r.status === "pending")
  const q = query.trim().toLowerCase()
  const visible = q
    ? requests.filter((r) =>
        (r.device_hostname ?? "").toLowerCase().includes(q) || r.device_id.toLowerCase().includes(q),
      )
    : requests

  if (loading) return <p className="text-muted-foreground text-sm">{t(M.loading)}</p>

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        {pending.length > 0 && <Badge variant="secondary">{pending.length}</Badge>}
      </div>

      {/* overflow-hidden: янтарная подсветка последней pending-строки иначе вылезает
          за 16px-скругление стеклянной карты. */}
      <div className="glass overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 pt-4 pb-3">
          <div>
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.cardTitle)}</h2>
            <p className="text-xs text-muted-foreground">{t(M.cardSubtitle)}</p>
          </div>
          <Input
            placeholder={t(M.searchPlaceholder)}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="max-w-[240px]"
          />
        </div>

        <Table>
          <TableHeader>
            <TableRow className={ROW}>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thDevice)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thReason)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thRequested)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thExpires)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thStatus)}</TableHead>
              <TableHead className="px-5" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {visible.length === 0 && (
              <TableRow className={ROW}>
                <TableCell colSpan={6} className="text-center text-xs text-muted-foreground py-8">
                  {requests.length === 0 ? t(M.emptyNone) : t(M.emptySearch)}
                </TableCell>
              </TableRow>
            )}
            {visible.map((req) => (
              // Ожидающие заявки подсвечены янтарным — тем же цветом, что и статус pending.
              <TableRow key={req.id} className={`${ROW} ${req.status === "pending" ? "bg-amber-500/[0.06]" : ""}`}>
                <TableCell className="px-5 py-3 text-sm font-medium text-foreground">{req.device_hostname || req.device_id.slice(0, 8)}</TableCell>
                <TableCell className="px-5 py-3 text-[13px] max-w-xs">
                  {req.reason ? (
                    <button
                      type="button"
                      onClick={() => setReasonReq(req)}
                      className="truncate block max-w-xs text-left text-soft hover:text-foreground transition-colors hover:underline underline-offset-2"
                      title={t(M.reasonFullHint)}
                    >
                      {req.reason}
                    </button>
                  ) : <span className="text-muted-foreground">—</span>}
                </TableCell>
                <TableCell className="px-5 py-3 text-xs text-muted-foreground">{formatDistanceToNow(req.requested_at)}</TableCell>
                <TableCell className="px-5 py-3 text-xs text-muted-foreground">
                  {req.expires_at ? formatDistanceToNow(req.expires_at) : req.pending_expires_at ? formatDistanceToNow(req.pending_expires_at) : "—"}
                </TableCell>
                <TableCell className="px-5 py-3">
                  <Badge variant={statusVariant[req.status] ?? "default"}>
                    {statusLabel[req.status] ? t(statusLabel[req.status]) : req.status}
                  </Badge>
                </TableCell>
                <TableCell className="px-5 py-3">
                  {req.status === "pending" && (
                    <div className="flex gap-2">
                      <Dialog open={approveOpen === req.id} onOpenChange={(o) => setApproveOpen(o ? req.id : null)}>
                        <DialogTrigger asChild>
                          {/* Одобрение — единственное «продвигающее» действие строки, поэтому
                              фирменный градиент; отказ и отзыв остаются вторичными. */}
                          <Button size="sm">
                            {t(M.approve)}
                          </Button>
                        </DialogTrigger>
                        <DialogContent>
                          <DialogHeader>
                            <DialogTitle>{t(M.approveTitle)}</DialogTitle>
                          </DialogHeader>
                          <div className="space-y-4 pt-2">
                            <p className="text-[13px] text-soft">
                              {t(M.deviceColon)} <span className="font-medium text-foreground">{req.device_hostname}</span>
                            </p>
                            <div className="space-y-1.5">
                              <Label className="text-soft">{t(M.duration)}</Label>
                              <div className="flex gap-2">
                                <Input
                                  type="number"
                                  min="1"
                                  step="1"
                                  className="flex-1"
                                  value={durationValue}
                                  onChange={(e) => setDurationValue(e.target.value)}
                                />
                                <Select
                                  className="w-36"
                                  value={durationUnit}
                                  onChange={(v) => setDurationUnit(v as DurationUnit)}
                                  options={[
                                    { value: "minutes", label: t(M.unitMinutes) },
                                    { value: "hours", label: t(M.unitHours) },
                                  ]}
                                />
                              </div>
                              {!durationValid && (
                                <p className="text-xs text-destructive">
                                  {t(M.durationHint)}
                                </p>
                              )}
                            </div>
                            <Button
                              className="w-full"
                              onClick={() => respond(req.id, "approved", durationSeconds)}
                              disabled={submitting || !durationValid}
                            >
                              {submitting ? t(M.sending) : t(M.confirm)}
                            </Button>
                          </div>
                        </DialogContent>
                      </Dialog>
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-destructive border-destructive/30 hover:bg-destructive/10 hover:text-destructive"
                        disabled={submitting}
                        onClick={() => respond(req.id, "rejected")}
                      >
                        {t(M.reject)}
                      </Button>
                    </div>
                  )}
                  {req.status === "approved" && (
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-destructive border-destructive/30 hover:bg-destructive/10 hover:text-destructive"
                      disabled={submitting}
                      onClick={() => revoke(req.id)}
                    >
                      {t(M.revoke)}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Dialog open={!!reasonReq} onOpenChange={(o) => !o && setReasonReq(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.reasonTitle)}</DialogTitle>
          </DialogHeader>
          {reasonReq && (
            <div className="space-y-4 pt-1">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.thDevice)}</p>
                  <p className="text-sm font-medium text-foreground">{reasonReq.device_hostname || reasonReq.device_id.slice(0, 8)}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.thStatus)}</p>
                  <Badge variant={statusVariant[reasonReq.status] ?? "default"}>
                    {statusLabel[reasonReq.status] ? t(statusLabel[reasonReq.status]) : reasonReq.status}
                  </Badge>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.thRequested)}</p>
                  <p className="text-[13px] text-soft">{formatDistanceToNow(reasonReq.requested_at)}</p>
                </div>
              </div>
              <div>
                <p className="text-xs text-muted-foreground mb-1.5">{t(M.thReason)}</p>
                <div className="rounded-md border border-border bg-muted px-3 py-2.5 text-[13px] leading-relaxed text-soft break-words">
                  {reasonReq.reason}
                </div>
              </div>

              {/* Аудит JIT-доступа: дельта ПО за сессию админ-прав. Только у
                  завершённых заявок; у активной — подсказка «сессия ещё идёт». */}
              {(reasonReq.status === "revoked" || reasonReq.status === "expired") ? (
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">{t(M.sessionChanges)}</p>
                  {deltaLoading ? (
                    <p className="text-[13px] text-soft">{t(M.loading)}</p>
                  ) : delta && (delta.added.length > 0 || delta.removed.length > 0) ? (
                    <div className="space-y-2">
                      {delta.added.length > 0 && (
                        <div>
                          <p className="mb-1 text-[11px] font-medium text-green-700 dark:text-green-400">{t(M.installed)} · {delta.added.length}</p>
                          <ul className="space-y-0.5 rounded-md border border-border bg-muted px-3 py-2 text-[13px] text-soft">
                            {delta.added.map((s, i) => <li key={`a-${i}`}>{s.name}{s.version ? ` (${s.version})` : ""}</li>)}
                          </ul>
                        </div>
                      )}
                      {delta.removed.length > 0 && (
                        <div>
                          <p className="mb-1 text-[11px] font-medium text-red-700 dark:text-red-400">{t(M.removed)} · {delta.removed.length}</p>
                          <ul className="space-y-0.5 rounded-md border border-border bg-muted px-3 py-2 text-[13px] text-soft">
                            {delta.removed.map((s, i) => <li key={`r-${i}`}>{s.name}{s.version ? ` (${s.version})` : ""}</li>)}
                          </ul>
                        </div>
                      )}
                    </div>
                  ) : (
                    <p className="text-[13px] text-soft">{t(M.noChanges)}</p>
                  )}
                </div>
              ) : reasonReq.status === "approved" ? (
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">{t(M.sessionChanges)}</p>
                  <p className="text-[13px] text-soft">{t(M.inProgress)}</p>
                </div>
              ) : null}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
