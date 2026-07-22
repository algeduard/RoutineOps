import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { Paperclip } from "lucide-react"
import api, { HelpRequest, helpRequestScreenshotUrl } from "@/lib/api"
import { useMe } from "@/lib/useMe"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { formatDistanceToNow } from "@/lib/time"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

const statusLabel: Record<string, Msg> = {
  new: { ru: "Новое", en: "New" },
  closed: { ru: "Закрыто", en: "Closed" },
}

const M = {
  loadErr: { ru: "Не удалось загрузить обращения", en: "Failed to load help requests" },
  statusErr: { ru: "Не удалось изменить статус обращения", en: "Failed to change request status" },
  loading: { ru: "Загрузка...", en: "Loading..." },
  title: { ru: "Обращения", en: "Help requests" },
  cardTitle: { ru: "Обращения за помощью", en: "Help requests" },
  cardSubtitle: {
    ru: "Сообщения сотрудников с устройств («Сообщить о проблеме» в трее)",
    en: "Messages from employees on their devices (\"Report a problem\" in the tray)",
  },
  onlyNew: { ru: "Только новые", en: "New only" },
  searchPlaceholder: { ru: "Поиск...", en: "Search..." },
  thDevice: { ru: "Устройство", en: "Device" },
  thUser: { ru: "Пользователь", en: "User" },
  thMessage: { ru: "Сообщение", en: "Message" },
  thReceived: { ru: "Получено", en: "Received" },
  thStatus: { ru: "Статус", en: "Status" },
  emptyNone: { ru: "Нет обращений", en: "No help requests" },
  emptySearch: { ru: "Ничего не найдено", en: "Nothing found" },
  openHint: { ru: "Нажмите, чтобы открыть обращение", en: "Click to open the request" },
  screenshotNoText: { ru: "(скриншот без текста)", en: "(screenshot without text)" },
  close: { ru: "Закрыть", en: "Close" },
  reopen: { ru: "Переоткрыть", en: "Reopen" },
  dialogTitle: { ru: "Обращение за помощью", en: "Help request" },
  labelClosedBy: { ru: "Закрыл", en: "Closed by" },
  screenshot: { ru: "Скриншот", en: "Screenshot" },
  screenshotAlt: { ru: "Скриншот с устройства", en: "Screenshot from device" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  closeRequest: { ru: "Закрыть обращение", en: "Close request" },
}

const statusVariant: Record<string, "default" | "secondary" | "success" | "destructive" | "outline"> = {
  new: "secondary",
  closed: "outline",
}

// Строки таблицы разделяются верхней границей (как на «Заявках на права»).
const ROW = "hover:bg-transparent"

export default function HelpRequests() {
  const t = useT()
  const { isAdmin } = useMe()
  const [requests, setRequests] = useState<HelpRequest[]>([])
  const [onlyNew, setOnlyNew] = useState(true)
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [viewReq, setViewReq] = useState<HelpRequest | null>(null)

  async function load() {
    try {
      const r = await api.get<HelpRequest[]>("/help-requests")
      setRequests(r.data ?? [])
    } catch {
      toast({ title: t(M.loadErr), variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  async function setStatus(id: string, status: "new" | "closed") {
    setSubmitting(true)
    try {
      await api.post(`/help-requests/${id}/status`, { status })
      setViewReq(null)
      await load()
    } catch {
      toast({ title: t(M.statusErr), variant: "destructive" })
    } finally {
      setSubmitting(false)
    }
  }

  const fresh = requests.filter((r) => r.status === "new")
  const base = onlyNew ? fresh : requests
  const q = query.trim().toLowerCase()
  const visible = q
    ? base.filter((r) =>
        (r.device_hostname ?? "").toLowerCase().includes(q) ||
        (r.reporter ?? "").toLowerCase().includes(q) ||
        r.message.toLowerCase().includes(q),
      )
    : base

  if (loading) return <p className="text-muted-foreground text-sm">{t(M.loading)}</p>

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        {fresh.length > 0 && <Badge variant="secondary">{fresh.length}</Badge>}
      </div>

      <div className="glass overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 pt-4 pb-3">
          <div>
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.cardTitle)}</h2>
            <p className="text-xs text-muted-foreground">{t(M.cardSubtitle)}</p>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant={onlyNew ? "default" : "outline"}
              onClick={() => setOnlyNew(!onlyNew)}
            >
              {t(M.onlyNew)}
            </Button>
            <Input
              placeholder={t(M.searchPlaceholder)}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="max-w-[220px]"
            />
          </div>
        </div>

        <Table>
          <TableHeader>
            <TableRow className={ROW}>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thDevice)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thUser)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thMessage)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.thReceived)}</TableHead>
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
              // Новые обращения подсвечены янтарным — как pending на «Заявках на права».
              <TableRow key={req.id} className={`${ROW} ${req.status === "new" ? "bg-amber-500/[0.06]" : ""}`}>
                <TableCell className="px-5 py-3 text-sm font-medium text-foreground">
                  <Link to={`/devices/${req.device_id}`} className="hover:underline underline-offset-2">
                    {req.device_hostname || req.device_id.slice(0, 8)}
                  </Link>
                </TableCell>
                <TableCell className="px-5 py-3 text-[13px] text-soft">{req.reporter || "—"}</TableCell>
                <TableCell className="px-5 py-3 text-[13px] max-w-sm">
                  <button
                    type="button"
                    onClick={() => setViewReq(req)}
                    className="flex items-center gap-1.5 max-w-sm text-left text-soft hover:text-foreground transition-colors hover:underline underline-offset-2"
                    title={t(M.openHint)}
                  >
                    {req.has_screenshot && <Paperclip className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />}
                    <span className="truncate">{req.message || t(M.screenshotNoText)}</span>
                  </button>
                </TableCell>
                <TableCell className="px-5 py-3 text-xs text-muted-foreground">{formatDistanceToNow(req.received_at)}</TableCell>
                <TableCell className="px-5 py-3">
                  <Badge variant={statusVariant[req.status] ?? "default"}>
                    {statusLabel[req.status] ? t(statusLabel[req.status]) : req.status}
                  </Badge>
                </TableCell>
                <TableCell className="px-5 py-3">
                  {isAdmin && req.status === "new" && (
                    <Button size="sm" variant="outline" disabled={submitting} onClick={() => setStatus(req.id, "closed")}>
                      {t(M.close)}
                    </Button>
                  )}
                  {isAdmin && req.status === "closed" && (
                    <Button size="sm" variant="outline" disabled={submitting} onClick={() => setStatus(req.id, "new")}>
                      {t(M.reopen)}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Dialog open={!!viewReq} onOpenChange={(o) => !o && setViewReq(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t(M.dialogTitle)}</DialogTitle>
          </DialogHeader>
          {viewReq && (
            <div className="space-y-4 pt-1">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.thDevice)}</p>
                  <p className="text-sm font-medium text-foreground">
                    <Link to={`/devices/${viewReq.device_id}`} className="hover:underline underline-offset-2">
                      {viewReq.device_hostname || viewReq.device_id.slice(0, 8)}
                    </Link>
                  </p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.thStatus)}</p>
                  <Badge variant={statusVariant[viewReq.status] ?? "default"}>
                    {statusLabel[viewReq.status] ? t(statusLabel[viewReq.status]) : viewReq.status}
                  </Badge>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.thUser)}</p>
                  <p className="text-[13px] text-soft">{viewReq.reporter || "—"}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.thReceived)}</p>
                  <p className="text-[13px] text-soft">{formatDistanceToNow(viewReq.received_at)}</p>
                </div>
                {viewReq.status === "closed" && viewReq.closed_by_email && (
                  <div className="col-span-2">
                    <p className="text-xs text-muted-foreground mb-0.5">{t(M.labelClosedBy)}</p>
                    <p className="text-[13px] text-soft">
                      {viewReq.closed_by_email}
                      {viewReq.closed_at ? ` · ${formatDistanceToNow(viewReq.closed_at)}` : ""}
                    </p>
                  </div>
                )}
              </div>
              {viewReq.message && (
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">{t(M.thMessage)}</p>
                  <div className="rounded-md border border-border bg-muted px-3 py-2.5 text-[13px] leading-relaxed text-soft break-words whitespace-pre-wrap">
                    {viewReq.message}
                  </div>
                </div>
              )}
              {viewReq.has_screenshot && (
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">{t(M.screenshot)}</p>
                  {/* Cookie-авторизация: <img> на same-origin URL работает без токенов.
                      Клик открывает оригинал в новой вкладке. */}
                  <a href={helpRequestScreenshotUrl(viewReq.id)} target="_blank" rel="noreferrer">
                    <img
                      src={helpRequestScreenshotUrl(viewReq.id)}
                      alt={t(M.screenshotAlt)}
                      loading="lazy"
                      className="max-h-[360px] w-auto rounded-md border border-border"
                    />
                  </a>
                </div>
              )}
              {isAdmin && (
                <div className="flex justify-end gap-2">
                  {viewReq.status === "new" ? (
                    <Button disabled={submitting} onClick={() => setStatus(viewReq.id, "closed")}>
                      {submitting ? t(M.saving) : t(M.closeRequest)}
                    </Button>
                  ) : (
                    <Button variant="outline" disabled={submitting} onClick={() => setStatus(viewReq.id, "new")}>
                      {submitting ? t(M.saving) : t(M.reopen)}
                    </Button>
                  )}
                </div>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
