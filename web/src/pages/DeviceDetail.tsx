import { useEffect, useState } from "react"
import { useParams, useNavigate } from "react-router-dom"
import { ChevronLeft, Copy, Check, Terminal, ShieldCheck, Cpu, HardDrive, MemoryStick, ChevronDown } from "lucide-react"
import api, { Device, Software, Task, Script, DeviceDetailResponse, ReenrollResponse, deviceRunsScript, agentPlatform } from "@/lib/api"
import { GroupBadge } from "@/components/GroupBadge"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Select } from "@/components/ui/select"
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { Dialog, DialogTrigger, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu"
import { Label } from "@/components/ui/label"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { formatDistanceToNow } from "@/lib/time"
import { useMe } from "@/lib/useMe"

type TaskForm = { script: string; platform: string; priority: string }
type TaskMode = "library" | "manual"

const statusBadge = (status: Device["status"]) => {
  const map: Record<Device["status"], { label: string; variant: "success" | "default" | "secondary" | "destructive" | "outline" }> = {
    active:   { label: "Активен",         variant: "success"     },
    enrolled: { label: "Зарегистрирован", variant: "default"     },
    pending:  { label: "Ожидает",         variant: "secondary"   },
    blocked:  { label: "Заблокирован",    variant: "destructive" },
  }
  const { label, variant } = map[status] ?? { label: status, variant: "outline" as const }
  return <Badge variant={variant}>{label}</Badge>
}

const taskStatusLabel: Record<string, string> = {
  pending:   "Ожидает",
  acked:     "Принята",
  completed: "Выполнена",
  failed:    "Ошибка",
}

const taskStatusVariant: Record<string, "default" | "secondary" | "success" | "destructive" | "outline"> = {
  pending:   "secondary",
  acked:     "outline",
  completed: "success",
  failed:    "destructive",
}

const PLATFORM_OPTIONS = [
  { value: "linux",   label: "Linux"   },
  { value: "darwin",  label: "macOS"   },
  { value: "windows", label: "Windows" },
]

const PRIORITY_OPTIONS = [
  { value: "low",    label: "Низкий"   },
  { value: "normal", label: "Обычный"  },
  { value: "high",   label: "Высокий"  },
]

export default function DeviceDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { isAdmin } = useMe()
  const [device, setDevice] = useState<Device | null>(null)
  const [software, setSoftware] = useState<Software[]>([])
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [blocking, setBlocking] = useState(false)
  const [taskForm, setTaskForm] = useState<TaskForm>({ script: "", platform: "linux", priority: "normal" })
  const [taskOpen, setTaskOpen] = useState(false)
  const [taskMode, setTaskMode] = useState<TaskMode>("library")
  const [submitting, setSubmitting] = useState(false)
  const [scripts, setScripts] = useState<Script[]>([])
  const [selectedScriptId, setSelectedScriptId] = useState<string>("")
  const [logTask, setLogTask] = useState<Task | null>(null)
  const [confirmBlock, setConfirmBlock] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [reenrollOpen, setReenrollOpen] = useState(false)
  const [reenrolling, setReenrolling] = useState(false)
  const [reenrollResult, setReenrollResult] = useState<ReenrollResponse | null>(null)
  const [copied, setCopied] = useState(false)
  const [lockOpen, setLockOpen] = useState(false)
  const [lockReason, setLockReason] = useState("")
  const [locking, setLocking] = useState(false)
  const [lockPassword, setLockPassword] = useState<string | null>(null)
  const [lockCopied, setLockCopied] = useState(false)

  useEffect(() => {
    async function load() {
      try {
        const [d, t] = await Promise.all([
          api.get<DeviceDetailResponse>(`/devices/${id}`),
          api.get<Task[]>(`/devices/${id}/tasks`),
        ])
        setDevice(d.data.device)
        setSoftware(d.data.software ?? [])
        setTasks(t.data ?? [])
      } catch {
        toast({ title: "Не удалось загрузить данные устройства", variant: "destructive" })
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [id])

  useEffect(() => {
    const interval = setInterval(async () => {
      try {
        const [d, t] = await Promise.all([
          api.get<DeviceDetailResponse>(`/devices/${id}`),
          api.get<Task[]>(`/devices/${id}/tasks`),
        ])
        setDevice(d.data.device)
        setSoftware(d.data.software ?? [])
        setTasks(t.data ?? [])
      } catch { /* фоновый поллинг */ }
    }, 10000)
    return () => clearInterval(interval)
  }, [id])

  useEffect(() => {
    api.get<Script[]>("/scripts").then((r) => setScripts(r.data ?? [])).catch(() => {})
  }, [])

  const runnableScripts = device ? scripts.filter((s) => deviceRunsScript(device.os, s.platform)) : []
  const selectedScript = runnableScripts.find((s) => s.id === selectedScriptId) ?? null
  const scriptOptions = runnableScripts.map((s) => ({ value: s.id, label: `${s.name} (${s.platform})` }))

  function openTaskDialog(mode: TaskMode) {
    setTaskMode(mode)
    setSelectedScriptId("")
    setTaskForm({ script: "", platform: "linux", priority: "normal" })
    setTaskOpen(true)
  }

  async function sendLock() {
    if (!device) return
    setLocking(true)
    try {
      const r = await api.post<{ task_id: string; password: string }>(`/devices/${id}/lock`, { reason: lockReason })
      setLockPassword(r.data.password)
      setDevice({ ...device, lock_status: "locked" })
    } catch {
      toast({ title: "Не удалось отправить команду блокировки", variant: "destructive" })
    } finally {
      setLocking(false)
    }
  }

  async function sendUnlock() {
    if (!device) return
    try {
      await api.post(`/devices/${id}/unlock`, {})
      setDevice({ ...device, lock_status: "unlocked" })
      toast({ title: "Команда разблокировки отправлена", variant: "success" })
    } catch {
      toast({ title: "Не удалось отправить команду разблокировки", variant: "destructive" })
    }
  }

  async function toggleBlock() {
    if (!device) return
    setBlocking(true)
    try {
      const next = device.status === "active" ? "blocked" : "active"
      await api.put(`/devices/${id}/status`, { status: next })
      setDevice({ ...device, status: next })
      toast({ title: next === "blocked" ? "Устройство заблокировано" : "Устройство разблокировано", variant: "success" })
    } finally {
      setBlocking(false)
    }
  }

  async function submitTask() {
    setSubmitting(true)
    try {
      if (taskMode === "library") {
        if (!selectedScript) return
        await api.post(`/devices/${id}/tasks`, {
          script_content: selectedScript.content,
          platform: agentPlatform(selectedScript.platform),
          priority: "normal",
        })
      } else {
        await api.post(`/devices/${id}/tasks`, {
          script_content: taskForm.script,
          platform: taskForm.platform,
          priority: taskForm.priority,
        })
      }
      setTaskOpen(false)
      setSelectedScriptId("")
      setTaskForm({ script: "", platform: "linux", priority: "normal" })
      const t = await api.get<Task[]>(`/devices/${id}/tasks`)
      setTasks(t.data ?? [])
      toast({ title: "Задача отправлена на устройство", variant: "success" })
    } finally {
      setSubmitting(false)
    }
  }

  async function removeDevice() {
    setDeleting(true)
    try {
      await api.delete(`/devices/${id}`)
      toast({ title: "Устройство удалено", variant: "success" })
      navigate("/devices")
    } catch (e) {
      const status = (e as { response?: { status?: number } }).response?.status
      toast({
        title: status === 409
          ? "Нельзя удалить: есть эскроу recovery-ключей"
          : "Не удалось удалить устройство",
        variant: "destructive",
      })
    } finally {
      setDeleting(false)
    }
  }

  async function reenroll() {
    setReenrolling(true)
    try {
      const r = await api.post<ReenrollResponse>(`/devices/${id}/reenroll`, {})
      setReenrollResult(r.data)
    } catch {
      toast({ title: "Не удалось создать токен перерегистрации", variant: "destructive" })
      setReenrollOpen(false)
    } finally {
      setReenrolling(false)
    }
  }

  function reenrollCommand() {
    if (!reenrollResult) return ""
    const enrollURL = `${window.location.origin}/api/v1/enroll`
    return `agent enroll -enroll-url ${enrollURL} -token ${reenrollResult.enrollment_token}`
  }

  async function copyCommand() {
    await navigator.clipboard.writeText(reenrollCommand())
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  if (loading) return <p className="text-muted-foreground text-sm">Загрузка...</p>
  if (!device) return <p className="text-destructive text-sm">Устройство не найдено</p>

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => navigate("/devices")}
          className="text-muted-foreground hover:text-foreground transition-colors"
        >
          <ChevronLeft className="h-5 w-5" />
        </button>
        <h1 className="text-xl font-semibold">{device.hostname}</h1>
        {statusBadge(device.status)}
        {device.lock_status === "locked" && <Badge variant="destructive">Экран заблокирован</Badge>}
        {device.groups?.map((g) => <GroupBadge key={g.id} group={g} />)}
        <div className="ml-auto flex gap-2">
          {/* Действия: перерегистрация и блокировка — только it_admin */}
          {isAdmin && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                Действия <ChevronDown className="ml-1 h-3.5 w-3.5 opacity-60" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem disabled={reenrolling} onSelect={() => { setReenrollOpen(true); if (!reenrollResult) reenroll() }}>
                Перерегистрировать
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {device.lock_status === "locked" ? (
                <DropdownMenuItem onSelect={sendUnlock}>
                  Разблокировать экран
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem
                  onSelect={() => { setLockPassword(null); setLockReason(""); setLockOpen(true) }}
                  disabled={device.status !== "active"}
                >
                  Заблокировать экран
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem
                destructive
                disabled={blocking || device.status === "pending" || device.status === "enrolled"}
                onSelect={() => device.status === "active" ? setConfirmBlock(true) : toggleBlock()}
              >
                {device.status === "active" ? "Заблокировать доступ" : "Разблокировать доступ"}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive disabled={deleting} onSelect={() => setConfirmDelete(true)}>
                Удалить из инвентаря
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          )}

          {/* Диалог перерегистрации (открывается из dropdown) */}
          <Dialog open={reenrollOpen} onOpenChange={(o) => { setReenrollOpen(o); if (!o) { setReenrollResult(null); setCopied(false) } }}>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Перерегистрация устройства</DialogTitle>
              </DialogHeader>
              {reenrollResult ? (
                <div className="space-y-4 pt-2">
                  <p className="text-sm text-muted-foreground">Запустите на устройстве. Токен действует 24ч.</p>
                  <div className="relative">
                    <pre className="rounded-md border bg-muted px-3 py-3 text-xs font-mono break-all whitespace-pre-wrap pr-10">
                      {reenrollCommand()}
                    </pre>
                    <button
                      type="button"
                      onClick={copyCommand}
                      className="absolute right-2 top-2 rounded p-1 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {copied ? <Check className="h-4 w-4 text-green-500" /> : <Copy className="h-4 w-4" />}
                    </button>
                  </div>
                  <p className="text-xs text-muted-foreground font-mono">{reenrollResult.enrollment_token}</p>
                  <Button className="w-full" variant="outline" onClick={() => setReenrollOpen(false)}>
                    Готово
                  </Button>
                </div>
              ) : (
                <p className="text-sm text-muted-foreground pt-2">Генерация токена...</p>
              )}
            </DialogContent>
          </Dialog>

          {/* Единый диалог задачи: библиотека / вручную */}
          <Dialog open={taskOpen} onOpenChange={(o) => { setTaskOpen(o); if (!o) { setSelectedScriptId(""); setTaskForm({ script: "", platform: "linux", priority: "normal" }) } }}>
            {isAdmin && (
            <DialogTrigger asChild>
              <Button size="sm" onClick={() => openTaskDialog("library")}>Новая задача</Button>
            </DialogTrigger>
            )}
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Новая задача — {device.hostname}</DialogTitle>
              </DialogHeader>
              <div className="space-y-4 pt-2">
                {/* Переключатель режима */}
                <div className="flex rounded-md border p-0.5 gap-0.5">
                  {(["library", "manual"] as TaskMode[]).map((mode) => (
                    <button
                      type="button"
                      key={mode}
                      onClick={() => setTaskMode(mode)}
                      className={[
                        "flex-1 rounded px-3 py-1.5 text-sm font-medium transition-colors",
                        taskMode === mode
                          ? "bg-primary text-primary-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      ].join(" ")}
                    >
                      {mode === "library" ? "Из библиотеки" : "Написать вручную"}
                    </button>
                  ))}
                </div>

                {taskMode === "library" ? (
                  <>
                    <div className="space-y-1.5">
                      <Label>Скрипт для {device.os}</Label>
                      {runnableScripts.length === 0 ? (
                        <p className="text-sm text-muted-foreground">
                          Нет скриптов для этой ОС. Добавьте их в разделе «Скрипты».
                        </p>
                      ) : (
                        <Select
                          value={selectedScriptId}
                          onChange={setSelectedScriptId}
                          placeholder="Выберите скрипт…"
                          options={[{ value: "", label: "Выберите скрипт…", disabled: true }, ...scriptOptions]}
                        />
                      )}
                    </div>
                    {selectedScript && (
                      <pre className="rounded-md border bg-muted px-3 py-2 text-xs font-mono whitespace-pre-wrap break-all max-h-48 overflow-auto">
                        {selectedScript.content}
                      </pre>
                    )}
                    <Button
                      className="w-full"
                      onClick={submitTask}
                      disabled={submitting || !selectedScript}
                    >
                      {submitting ? "Запуск..." : "Запустить"}
                    </Button>
                  </>
                ) : (
                  <>
                    <div className="space-y-1.5">
                      <Label htmlFor="task-script">Скрипт</Label>
                      <textarea
                        id="task-script"
                        className="flex min-h-[120px] w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring font-mono"
                        placeholder="#!/bin/bash&#10;echo hello"
                        value={taskForm.script}
                        onChange={(e) => setTaskForm({ ...taskForm, script: e.target.value })}
                      />
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                      <div className="space-y-1.5">
                        <Label>Платформа</Label>
                        <Select
                          value={taskForm.platform}
                          onChange={(v) => setTaskForm({ ...taskForm, platform: v })}
                          options={PLATFORM_OPTIONS}
                        />
                      </div>
                      <div className="space-y-1.5">
                        <Label>Приоритет</Label>
                        <Select
                          value={taskForm.priority}
                          onChange={(v) => setTaskForm({ ...taskForm, priority: v })}
                          options={PRIORITY_OPTIONS}
                        />
                      </div>
                    </div>
                    <Button
                      className="w-full"
                      onClick={submitTask}
                      disabled={submitting || !taskForm.script}
                    >
                      {submitting ? "Отправка..." : "Создать"}
                    </Button>
                  </>
                )}
              </div>
            </DialogContent>
          </Dialog>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {[
          { label: "ОС",              value: `${device.os} ${device.os_version}`, color: "text-blue-500"   },
          { label: "IP",              value: device.ip_address || "—",             color: "text-violet-500" },
          { label: "Последний раз",   value: device.last_seen_at ? formatDistanceToNow(device.last_seen_at) : "—", color: "text-emerald-500" },
          { label: "Зарегистрировано",value: formatDistanceToNow(device.created_at), color: "text-amber-500" },
        ].map(({ label, value, color }) => (
          <Card key={label} className="overflow-hidden">
            <div className={`h-0.5 w-full ${color.replace("text-", "bg-")}`} />
            <CardContent className="pt-4">
              <p className="text-xs text-muted-foreground">{label}</p>
              <p className={`text-sm font-medium mt-0.5 ${color}`}>{value}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base flex items-center gap-2">
            <ShieldCheck className="h-4 w-4 text-emerald-500" />
            Диагностика
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="space-y-3">
              <div>
                <p className="text-xs text-muted-foreground mb-0.5">Device ID (cert CN)</p>
                <p className="text-sm font-mono">{device.cert_cn || "—"}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground mb-0.5">Энроллмент</p>
                <p className="text-sm">{device.enrolled_at ? formatDistanceToNow(device.enrolled_at) : "—"}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground mb-0.5">MAC-адрес</p>
                <p className="text-sm font-mono">{device.mac_address || "—"}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground mb-0.5">Серийный номер (SN)</p>
                <p className="text-sm font-mono">{device.serial_number || "—"}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground mb-0.5">Версия агента</p>
                <p className="text-sm font-mono">{device.agent_version || "—"}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground mb-0.5">Внутренний IP</p>
                <p className="text-sm font-mono">{device.ip_address || "—"}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground mb-0.5">Внешний IP</p>
                <p className="text-sm font-mono">{device.public_ip || "—"}</p>
              </div>
            </div>
            <div className="space-y-3">
              {device.cpu && (
                <div className="flex items-start gap-2">
                  <Cpu className="h-3.5 w-3.5 text-muted-foreground mt-0.5 shrink-0" />
                  <div>
                    <p className="text-xs text-muted-foreground">CPU</p>
                    <p className="text-sm">{device.cpu}</p>
                  </div>
                </div>
              )}
              {device.ram_mb > 0 && (
                <div className="flex items-start gap-2">
                  <MemoryStick className="h-3.5 w-3.5 text-muted-foreground mt-0.5 shrink-0" />
                  <div>
                    <p className="text-xs text-muted-foreground">RAM</p>
                    <p className="text-sm">{(device.ram_mb / 1024).toFixed(1)} ГБ</p>
                  </div>
                </div>
              )}
              {device.disk && (
                <div className="flex items-start gap-2">
                  <HardDrive className="h-3.5 w-3.5 text-muted-foreground mt-0.5 shrink-0" />
                  <div>
                    <p className="text-xs text-muted-foreground">Диск (C:)</p>
                    <p className="text-sm">{device.disk}</p>
                  </div>
                </div>
              )}
            </div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base flex items-center gap-2">
            <Terminal className="h-4 w-4 text-violet-500" />
            Задачи
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Статус</TableHead>
                <TableHead>Платформа</TableHead>
                <TableHead>Приоритет</TableHead>
                <TableHead>Создана</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {tasks.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-muted-foreground py-6">
                    Нет задач
                  </TableCell>
                </TableRow>
              )}
              {tasks.map((t) => {
                const hasLog = !!(t.output || t.error_log || t.script_content)
                return (
                  <TableRow
                    key={t.id}
                    className={hasLog ? "cursor-pointer hover:bg-accent/40" : undefined}
                    onClick={() => hasLog && setLogTask(t)}
                  >
                    <TableCell>
                      <Badge variant={taskStatusVariant[t.status]}>
                        {taskStatusLabel[t.status] ?? t.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{t.platform}</TableCell>
                    <TableCell className="text-muted-foreground">{t.priority}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">{formatDistanceToNow(t.created_at)}</TableCell>
                    <TableCell className="text-right">
                      {hasLog && (
                        <span className="text-xs text-muted-foreground">лог →</span>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {software.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Программное обеспечение</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Название</TableHead>
                  <TableHead>Версия</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {software.map((s) => (
                  <TableRow key={s.name}>
                    <TableCell className="font-medium">{s.name}</TableCell>
                    <TableCell className="text-muted-foreground">{s.version}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {/* Task log dialog */}
      <Dialog open={!!logTask} onOpenChange={(o) => !o && setLogTask(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Terminal className="h-4 w-4 text-violet-500" />
              Лог задачи
              {logTask && (
                <Badge variant={taskStatusVariant[logTask.status]} className="ml-1">
                  {taskStatusLabel[logTask.status] ?? logTask.status}
                </Badge>
              )}
            </DialogTitle>
          </DialogHeader>
          {logTask && (
            <div className="space-y-4 pt-1">
              <div className="flex items-center gap-4 text-xs text-muted-foreground">
                <span>Платформа: <span className="text-foreground">{logTask.platform}</span></span>
                <span>Приоритет: <span className="text-foreground">{logTask.priority}</span></span>
                <span>Создана: <span className="text-foreground">{formatDistanceToNow(logTask.created_at)}</span></span>
              </div>

              {logTask.script_content && (
                <div className="space-y-1">
                  <p className="text-xs font-medium text-muted-foreground">Скрипт</p>
                  <pre className="rounded-md border bg-muted px-3 py-2.5 text-xs font-mono whitespace-pre-wrap break-all max-h-40 overflow-auto">
                    {logTask.script_content}
                  </pre>
                </div>
              )}

              {logTask.output && (
                <div className="space-y-1">
                  <p className="text-xs font-medium text-emerald-600 dark:text-emerald-400">Вывод</p>
                  <pre className="rounded-md border border-emerald-500/20 bg-emerald-500/5 px-3 py-2.5 text-xs font-mono whitespace-pre-wrap break-all max-h-64 overflow-auto text-foreground">
                    {logTask.output}
                  </pre>
                </div>
              )}

              {logTask.error_log && (
                <div className="space-y-1">
                  <p className="text-xs font-medium text-destructive">Ошибки</p>
                  <pre className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2.5 text-xs font-mono whitespace-pre-wrap break-all max-h-64 overflow-auto text-destructive">
                    {logTask.error_log}
                  </pre>
                </div>
              )}

              {!logTask.output && !logTask.error_log && (
                <p className="text-sm text-muted-foreground">
                  {logTask.status === "pending" || logTask.status === "acked"
                    ? "Задача ещё выполняется — вывод появится после завершения."
                    : "Вывод отсутствует."}
                </p>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmBlock}
        onOpenChange={setConfirmBlock}
        title="Заблокировать доступ?"
        description={`Агент на «${device.hostname}» будет отключён от управления до разблокировки.`}
        confirmLabel="Заблокировать"
        destructive
        onConfirm={toggleBlock}
      />

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Удалить устройство?"
        description={`«${device.hostname}» и вся его история (задачи, скрипты, алерты, членство в группах) будут удалены безвозвратно. Если агент ещё жив, устройство появится снова при следующем heartbeat — сначала удалите агента с машины.`}
        confirmLabel="Удалить"
        destructive
        onConfirm={removeDevice}
      />

      {/* Диалог блокировки экрана */}
      <Dialog open={lockOpen} onOpenChange={(o) => { setLockOpen(o); if (!o) { setLockPassword(null); setLockReason("") } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Заблокировать экран</DialogTitle>
          </DialogHeader>
          {lockPassword ? (
            <div className="space-y-4 pt-2">
              <p className="text-sm text-muted-foreground">Команда отправлена. Сохраните пароль — он не будет показан повторно.</p>
              <div className="relative">
                <pre className="rounded-md border bg-muted px-3 py-3 text-sm font-mono pr-10">{lockPassword}</pre>
                <button
                  type="button"
                  onClick={async () => {
                    await navigator.clipboard.writeText(lockPassword).catch(() => {})
                    setLockCopied(true)
                    setTimeout(() => setLockCopied(false), 2000)
                  }}
                  className="absolute right-2 top-2 rounded p-1 text-muted-foreground hover:text-foreground transition-colors"
                >
                  {lockCopied ? <Check className="h-4 w-4 text-green-500" /> : <Copy className="h-4 w-4" />}
                </button>
              </div>
              <Button className="w-full" variant="outline" onClick={() => setLockOpen(false)}>Закрыть</Button>
            </div>
          ) : (
            <div className="space-y-4 pt-2">
              <p className="text-sm text-muted-foreground">
                На экране устройства появится замок с паролем разблокировки. Пароль генерируется один раз.
              </p>
              <div className="space-y-1.5">
                <Label htmlFor="lock-reason">Причина (необязательно)</Label>
                <input
                  id="lock-reason"
                  className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  placeholder="Нарушение ИБ, утеря ноутбука..."
                  value={lockReason}
                  onChange={(e) => setLockReason(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && sendLock()}
                />
              </div>
              <Button className="w-full" onClick={sendLock} disabled={locking}>
                {locking ? "Отправка..." : "Заблокировать экран"}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
