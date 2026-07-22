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

const statusLabel: Record<string, string> = {
  new: "Новое",
  closed: "Закрыто",
}

const statusVariant: Record<string, "default" | "secondary" | "success" | "destructive" | "outline"> = {
  new: "secondary",
  closed: "outline",
}

// Строки таблицы разделяются верхней границей (как на «Заявках на права»).
const ROW = "hover:bg-transparent"

export default function HelpRequests() {
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
      toast({ title: "Не удалось загрузить обращения", variant: "destructive" })
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
      toast({ title: "Не удалось изменить статус обращения", variant: "destructive" })
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

  if (loading) return <p className="text-muted-foreground text-sm">Загрузка...</p>

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold text-foreground">Обращения</h1>
        {fresh.length > 0 && <Badge variant="secondary">{fresh.length}</Badge>}
      </div>

      <div className="glass overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 pt-4 pb-3">
          <div>
            <h2 className="text-[15px] font-semibold text-foreground">Обращения за помощью</h2>
            <p className="text-xs text-muted-foreground">Сообщения сотрудников с устройств («Сообщить о проблеме» в трее)</p>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant={onlyNew ? "default" : "outline"}
              onClick={() => setOnlyNew(!onlyNew)}
            >
              Только новые
            </Button>
            <Input
              placeholder="Поиск..."
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="max-w-[220px]"
            />
          </div>
        </div>

        <Table>
          <TableHeader>
            <TableRow className={ROW}>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">Устройство</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">Пользователь</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">Сообщение</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">Получено</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">Статус</TableHead>
              <TableHead className="px-5" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {visible.length === 0 && (
              <TableRow className={ROW}>
                <TableCell colSpan={6} className="text-center text-xs text-muted-foreground py-8">
                  {requests.length === 0 ? "Нет обращений" : "Ничего не найдено"}
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
                    title="Нажмите, чтобы открыть обращение"
                  >
                    {req.has_screenshot && <Paperclip className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />}
                    <span className="truncate">{req.message || "(скриншот без текста)"}</span>
                  </button>
                </TableCell>
                <TableCell className="px-5 py-3 text-xs text-muted-foreground">{formatDistanceToNow(req.received_at)}</TableCell>
                <TableCell className="px-5 py-3">
                  <Badge variant={statusVariant[req.status] ?? "default"}>
                    {statusLabel[req.status] ?? req.status}
                  </Badge>
                </TableCell>
                <TableCell className="px-5 py-3">
                  {isAdmin && req.status === "new" && (
                    <Button size="sm" variant="outline" disabled={submitting} onClick={() => setStatus(req.id, "closed")}>
                      Закрыть
                    </Button>
                  )}
                  {isAdmin && req.status === "closed" && (
                    <Button size="sm" variant="outline" disabled={submitting} onClick={() => setStatus(req.id, "new")}>
                      Переоткрыть
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
            <DialogTitle>Обращение за помощью</DialogTitle>
          </DialogHeader>
          {viewReq && (
            <div className="space-y-4 pt-1">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">Устройство</p>
                  <p className="text-sm font-medium text-foreground">
                    <Link to={`/devices/${viewReq.device_id}`} className="hover:underline underline-offset-2">
                      {viewReq.device_hostname || viewReq.device_id.slice(0, 8)}
                    </Link>
                  </p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">Статус</p>
                  <Badge variant={statusVariant[viewReq.status] ?? "default"}>
                    {statusLabel[viewReq.status] ?? viewReq.status}
                  </Badge>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">Пользователь</p>
                  <p className="text-[13px] text-soft">{viewReq.reporter || "—"}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">Получено</p>
                  <p className="text-[13px] text-soft">{formatDistanceToNow(viewReq.received_at)}</p>
                </div>
                {viewReq.status === "closed" && viewReq.closed_by_email && (
                  <div className="col-span-2">
                    <p className="text-xs text-muted-foreground mb-0.5">Закрыл</p>
                    <p className="text-[13px] text-soft">
                      {viewReq.closed_by_email}
                      {viewReq.closed_at ? ` · ${formatDistanceToNow(viewReq.closed_at)}` : ""}
                    </p>
                  </div>
                )}
              </div>
              {viewReq.message && (
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">Сообщение</p>
                  <div className="rounded-md border border-border bg-muted px-3 py-2.5 text-[13px] leading-relaxed text-soft break-words whitespace-pre-wrap">
                    {viewReq.message}
                  </div>
                </div>
              )}
              {viewReq.has_screenshot && (
                <div>
                  <p className="text-xs text-muted-foreground mb-1.5">Скриншот</p>
                  {/* Cookie-авторизация: <img> на same-origin URL работает без токенов.
                      Клик открывает оригинал в новой вкладке. */}
                  <a href={helpRequestScreenshotUrl(viewReq.id)} target="_blank" rel="noreferrer">
                    <img
                      src={helpRequestScreenshotUrl(viewReq.id)}
                      alt="Скриншот с устройства"
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
                      {submitting ? "Сохранение..." : "Закрыть обращение"}
                    </Button>
                  ) : (
                    <Button variant="outline" disabled={submitting} onClick={() => setStatus(viewReq.id, "new")}>
                      {submitting ? "Сохранение..." : "Переоткрыть"}
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
