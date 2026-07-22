import { useEffect, useState } from "react"
import { useParams, useNavigate } from "react-router-dom"
import { ChevronLeft, Copy, Check, Terminal, ShieldCheck, Cpu, HardDrive, MemoryStick, ChevronDown, LifeBuoy, MonitorPlay, ArrowRightLeft } from "lucide-react"
import api, { Device, Software, Task, Script, HelpRequest, DeviceDetailResponse, ReenrollResponse, MigrationRosterEntry, UpdateChannel, deviceRunsScript, agentPlatform, DEVICE_STATUS, helpRequestScreenshotUrl } from "@/lib/api"
import { GroupBadge } from "@/components/GroupBadge"
import DeviceResources from "@/components/DeviceResources"
import DeviceActivity from "@/components/DeviceActivity"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Select } from "@/components/ui/select"
import { Dialog, DialogTrigger, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu"
import { Label } from "@/components/ui/label"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { formatDistanceToNow } from "@/lib/time"
import { useMe } from "@/lib/useMe"
import { useT, type Msg } from "@/lib/i18n"

type TaskForm = { script: string; platform: string; priority: string }
type TaskMode = "library" | "manual"

const statusBadge = (status: Device["status"]) => {
  const { label, variant } = DEVICE_STATUS[status] ?? { label: status, variant: "outline" as const }
  return <Badge variant={variant}>{label}</Badge>
}

// Значения-Msg: сами лейблы статусов локализуются через t() уже в JSX
// (t — хук, на верхнем уровне модуля его звать нельзя).
const taskStatusLabel: Record<string, Msg> = {
  pending:   { ru: "Ожидает",   en: "Pending"   },
  acked:     { ru: "Принята",   en: "Acked"     },
  completed: { ru: "Выполнена", en: "Completed" },
  failed:    { ru: "Ошибка",    en: "Failed"    },
}

const taskStatusVariant: Record<string, "default" | "secondary" | "success" | "destructive" | "outline"> = {
  pending:   "secondary",
  acked:     "outline",
  completed: "success",
  failed:    "destructive",
}

const helpStatusLabel: Record<string, Msg> = {
  new:    { ru: "Новое",   en: "New"    },
  closed: { ru: "Закрыто", en: "Closed" },
}

const helpStatusVariant: Record<string, "default" | "secondary" | "success" | "destructive" | "outline"> = {
  new:    "secondary",
  closed: "outline",
}

const PLATFORM_OPTIONS = [
  { value: "linux",   label: "Linux"   },
  { value: "darwin",  label: "macOS"   },
  { value: "windows", label: "Windows" },
]

const M = {
  loadFailed:          { ru: "Не удалось загрузить данные устройства", en: "Failed to load device data" },
  lockSendFailed:      { ru: "Не удалось отправить команду блокировки", en: "Failed to send lock command" },
  unlockSent:          { ru: "Команда разблокировки отправлена", en: "Unlock command sent" },
  unlockSendFailed:    { ru: "Не удалось отправить команду разблокировки", en: "Failed to send unlock command" },
  deviceBlocked:       { ru: "Устройство заблокировано", en: "Device blocked" },
  deviceUnblocked:     { ru: "Устройство разблокировано", en: "Device unblocked" },
  taskSent:            { ru: "Задача отправлена на устройство", en: "Task sent to the device" },
  deviceDeleted:       { ru: "Устройство удалено", en: "Device deleted" },
  deleteEscrowConflict:{ ru: "Нельзя удалить: есть эскроу recovery-ключей", en: "Cannot delete: recovery keys are in escrow" },
  deleteFailed:        { ru: "Не удалось удалить устройство", en: "Failed to delete device" },
  reenrollTokenFailed: { ru: "Не удалось создать токен перерегистрации", en: "Failed to create re-enrollment token" },
  loading:             { ru: "Загрузка...", en: "Loading..." },
  deviceNotFound:      { ru: "Устройство не найдено", en: "Device not found" },
  screenLocked:        { ru: "Экран заблокирован", en: "Screen locked" },
  actions:             { ru: "Действия", en: "Actions" },
  reenroll:            { ru: "Перерегистрировать", en: "Re-enroll" },
  remoteDesktop:       { ru: "Удалённый рабочий стол", en: "Remote desktop" },
  unlockScreen:        { ru: "Разблокировать экран", en: "Unlock screen" },
  lockScreen:          { ru: "Заблокировать экран", en: "Lock screen" },
  blockAccess:         { ru: "Заблокировать доступ", en: "Block access" },
  unblockAccess:       { ru: "Разблокировать доступ", en: "Unblock access" },
  deleteFromInventory: { ru: "Удалить из инвентаря", en: "Delete from inventory" },
  reenrollTitle:       { ru: "Перерегистрация устройства", en: "Device re-enrollment" },
  reenrollHint:        { ru: "Запустите на устройстве. Токен действует 24ч.", en: "Run this on the device. The token is valid for 24h." },
  done:                { ru: "Готово", en: "Done" },
  generatingToken:     { ru: "Генерация токена...", en: "Generating token..." },
  newTask:             { ru: "Новая задача", en: "New task" },
  newTaskFor:          { ru: "Новая задача — {host}", en: "New task — {host}" },
  fromLibrary:         { ru: "Из библиотеки", en: "From library" },
  writeManually:       { ru: "Написать вручную", en: "Write manually" },
  scriptForOs:         { ru: "Скрипт для {os}", en: "Script for {os}" },
  noScriptsForOs:      { ru: "Нет скриптов для этой ОС. Добавьте их в разделе «Скрипты».", en: "No scripts for this OS. Add them in the Scripts section." },
  selectScript:        { ru: "Выберите скрипт…", en: "Select a script…" },
  run:                 { ru: "Запустить", en: "Run" },
  running:             { ru: "Запуск...", en: "Running..." },
  scriptLabel:         { ru: "Скрипт", en: "Script" },
  platform:            { ru: "Платформа", en: "Platform" },
  priority:            { ru: "Приоритет", en: "Priority" },
  prioLow:             { ru: "Низкий", en: "Low" },
  prioNormal:          { ru: "Обычный", en: "Normal" },
  prioHigh:            { ru: "Высокий", en: "High" },
  sending:             { ru: "Отправка...", en: "Sending..." },
  create:              { ru: "Создать", en: "Create" },
  osLabel:             { ru: "ОС", en: "OS" },
  lastSeen:            { ru: "Последний раз", en: "Last seen" },
  registered:          { ru: "Зарегистрировано", en: "Registered" },
  diagnostics:         { ru: "Диагностика", en: "Diagnostics" },
  enrollment:          { ru: "Энроллмент", en: "Enrollment" },
  macAddress:          { ru: "MAC-адрес", en: "MAC address" },
  serialNumber:        { ru: "Серийный номер (SN)", en: "Serial number (SN)" },
  agentVersion:        { ru: "Версия агента", en: "Agent version" },
  updateChannel:       { ru: "Канал обновления", en: "Update channel" },
  channelStable:       { ru: "Стабильный", en: "Stable" },
  channelBeta:         { ru: "Бета", en: "Beta" },
  channelHint:         { ru: "Бета-устройства получают предрелизные сборки агента раньше остального парка.", en: "Beta devices receive prerelease agent builds ahead of the rest of the fleet." },
  channelChanged:      { ru: "Канал обновления: {ch}", en: "Update channel: {ch}" },
  internalIp:          { ru: "Внутренний IP", en: "Internal IP" },
  externalIp:          { ru: "Внешний IP", en: "External IP" },
  gbValue:             { ru: "{n} ГБ", en: "{n} GB" },
  diskC:               { ru: "Диск (C:)", en: "Disk (C:)" },
  tasksHeading:        { ru: "Задачи", en: "Tasks" },
  noTasks:             { ru: "Нет задач", en: "No tasks" },
  logArrow:            { ru: "лог →", en: "log →" },
  helpHeading:         { ru: "Обращения", en: "Help requests" },
  screenshotNoText:    { ru: "(скриншот без текста)", en: "(screenshot without text)" },
  screenshotArrow:     { ru: "скриншот →", en: "screenshot →" },
  softwareHeading:     { ru: "Программное обеспечение", en: "Software" },
  migrationHeading:    { ru: "Импортировано из MDM", en: "Imported from MDM" },
  migrationSource:     { ru: "Источник", en: "Source" },
  migrationBatch:      { ru: "Партия", en: "Batch" },
  migrationUser:       { ru: "Сотрудник", en: "Assigned user" },
  migrationAsset:      { ru: "Инвентарный номер", en: "Asset tag" },
  migrationGroupHint:  { ru: "Группа (из MDM)", en: "Group (from MDM)" },
  migrationNotes:      { ru: "Заметки", en: "Notes" },
  helpRequestTitle:    { ru: "Обращение за помощью", en: "Help request" },
  reporterLabel:       { ru: "Пользователь", en: "User" },
  receivedLabel:       { ru: "Получено", en: "Received" },
  screenshotAlt:       { ru: "Скриншот с устройства", en: "Screenshot from the device" },
  taskLog:             { ru: "Лог задачи", en: "Task log" },
  createdLabel:        { ru: "Создана", en: "Created" },
  output:              { ru: "Вывод", en: "Output" },
  errors:              { ru: "Ошибки", en: "Errors" },
  taskRunning:         { ru: "Задача ещё выполняется — вывод появится после завершения.", en: "The task is still running — output will appear once it finishes." },
  noOutput:            { ru: "Вывод отсутствует.", en: "No output." },
  blockAccessTitle:    { ru: "Заблокировать доступ?", en: "Block access?" },
  blockAccessDesc:     { ru: "Агент на «{host}» будет отключён от управления до разблокировки.", en: "The agent on \"{host}\" will be disconnected from management until unblocked." },
  blockConfirm:        { ru: "Заблокировать", en: "Block" },
  deleteTitle:         { ru: "Удалить устройство?", en: "Delete device?" },
  deleteDesc:          { ru: "«{host}» и вся его история (задачи, скрипты, алерты, членство в группах) будут удалены безвозвратно. Если агент ещё жив, устройство появится снова при следующем heartbeat — сначала удалите агента с машины.", en: "\"{host}\" and all of its history (tasks, scripts, alerts, group memberships) will be deleted permanently. If the agent is still alive, the device will reappear on the next heartbeat — remove the agent from the machine first." },
  deleteConfirm:       { ru: "Удалить", en: "Delete" },
  lockPasswordHint:    { ru: "Команда отправлена. Сохраните пароль — он не будет показан повторно.", en: "Command sent. Save the password — it will not be shown again." },
  close:               { ru: "Закрыть", en: "Close" },
  lockScreenHint:      { ru: "На экране устройства появится замок с паролем разблокировки. Пароль генерируется один раз.", en: "A lock with the unlock password will appear on the device screen. The password is generated once." },
  reasonLabel:         { ru: "Причина (необязательно)", en: "Reason (optional)" },
  reasonPlaceholder:   { ru: "Нарушение ИБ, утеря ноутбука...", en: "Security incident, lost laptop..." },
  rdUnattendedEnable:  { ru: "Включить unattended-доступ", en: "Enable unattended access" },
  rdUnattendedDisable: { ru: "Выключить unattended-доступ", en: "Disable unattended access" },
  rdUnattendedOn:      { ru: "Unattended-доступ: включён", en: "Unattended access: on" },
  rdUnattendedEnableTitle:  { ru: "Включить unattended-доступ?", en: "Enable unattended access?" },
  rdUnattendedEnableDesc: {
    ru: "На «{host}» удалённые сеансы будут начинаться БЕЗ запроса согласия у пользователя. Плашка «идёт сеанс» на устройстве и запись в аудит сохраняются — убирается только подтверждение. Включайте только для серверов/киосков или устройств с явного согласия.",
    en: "On \"{host}\", remote sessions will start WITHOUT asking the user for consent. The on-screen \"session in progress\" banner and the audit record are kept — only the confirmation prompt is removed. Enable only for servers/kiosks or devices with explicit consent.",
  },
  rdUnattendedEnableConfirm: { ru: "Включить", en: "Enable" },
  rdUnattendedEnabled:  { ru: "Unattended-доступ включён", en: "Unattended access enabled" },
  rdUnattendedDisabled: { ru: "Unattended-доступ выключен", en: "Unattended access disabled" },
  rdUnattendedFailed:   { ru: "Не удалось изменить unattended-доступ", en: "Failed to change unattended access" },
}

export default function DeviceDetail() {
  const t = useT()
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { isAdmin } = useMe()
  const [device, setDevice] = useState<Device | null>(null)
  const [software, setSoftware] = useState<Software[]>([])
  const [tasks, setTasks] = useState<Task[]>([])
  const [helpRequests, setHelpRequests] = useState<HelpRequest[]>([])
  const [helpReq, setHelpReq] = useState<HelpRequest | null>(null)
  const [loading, setLoading] = useState(true)
  const [blocking, setBlocking] = useState(false)
  const [savingChannel, setSavingChannel] = useState(false)
  const [taskForm, setTaskForm] = useState<TaskForm>({ script: "", platform: "linux", priority: "normal" })
  const [taskOpen, setTaskOpen] = useState(false)
  const [taskMode, setTaskMode] = useState<TaskMode>("library")
  const [submitting, setSubmitting] = useState(false)
  const [scripts, setScripts] = useState<Script[]>([])
  const [selectedScriptId, setSelectedScriptId] = useState<string>("")
  const [logTask, setLogTask] = useState<Task | null>(null)
  const [confirmBlock, setConfirmBlock] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [confirmUnattended, setConfirmUnattended] = useState(false)
  const [togglingUnattended, setTogglingUnattended] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [reenrollOpen, setReenrollOpen] = useState(false)
  const [reenrolling, setReenrolling] = useState(false)
  const [reenrollResult, setReenrollResult] = useState<ReenrollResponse | null>(null)
  const [copied, setCopied] = useState(false)
  const [lockOpen, setLockOpen] = useState(false)
  const [lockReason, setLockReason] = useState("")
  const [locking, setLocking] = useState(false)
  const [lockPassword, setLockPassword] = useState<string | null>(null)
  const [migration, setMigration] = useState<MigrationRosterEntry | null>(null)
  const [lockCopied, setLockCopied] = useState(false)

  useEffect(() => {
    async function load() {
      try {
        const [d, tk, hr] = await Promise.all([
          api.get<DeviceDetailResponse>(`/devices/${id}`),
          api.get<Task[]>(`/devices/${id}/tasks`),
          api.get<HelpRequest[]>(`/help-requests?device_id=${id}`),
        ])
        setDevice(d.data.device)
        setSoftware(d.data.software ?? [])
        setTasks(tk.data ?? [])
        setHelpRequests(hr.data ?? [])
      } catch {
        toast({ title: t(M.loadFailed), variant: "destructive" })
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [id])

  useEffect(() => {
    const interval = setInterval(async () => {
      try {
        const [d, tk, hr] = await Promise.all([
          api.get<DeviceDetailResponse>(`/devices/${id}`),
          api.get<Task[]>(`/devices/${id}/tasks`),
          api.get<HelpRequest[]>(`/help-requests?device_id=${id}`),
        ])
        setDevice(d.data.device)
        setSoftware(d.data.software ?? [])
        setTasks(tk.data ?? [])
        setHelpRequests(hr.data ?? [])
      } catch { /* фоновый поллинг */ }
    }, 10000)
    return () => clearInterval(interval)
  }, [id])

  // Строка ростера миграции, из которой приехало устройство (null = не из импорта).
  // Отдельным запросом: блок опциональный, его отказ не должен ронять карточку.
  useEffect(() => {
    api.get<MigrationRosterEntry | null>(`/devices/${id}/migration-info`)
      .then((r) => setMigration(r.data ?? null))
      .catch(() => setMigration(null))
  }, [id])

  useEffect(() => {
    api.get<Script[]>("/scripts").then((r) => setScripts(r.data ?? [])).catch(() => {})
  }, [])

  const runnableScripts = device ? scripts.filter((s) => deviceRunsScript(device.os, s.platform)) : []
  const selectedScript = runnableScripts.find((s) => s.id === selectedScriptId) ?? null
  const scriptOptions = runnableScripts.map((s) => ({ value: s.id, label: `${s.name} (${s.platform})` }))
  const priorityOptions = [
    { value: "low",    label: t(M.prioLow)    },
    { value: "normal", label: t(M.prioNormal) },
    { value: "high",   label: t(M.prioHigh)   },
  ]

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
      toast({ title: t(M.lockSendFailed), variant: "destructive" })
    } finally {
      setLocking(false)
    }
  }

  async function sendUnlock() {
    if (!device) return
    try {
      await api.post(`/devices/${id}/unlock`, {})
      setDevice({ ...device, lock_status: "unlocked" })
      toast({ title: t(M.unlockSent), variant: "success" })
    } catch {
      toast({ title: t(M.unlockSendFailed), variant: "destructive" })
    }
  }

  async function toggleBlock() {
    if (!device) return
    setBlocking(true)
    try {
      const next = device.status === "active" ? "blocked" : "active"
      await api.put(`/devices/${id}/status`, { status: next })
      setDevice({ ...device, status: next })
      toast({ title: next === "blocked" ? t(M.deviceBlocked) : t(M.deviceUnblocked), variant: "success" })
    } finally {
      setBlocking(false)
    }
  }

  const channelLabel = (ch: UpdateChannel) => (ch === "beta" ? t(M.channelBeta) : t(M.channelStable))

  async function setChannel(next: UpdateChannel) {
    if (!device || next === (device.update_channel ?? "stable")) return
    setSavingChannel(true)
    try {
      await api.put(`/devices/${id}/update-channel`, { channel: next })
      setDevice({ ...device, update_channel: next })
      toast({ title: t(M.channelChanged, { ch: channelLabel(next) }), variant: "success" })
    } finally {
      setSavingChannel(false)
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
      const res = await api.get<Task[]>(`/devices/${id}/tasks`)
      setTasks(res.data ?? [])
      toast({ title: t(M.taskSent), variant: "success" })
    } finally {
      setSubmitting(false)
    }
  }

  async function removeDevice() {
    setDeleting(true)
    try {
      await api.delete(`/devices/${id}`)
      toast({ title: t(M.deviceDeleted), variant: "success" })
      navigate("/devices")
    } catch (e) {
      const status = (e as { response?: { status?: number } }).response?.status
      toast({
        title: status === 409
          ? t(M.deleteEscrowConflict)
          : t(M.deleteFailed),
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
      toast({ title: t(M.reenrollTokenFailed), variant: "destructive" })
      setReenrollOpen(false)
    } finally {
      setReenrolling(false)
    }
  }

  // setUnattended включает/выключает opt-in unattended-доступ удалённого стола. Это
  // снимает (или возвращает) запрос согласия на устройстве; плашка «идёт сеанс» и
  // аудит сеансов не затрагиваются. Сервер требует it_admin + человека и пишет аудит.
  async function setUnattended(enabled: boolean) {
    setTogglingUnattended(true)
    try {
      await api.put(`/devices/${id}/rd-unattended`, { unattended: enabled })
      setDevice((d) => (d ? { ...d, rd_unattended: enabled } : d))
      toast({ title: t(enabled ? M.rdUnattendedEnabled : M.rdUnattendedDisabled), variant: "success" })
    } catch {
      toast({ title: t(M.rdUnattendedFailed), variant: "destructive" })
    } finally {
      setTogglingUnattended(false)
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

  if (loading) return <p className="text-muted-foreground text-sm">{t(M.loading)}</p>
  if (!device) return <p className="text-destructive text-sm">{t(M.deviceNotFound)}</p>

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => navigate("/devices")}
          className="text-muted-foreground hover:text-foreground transition-colors"
        >
          <ChevronLeft className="h-5 w-5" strokeWidth={2} />
        </button>
        <h1 className="text-xl font-semibold text-foreground">{device.hostname}</h1>
        {statusBadge(device.status)}
        {device.lock_status === "locked" && <Badge variant="destructive">{t(M.screenLocked)}</Badge>}
        {device.rd_unattended && <Badge variant="outline" className="border-amber-500/50 text-amber-600">{t(M.rdUnattendedOn)}</Badge>}
        {device.groups?.map((g) => <GroupBadge key={g.id} group={g} />)}
        <div className="ml-auto flex gap-2">
          {/* Действия: перерегистрация и блокировка — только it_admin */}
          {isAdmin && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                {t(M.actions)} <ChevronDown className="ml-1 h-3.5 w-3.5 opacity-60" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem disabled={reenrolling} onSelect={() => { setReenrollOpen(true); if (!reenrollResult) reenroll() }}>
                {t(M.reenroll)}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {/* Удалённый рабочий стол: доступен только на онлайн-устройстве; если
                  устройство офлайн — страница покажет ошибку от сервера (409). */}
              <DropdownMenuItem
                onSelect={() => navigate(`/devices/${device.id}/remote-desktop`)}
                disabled={device.status !== "active"}
              >
                <MonitorPlay className="mr-2 h-3.5 w-3.5 opacity-70" />
                {t(M.remoteDesktop)}
              </DropdownMenuItem>
              {/* Unattended-доступ (opt-in): включение — через подтверждение (снимает
                  запрос согласия); выключение — сразу (безопасное направление). */}
              <DropdownMenuItem
                onSelect={() => { if (device.rd_unattended) { setUnattended(false) } else { setConfirmUnattended(true) } }}
                disabled={togglingUnattended}
              >
                <ShieldCheck className={`mr-2 h-3.5 w-3.5 ${device.rd_unattended ? "text-amber-500" : "opacity-70"}`} />
                {device.rd_unattended ? t(M.rdUnattendedDisable) : t(M.rdUnattendedEnable)}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {device.lock_status === "locked" ? (
                <DropdownMenuItem onSelect={sendUnlock}>
                  {t(M.unlockScreen)}
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem
                  onSelect={() => { setLockPassword(null); setLockReason(""); setLockOpen(true) }}
                  disabled={device.status !== "active"}
                >
                  {t(M.lockScreen)}
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              {/* Разблокировка = PUT status=active, а сервер такой переход из очереди
                  одобрения и терминальных состояний отбивает 409-й (handler.go:629).
                  Раньше пункт для них был ВКЛЮЧЁН и звал «Разблокировать доступ» —
                  клик приводил к сырому английскому тексту ошибки в тосте.
                  Разрешаем только там, где блокировка реально применима. */}
              <DropdownMenuItem
                destructive
                disabled={blocking || (device.status !== "active" && device.status !== "blocked")}
                onSelect={() => device.status === "active" ? setConfirmBlock(true) : toggleBlock()}
              >
                {device.status === "active" ? t(M.blockAccess) : t(M.unblockAccess)}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive disabled={deleting} onSelect={() => setConfirmDelete(true)}>
                {t(M.deleteFromInventory)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          )}

          {/* Диалог перерегистрации (открывается из dropdown) */}
          <Dialog open={reenrollOpen} onOpenChange={(o) => { setReenrollOpen(o); if (!o) { setReenrollResult(null); setCopied(false) } }}>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>{t(M.reenrollTitle)}</DialogTitle>
              </DialogHeader>
              {reenrollResult ? (
                <div className="space-y-4 pt-2">
                  <p className="text-sm text-muted-foreground">{t(M.reenrollHint)}</p>
                  <div className="relative">
                    <pre className="rounded-md border border-border bg-muted px-3 py-3 text-xs font-mono break-all whitespace-pre-wrap pr-10 text-soft">
                      {reenrollCommand()}
                    </pre>
                    <button
                      type="button"
                      onClick={copyCommand}
                      className="absolute right-2 top-2 rounded p-1 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {copied ? <Check className="h-4 w-4 text-emerald-600 dark:text-emerald-500" /> : <Copy className="h-4 w-4" />}
                    </button>
                  </div>
                  <p className="text-xs text-muted-foreground font-mono">{reenrollResult.enrollment_token}</p>
                  <Button className="w-full" variant="outline" onClick={() => setReenrollOpen(false)}>
                    {t(M.done)}
                  </Button>
                </div>
              ) : (
                <p className="text-sm text-muted-foreground pt-2">{t(M.generatingToken)}</p>
              )}
            </DialogContent>
          </Dialog>

          {/* Единый диалог задачи: библиотека / вручную */}
          <Dialog open={taskOpen} onOpenChange={(o) => { setTaskOpen(o); if (!o) { setSelectedScriptId(""); setTaskForm({ script: "", platform: "linux", priority: "normal" }) } }}>
            {isAdmin && (
            <DialogTrigger asChild>
              <Button size="sm" onClick={() => openTaskDialog("library")}>{t(M.newTask)}</Button>
            </DialogTrigger>
            )}
            <DialogContent>
              <DialogHeader>
                <DialogTitle>{t(M.newTaskFor, { host: device.hostname })}</DialogTitle>
              </DialogHeader>
              <div className="space-y-4 pt-2">
                {/* Переключатель режима */}
                <div className="flex rounded-md border border-border p-0.5 gap-0.5">
                  {(["library", "manual"] as TaskMode[]).map((mode) => (
                    <button
                      type="button"
                      key={mode}
                      onClick={() => setTaskMode(mode)}
                      className={[
                        "flex-1 rounded px-3 py-1.5 text-sm font-medium transition-colors",
                        taskMode === mode
                          ? "brand-gradient text-white dark:text-[hsl(224_14%_10%)]"
                          : "text-muted-foreground hover:text-foreground",
                      ].join(" ")}
                    >
                      {mode === "library" ? t(M.fromLibrary) : t(M.writeManually)}
                    </button>
                  ))}
                </div>

                {taskMode === "library" ? (
                  <>
                    <div className="space-y-1.5">
                      <Label>{t(M.scriptForOs, { os: device.os })}</Label>
                      {runnableScripts.length === 0 ? (
                        <p className="text-sm text-muted-foreground">
                          {t(M.noScriptsForOs)}
                        </p>
                      ) : (
                        <Select
                          value={selectedScriptId}
                          onChange={setSelectedScriptId}
                          placeholder={t(M.selectScript)}
                          options={[{ value: "", label: t(M.selectScript), disabled: true }, ...scriptOptions]}
                        />
                      )}
                    </div>
                    {selectedScript && (
                      <pre className="rounded-md border border-border bg-muted px-3 py-2 text-xs font-mono whitespace-pre-wrap break-all max-h-48 overflow-auto text-soft">
                        {selectedScript.content}
                      </pre>
                    )}
                    <Button
                      className="w-full"
                      onClick={submitTask}
                      disabled={submitting || !selectedScript}
                    >
                      {submitting ? t(M.running) : t(M.run)}
                    </Button>
                  </>
                ) : (
                  <>
                    <div className="space-y-1.5">
                      <Label htmlFor="task-script">{t(M.scriptLabel)}</Label>
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
                        <Label>{t(M.platform)}</Label>
                        <Select
                          value={taskForm.platform}
                          onChange={(v) => setTaskForm({ ...taskForm, platform: v })}
                          options={PLATFORM_OPTIONS}
                        />
                      </div>
                      <div className="space-y-1.5">
                        <Label>{t(M.priority)}</Label>
                        <Select
                          value={taskForm.priority}
                          onChange={(v) => setTaskForm({ ...taskForm, priority: v })}
                          options={priorityOptions}
                        />
                      </div>
                    </div>
                    <Button
                      className="w-full"
                      onClick={submitTask}
                      disabled={submitting || !taskForm.script}
                    >
                      {submitting ? t(M.sending) : t(M.create)}
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
          { label: t(M.osLabel),      value: `${device.os} ${device.os_version}` },
          { label: "IP",              value: device.ip_address || "—"            },
          { label: t(M.lastSeen),     value: device.last_seen_at ? formatDistanceToNow(device.last_seen_at) : "—" },
          { label: t(M.registered),   value: formatDistanceToNow(device.created_at) },
        ].map(({ label, value }) => (
          <div key={label} className="glass px-5 py-[18px]">
            <p className="text-xs text-muted-foreground">{label}</p>
            <p className="text-sm font-medium text-foreground mt-1 truncate">{value}</p>
          </div>
        ))}
      </div>

      <div className="glass px-5 py-[18px]">
        <h2 className="text-[15px] font-semibold text-foreground flex items-center gap-2 mb-4">
          <ShieldCheck className="h-[17px] w-[17px] text-muted-foreground" strokeWidth={2} />
          {t(M.diagnostics)}
        </h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="space-y-3">
            <div>
              <p className="text-xs text-soft mb-0.5">Device ID (cert CN)</p>
              <p className="text-sm font-mono text-foreground">{device.cert_cn || "—"}</p>
            </div>
            <div>
              <p className="text-xs text-soft mb-0.5">{t(M.enrollment)}</p>
              <p className="text-sm text-foreground">{device.enrolled_at ? formatDistanceToNow(device.enrolled_at) : "—"}</p>
            </div>
            <div>
              <p className="text-xs text-soft mb-0.5">{t(M.macAddress)}</p>
              <p className="text-sm font-mono text-foreground">{device.mac_address || "—"}</p>
            </div>
            <div>
              <p className="text-xs text-soft mb-0.5">{t(M.serialNumber)}</p>
              <p className="text-sm font-mono text-foreground">{device.serial_number || "—"}</p>
            </div>
            <div>
              <p className="text-xs text-soft mb-0.5">{t(M.agentVersion)}</p>
              <p className="text-sm font-mono text-foreground">{device.agent_version || "—"}</p>
            </div>
            <div>
              <p className="text-xs text-soft mb-0.5">{t(M.updateChannel)}</p>
              {isAdmin ? (
                <div className="mt-1 max-w-[200px]">
                  <Select
                    value={device.update_channel ?? "stable"}
                    onChange={(v) => setChannel(v as UpdateChannel)}
                    options={[
                      { value: "stable", label: t(M.channelStable) },
                      { value: "beta", label: t(M.channelBeta) },
                    ]}
                    className={savingChannel ? "pointer-events-none opacity-60" : undefined}
                  />
                  <p className="text-xs text-muted-foreground mt-1">{t(M.channelHint)}</p>
                </div>
              ) : (
                <Badge variant={device.update_channel === "beta" ? "default" : "secondary"}>
                  {channelLabel(device.update_channel ?? "stable")}
                </Badge>
              )}
            </div>
            <div>
              <p className="text-xs text-soft mb-0.5">{t(M.internalIp)}</p>
              <p className="text-sm font-mono text-foreground">{device.ip_address || "—"}</p>
            </div>
            <div>
              <p className="text-xs text-soft mb-0.5">{t(M.externalIp)}</p>
              <p className="text-sm font-mono text-foreground">{device.public_ip || "—"}</p>
            </div>
          </div>
          <div className="space-y-3">
            {device.cpu && (
              <div className="flex items-start gap-2">
                <Cpu className="h-3.5 w-3.5 text-muted-foreground mt-0.5 shrink-0" strokeWidth={2} />
                <div>
                  <p className="text-xs text-soft">CPU</p>
                  <p className="text-sm text-foreground">{device.cpu}</p>
                </div>
              </div>
            )}
            {device.ram_mb > 0 && (
              <div className="flex items-start gap-2">
                <MemoryStick className="h-3.5 w-3.5 text-muted-foreground mt-0.5 shrink-0" strokeWidth={2} />
                <div>
                  <p className="text-xs text-soft">RAM</p>
                  <p className="text-sm text-foreground">{t(M.gbValue, { n: (device.ram_mb / 1024).toFixed(1) })}</p>
                </div>
              </div>
            )}
            {device.disk && (
              <div className="flex items-start gap-2">
                <HardDrive className="h-3.5 w-3.5 text-muted-foreground mt-0.5 shrink-0" strokeWidth={2} />
                <div>
                  <p className="text-xs text-soft">{t(M.diskC)}</p>
                  <p className="text-sm text-foreground">{device.disk}</p>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Устройство приехало из импортированного парка — показываем метаданные из старого
          MDM (сотрудник, инвентарный номер, заметки). Матч ростер↔устройство считается на
          сервере по serial/hostname; null = устройство не из импорта, блок не рендерится. */}
      {migration && (
        <div className="glass px-5 py-[18px]">
          <h2 className="text-[15px] font-semibold text-foreground flex items-center gap-2 mb-4">
            <ArrowRightLeft className="h-[17px] w-[17px] text-muted-foreground" strokeWidth={2} />
            {t(M.migrationHeading)}
          </h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {migration.source_mdm && (
              <div><p className="text-xs text-soft mb-0.5">{t(M.migrationSource)}</p><p className="text-sm text-foreground">{migration.source_mdm}</p></div>
            )}
            {migration.batch_label && (
              <div><p className="text-xs text-soft mb-0.5">{t(M.migrationBatch)}</p><p className="text-sm text-foreground">{migration.batch_label}</p></div>
            )}
            {migration.assigned_user && (
              <div><p className="text-xs text-soft mb-0.5">{t(M.migrationUser)}</p><p className="text-sm text-foreground">{migration.assigned_user}</p></div>
            )}
            {migration.asset_tag && (
              <div><p className="text-xs text-soft mb-0.5">{t(M.migrationAsset)}</p><p className="text-sm font-mono text-foreground">{migration.asset_tag}</p></div>
            )}
            {migration.group_hint && (
              <div><p className="text-xs text-soft mb-0.5">{t(M.migrationGroupHint)}</p><p className="text-sm text-foreground">{migration.group_hint}</p></div>
            )}
            {migration.notes && (
              <div className="sm:col-span-2"><p className="text-xs text-soft mb-0.5">{t(M.migrationNotes)}</p><p className="text-sm text-foreground">{migration.notes}</p></div>
            )}
          </div>
        </div>
      )}

      {id && <DeviceResources deviceId={id} />}

      {id && <DeviceActivity deviceId={id} isAdmin={isAdmin} />}

      <div className="glass">
        <div className="px-5 pt-4 pb-3">
          <h2 className="text-[15px] font-semibold text-foreground flex items-center gap-2">
            <Terminal className="h-[17px] w-[17px] text-muted-foreground" strokeWidth={2} />
            {t(M.tasksHeading)}
          </h2>
        </div>
        <div>
          {tasks.length === 0 && (
            <p className="border-t border-border px-5 py-6 text-center text-xs text-muted-foreground">
              {t(M.noTasks)}
            </p>
          )}
          {tasks.map((task) => {
            const hasLog = !!(task.output || task.error_log || task.script_content)
            return (
              <div
                key={task.id}
                className={[
                  "flex items-center justify-between gap-4 border-t border-border px-5 py-3 last:rounded-b-2xl",
                  hasLog ? "cursor-pointer glass-hover" : "",
                ].join(" ")}
                onClick={() => hasLog && setLogTask(task)}
              >
                <div className="flex items-center gap-3 min-w-0">
                  <Badge variant={taskStatusVariant[task.status]}>
                    {taskStatusLabel[task.status] ? t(taskStatusLabel[task.status]) : task.status}
                  </Badge>
                  <span className="text-[13px] text-soft truncate">{task.platform}</span>
                  <span className="text-xs text-muted-foreground">{task.priority}</span>
                </div>
                <div className="flex items-center gap-4 flex-shrink-0">
                  <span className="text-xs text-muted-foreground">{formatDistanceToNow(task.created_at)}</span>
                  {hasLog && <span className="text-xs text-brand">{t(M.logArrow)}</span>}
                </div>
              </div>
            )
          })}
        </div>
      </div>

      {helpRequests.length > 0 && (
        <div className="glass">
          <div className="px-5 pt-4 pb-3">
            <h2 className="text-[15px] font-semibold text-foreground flex items-center gap-2">
              <LifeBuoy className="h-[17px] w-[17px] text-muted-foreground" strokeWidth={2} />
              {t(M.helpHeading)}
            </h2>
          </div>
          <div>
            {helpRequests.map((hr) => (
              <div
                key={hr.id}
                className="flex items-center justify-between gap-4 border-t border-border px-5 py-3 last:rounded-b-2xl cursor-pointer glass-hover"
                onClick={() => setHelpReq(hr)}
              >
                <div className="flex items-center gap-3 min-w-0">
                  <Badge variant={helpStatusVariant[hr.status] ?? "default"}>
                    {helpStatusLabel[hr.status] ? t(helpStatusLabel[hr.status]) : hr.status}
                  </Badge>
                  <span className="text-[13px] text-soft truncate">{hr.message || t(M.screenshotNoText)}</span>
                </div>
                <div className="flex items-center gap-4 flex-shrink-0">
                  <span className="text-xs text-muted-foreground">{formatDistanceToNow(hr.received_at)}</span>
                  {hr.has_screenshot && <span className="text-xs text-brand">{t(M.screenshotArrow)}</span>}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {software.length > 0 && (
        <div className="glass">
          <div className="px-5 pt-4 pb-3">
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.softwareHeading)}</h2>
          </div>
          <div>
            {software.map((s) => (
              <div
                key={s.name}
                className="flex items-center justify-between gap-4 border-t border-border px-5 py-3 last:rounded-b-2xl"
              >
                <span className="text-sm font-medium text-foreground truncate">{s.name}</span>
                <span className="text-xs text-muted-foreground flex-shrink-0">{s.version}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Просмотр обращения за помощью (кнопки закрытия — на странице «Обращения») */}
      <Dialog open={!!helpReq} onOpenChange={(o) => !o && setHelpReq(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <LifeBuoy className="h-4 w-4 text-muted-foreground" strokeWidth={2} />
              {t(M.helpRequestTitle)}
            </DialogTitle>
          </DialogHeader>
          {helpReq && (
            <div className="space-y-4 pt-1">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.reporterLabel)}</p>
                  <p className="text-[13px] text-soft">{helpReq.reporter || "—"}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground mb-0.5">{t(M.receivedLabel)}</p>
                  <p className="text-[13px] text-soft">{formatDistanceToNow(helpReq.received_at)}</p>
                </div>
              </div>
              {helpReq.message && (
                <div className="rounded-md border border-border bg-muted px-3 py-2.5 text-[13px] leading-relaxed text-soft break-words whitespace-pre-wrap">
                  {helpReq.message}
                </div>
              )}
              {helpReq.has_screenshot && (
                <a href={helpRequestScreenshotUrl(helpReq.id)} target="_blank" rel="noreferrer">
                  <img
                    src={helpRequestScreenshotUrl(helpReq.id)}
                    alt={t(M.screenshotAlt)}
                    loading="lazy"
                    className="max-h-[360px] w-auto rounded-md border border-border"
                  />
                </a>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* Task log dialog */}
      <Dialog open={!!logTask} onOpenChange={(o) => !o && setLogTask(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Terminal className="h-4 w-4 text-muted-foreground" strokeWidth={2} />
              {t(M.taskLog)}
              {logTask && (
                <Badge variant={taskStatusVariant[logTask.status]} className="ml-1">
                  {taskStatusLabel[logTask.status] ? t(taskStatusLabel[logTask.status]) : logTask.status}
                </Badge>
              )}
            </DialogTitle>
          </DialogHeader>
          {logTask && (
            <div className="space-y-4 pt-1">
              <div className="flex items-center gap-4 text-xs text-muted-foreground">
                <span>{t(M.platform)}: <span className="text-foreground">{logTask.platform}</span></span>
                <span>{t(M.priority)}: <span className="text-foreground">{logTask.priority}</span></span>
                <span>{t(M.createdLabel)}: <span className="text-foreground">{formatDistanceToNow(logTask.created_at)}</span></span>
              </div>

              {logTask.script_content && (
                <div className="space-y-1">
                  <p className="text-xs font-medium text-muted-foreground">{t(M.scriptLabel)}</p>
                  <pre className="rounded-md border border-border bg-muted px-3 py-2.5 text-xs font-mono whitespace-pre-wrap break-all max-h-40 overflow-auto text-soft">
                    {logTask.script_content}
                  </pre>
                </div>
              )}

              {logTask.output && (
                <div className="space-y-1">
                  <p className="text-xs font-medium text-emerald-600 dark:text-emerald-400">{t(M.output)}</p>
                  <pre className="rounded-md border border-emerald-500/20 bg-emerald-500/5 px-3 py-2.5 text-xs font-mono whitespace-pre-wrap break-all max-h-64 overflow-auto text-foreground">
                    {logTask.output}
                  </pre>
                </div>
              )}

              {logTask.error_log && (
                <div className="space-y-1">
                  <p className="text-xs font-medium text-destructive">{t(M.errors)}</p>
                  <pre className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2.5 text-xs font-mono whitespace-pre-wrap break-all max-h-64 overflow-auto text-destructive">
                    {logTask.error_log}
                  </pre>
                </div>
              )}

              {!logTask.output && !logTask.error_log && (
                <p className="text-sm text-muted-foreground">
                  {logTask.status === "pending" || logTask.status === "acked"
                    ? t(M.taskRunning)
                    : t(M.noOutput)}
                </p>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmBlock}
        onOpenChange={setConfirmBlock}
        title={t(M.blockAccessTitle)}
        description={t(M.blockAccessDesc, { host: device.hostname })}
        confirmLabel={t(M.blockConfirm)}
        destructive
        onConfirm={toggleBlock}
      />

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t(M.deleteTitle)}
        description={t(M.deleteDesc, { host: device.hostname })}
        confirmLabel={t(M.deleteConfirm)}
        destructive
        onConfirm={removeDevice}
      />

      {/* Включение unattended-доступа: чувствительно (снимает запрос согласия), поэтому
          через явное подтверждение с объяснением, что плашка и аудит остаются. */}
      <ConfirmDialog
        open={confirmUnattended}
        onOpenChange={setConfirmUnattended}
        title={t(M.rdUnattendedEnableTitle)}
        description={t(M.rdUnattendedEnableDesc, { host: device.hostname })}
        confirmLabel={t(M.rdUnattendedEnableConfirm)}
        onConfirm={() => setUnattended(true)}
      />

      {/* Диалог блокировки экрана */}
      <Dialog open={lockOpen} onOpenChange={(o) => { setLockOpen(o); if (!o) { setLockPassword(null); setLockReason("") } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.lockScreen)}</DialogTitle>
          </DialogHeader>
          {lockPassword ? (
            <div className="space-y-4 pt-2">
              <p className="text-sm text-muted-foreground">{t(M.lockPasswordHint)}</p>
              <div className="relative">
                <pre className="rounded-md border border-border bg-muted px-3 py-3 text-sm font-mono pr-10 text-foreground">{lockPassword}</pre>
                <button
                  type="button"
                  onClick={async () => {
                    await navigator.clipboard.writeText(lockPassword).catch(() => {})
                    setLockCopied(true)
                    setTimeout(() => setLockCopied(false), 2000)
                  }}
                  className="absolute right-2 top-2 rounded p-1 text-muted-foreground hover:text-foreground transition-colors"
                >
                  {lockCopied ? <Check className="h-4 w-4 text-emerald-600 dark:text-emerald-500" /> : <Copy className="h-4 w-4" />}
                </button>
              </div>
              <Button className="w-full" variant="outline" onClick={() => setLockOpen(false)}>{t(M.close)}</Button>
            </div>
          ) : (
            <div className="space-y-4 pt-2">
              <p className="text-sm text-muted-foreground">
                {t(M.lockScreenHint)}
              </p>
              <div className="space-y-1.5">
                <Label htmlFor="lock-reason">{t(M.reasonLabel)}</Label>
                <input
                  id="lock-reason"
                  className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  placeholder={t(M.reasonPlaceholder)}
                  value={lockReason}
                  onChange={(e) => setLockReason(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && sendLock()}
                />
              </div>
              <Button className="w-full" onClick={sendLock} disabled={locking}>
                {locking ? t(M.sending) : t(M.lockScreen)}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
