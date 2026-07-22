import { useEffect, useState } from "react"
import { Plus, Trash2, Users, Play, ShieldCheck } from "lucide-react"
import api, { Script, ScriptPolicy, DeviceGroup, Device, GROUP_PALETTE, DEFAULT_GROUP_COLOR } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Select } from "@/components/ui/select"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

const M = {
  colorAria: { ru: "Цвет {c}", en: "Color {c}" },
  loadErr: { ru: "Не удалось загрузить данные", en: "Failed to load data" },
  groupCreated: { ru: "Группа создана", en: "Group created" },
  taskCreated: { ru: "Задача создана на {n} устройств", en: "Task created on {n} devices" },
  loading: { ru: "Загрузка...", en: "Loading..." },
  title: { ru: "Группы устройств", en: "Device groups" },
  newGroup: { ru: "Новая группа", en: "New group" },
  emptyState: {
    ru: "Создайте группу, чтобы назначать политики и прогонять скрипты на устройства пачкой.",
    en: "Create a group to assign policies and run scripts on devices in bulk.",
  },
  deleteGroup: { ru: "Удалить группу", en: "Delete group" },
  groupStats: {
    ru: "{devices} устройств · {policies} скрипт-политик · {rules} софт-правил",
    en: "{devices} devices · {policies} script policies · {rules} software rules",
  },
  manage: { ru: "Управление", en: "Manage" },
  runScript: { ru: "Прогнать скрипт", en: "Run script" },
  newGroupTitle: { ru: "Новая группа устройств", en: "New device group" },
  groupNameLabel: { ru: "Название группы", en: "Group name" },
  groupNamePh: { ru: "MacBook-и бухгалтерии", en: "Accounting MacBooks" },
  color: { ru: "Цвет", en: "Color" },
  colorHint: {
    ru: "Этим цветом будут обведены устройства группы в списке.",
    en: "Devices in this group will be outlined with this color in the list.",
  },
  creating: { ru: "Создание...", en: "Creating..." },
  create: { ru: "Создать", en: "Create" },
  manageGroupTitle: { ru: "Управление группой: {name}", en: "Manage group: {name}" },
  devices: { ru: "Устройства", en: "Devices" },
  deviceFilterPh: { ru: "Фильтр: имя, IP, серийник...", en: "Filter: name, IP, serial..." },
  remove: { ru: "Убрать", en: "Remove" },
  add: { ru: "Добавить", en: "Add" },
  noDevices: { ru: "Нет устройств", en: "No devices" },
  nothingFound: { ru: "Ничего не найдено", en: "Nothing found" },
  scriptPolicies: { ru: "Политики скриптов", en: "Script policies" },
  unassign: { ru: "Снять", en: "Unassign" },
  assign: { ru: "Назначить", en: "Assign" },
  noScriptPolicies: { ru: "Нет политик скриптов", en: "No script policies" },
  softwareRules: { ru: "Софт-правила", en: "Software rules" },
  ruleForbidden: { ru: "Запрещено", en: "Forbidden" },
  ruleAllowed: { ru: "Разрешено", en: "Allowed" },
  removeRule: { ru: "Снять", en: "Remove" },
  noSoftwareRules: { ru: "Нет софт-правил", en: "No software rules" },
  softwareLabel: { ru: "ПО", en: "Software" },
  typeLabel: { ru: "Тип", en: "Type" },
  runScriptOnGroup: { ru: "Прогнать скрипт на группу", en: "Run script on group" },
  scriptLabel: { ru: "Скрипт", en: "Script" },
  selectScriptPh: { ru: "Выберите скрипт...", en: "Select a script..." },
  priorityLabel: { ru: "Приоритет", en: "Priority" },
  priorityLow: { ru: "Низкий", en: "Low" },
  priorityMedium: { ru: "Средний", en: "Medium" },
  priorityHigh: { ru: "Высокий", en: "High" },
  runHint: {
    ru: "Несовместимые по платформе устройства сервер пропустит автоматически.",
    en: "The server will automatically skip devices incompatible by platform.",
  },
  running: { ru: "Запуск...", en: "Running..." },
  run: { ru: "Запустить", en: "Run" },
}

// ColorPalette — выбор цвета группы. Цветом обводятся рамки её устройств в списке,
// поэтому он часть создания группы, а не косметическая настройка «потом».
function ColorPalette({ value, onChange }: { value: string; onChange: (c: string) => void }) {
  const t = useT()
  return (
    <div className="flex flex-wrap gap-2">
      {GROUP_PALETTE.map((c) => (
        <button
          type="button"
          key={c}
          aria-label={t(M.colorAria, { c })}
          aria-pressed={value === c}
          onClick={() => onChange(c)}
          className={
            "h-7 w-7 rounded-full transition-transform hover:scale-110 " +
            (value === c ? "ring-2 ring-offset-2 ring-offset-background ring-foreground" : "")
          }
          style={{ backgroundColor: c }}
        />
      ))}
    </div>
  )
}

export default function Groups() {
  const t = useT()
  const [groups, setGroups] = useState<DeviceGroup[]>([])
  const [devices, setDevices] = useState<Device[]>([])
  const [scriptPolicies, setScriptPolicies] = useState<ScriptPolicy[]>([])
  const [scripts, setScripts] = useState<Script[]>([])
  const [loading, setLoading] = useState(true)

  const [createOpen, setCreateOpen] = useState(false)
  const [groupName, setGroupName] = useState("")
  const [groupColor, setGroupColor] = useState<string>(DEFAULT_GROUP_COLOR)

  const [manageGroupId, setManageGroupId] = useState<string | null>(null)
  // Список устройств в диалоге рендерится целиком; на парке в сотни машин без фильтра
  // нужную не найти.
  const [deviceQuery, setDeviceQuery] = useState("")
  const [softwareForm, setSoftwareForm] = useState<{ software_name: string; rule_type: "allowed" | "forbidden" }>({
    software_name: "",
    rule_type: "forbidden",
  })

  const [runGroupId, setRunGroupId] = useState<string | null>(null)
  const [runForm, setRunForm] = useState<{ script_id: string; priority: "low" | "medium" | "high" }>({
    script_id: "",
    priority: "medium",
  })

  const [submitting, setSubmitting] = useState(false)

  async function load() {
    try {
      const [g, d, sp, s] = await Promise.all([
        api.get<DeviceGroup[]>("/device-groups"),
        api.get<Device[]>("/devices"),
        api.get<ScriptPolicy[]>("/script-policies"),
        api.get<Script[]>("/scripts"),
      ])
      setGroups(g.data ?? [])
      setDevices(d.data ?? [])
      setScriptPolicies(sp.data ?? [])
      setScripts(s.data ?? [])
    } catch {
      toast({ title: t(M.loadErr), variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  async function handleCreateGroup() {
    setSubmitting(true)
    try {
      await api.post("/device-groups", { name: groupName, color: groupColor })
      setCreateOpen(false)
      setGroupName("")
      setGroupColor(DEFAULT_GROUP_COLOR)
      await load()
      toast({ title: t(M.groupCreated), variant: "success" })
    } catch {
      // авто-тост интерсептора
    } finally {
      setSubmitting(false)
    }
  }

  // Цвет меняется прямо в карточке управления: перекрашивать группу не должно стоить
  // пересоздания. Оптимистично не обновляем — цвет уезжает в рамки устройств, пусть
  // источником правды останется сервер.
  async function handleChangeColor(groupId: string, color: string) {
    try {
      await api.patch(`/device-groups/${groupId}`, { color })
      await load()
    } catch {
      // авто-тост интерсептора
    }
  }

  async function handleDeleteGroup(id: string) {
    try {
      await api.delete(`/device-groups/${id}`)
      setGroups((prev) => prev.filter((g) => g.id !== id))
    } catch {
      // авто-тост интерсептора
    }
  }

  async function handleAddDevice(groupId: string, deviceId: string) {
    try {
      await api.post(`/device-groups/${groupId}/members`, { device_id: deviceId })
      await load()
    } catch {
      // авто-тост интерсептора
    }
  }

  async function handleRemoveDevice(groupId: string, deviceId: string) {
    try {
      await api.delete(`/device-groups/${groupId}/members/${deviceId}`)
      await load()
    } catch {
      // авто-тост интерсептора
    }
  }

  async function handleAssignPolicy(groupId: string, policyId: string) {
    try {
      await api.post(`/device-groups/${groupId}/policies`, { policy_id: policyId })
      await load()
    } catch {
      // авто-тост интерсептора
    }
  }

  async function handleUnassignPolicy(groupId: string, policyId: string) {
    try {
      await api.delete(`/device-groups/${groupId}/policies/${policyId}`)
      await load()
    } catch {
      // авто-тост интерсептора
    }
  }

  async function handleAddSoftwareRule(groupId: string) {
    if (!softwareForm.software_name.trim()) return
    setSubmitting(true)
    try {
      await api.post(`/device-groups/${groupId}/software-policies`, {
        software_name: softwareForm.software_name.trim(),
        rule_type: softwareForm.rule_type,
      })
      setSoftwareForm({ software_name: "", rule_type: softwareForm.rule_type })
      await load()
    } catch {
      // авто-тост интерсептора
    } finally {
      setSubmitting(false)
    }
  }

  async function handleRemoveSoftwareRule(groupId: string, ruleId: string) {
    try {
      await api.delete(`/device-groups/${groupId}/software-policies/${ruleId}`)
      await load()
    } catch {
      // авто-тост интерсептора
    }
  }

  async function handleRunScript() {
    if (!runGroupId || !runForm.script_id) return
    setSubmitting(true)
    try {
      const res = await api.post<{ created: number }>(`/device-groups/${runGroupId}/run-script`, {
        script_id: runForm.script_id,
        priority: runForm.priority,
      })
      setRunGroupId(null)
      setRunForm({ script_id: "", priority: "medium" })
      toast({ title: t(M.taskCreated, { n: res.data.created }), variant: "success" })
    } catch {
      // авто-тост интерсептора
    } finally {
      setSubmitting(false)
    }
  }

  const managedGroup = groups.find((g) => g.id === manageGroupId) ?? null

  const dq = deviceQuery.trim().toLowerCase()
  const visibleDevices = dq
    ? devices.filter((d) =>
        [d.hostname, d.ip_address, d.serial_number, d.mac_address, d.os]
          .some((v) => (v ?? "").toLowerCase().includes(dq)),
      )
    : devices

  if (loading) {
    return <div className="flex items-center justify-center h-48 text-muted-foreground text-sm">{t(M.loading)}</div>
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="h-4 w-4 mr-1.5" strokeWidth={2} />
          {t(M.newGroup)}
        </Button>
      </div>

      {groups.length === 0 && (
        <p className="text-sm text-muted-foreground">
          {t(M.emptyState)}
        </p>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {groups.map((g) => (
          <div key={g.id} className="glass overflow-hidden">
            {/* Полоса цвета группы — тот же цвет обводит её устройства в списке. */}
            <div className="h-1 w-full" style={{ backgroundColor: g.color }} />
            <div className="flex flex-col gap-3 px-5 pt-4 pb-[18px]">
              <div className="flex items-start justify-between gap-3">
                <h2 className="text-[15px] font-semibold text-foreground flex items-center gap-2 min-w-0">
                  <Users className="h-4 w-4 flex-shrink-0" strokeWidth={2} style={{ color: g.color }} />
                  <span className="truncate">{g.name}</span>
                </h2>
                <button
                  type="button"
                  onClick={() => handleDeleteGroup(g.id)}
                  className="text-muted-foreground hover:text-destructive transition-colors flex-shrink-0"
                  aria-label={t(M.deleteGroup)}
                >
                  <Trash2 className="h-3.5 w-3.5" strokeWidth={2} />
                </button>
              </div>
              <p className="text-xs text-muted-foreground">
                {t(M.groupStats, { devices: g.device_ids.length, policies: g.policy_ids.length, rules: g.software_rules.length })}
              </p>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7 text-xs px-2"
                  onClick={() => setManageGroupId(g.id)}
                >
                  {t(M.manage)}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7 text-xs px-2"
                  onClick={() => { setRunGroupId(g.id); setRunForm({ script_id: "", priority: "medium" }) }}
                  disabled={scripts.length === 0 || g.device_ids.length === 0}
                >
                  <Play className="h-3.5 w-3.5 mr-1" strokeWidth={2} />
                  {t(M.runScript)}
                </Button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Create Group Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.newGroupTitle)}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.groupNameLabel)}</Label>
              <Input
                placeholder={t(M.groupNamePh)}
                value={groupName}
                onChange={(e) => setGroupName(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label className="text-soft">{t(M.color)}</Label>
              <ColorPalette value={groupColor} onChange={setGroupColor} />
              <p className="text-xs text-muted-foreground">
                {t(M.colorHint)}
              </p>
            </div>
            <Button className="w-full" onClick={handleCreateGroup} disabled={submitting || !groupName.trim()}>
              {submitting ? t(M.creating) : t(M.create)}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* Manage Group Dialog */}
      <Dialog open={!!manageGroupId} onOpenChange={(o) => { if (!o) { setManageGroupId(null); setDeviceQuery("") } }}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{t(M.manageGroupTitle, { name: managedGroup?.name ?? "" })}</DialogTitle>
          </DialogHeader>
          {managedGroup && (
            <div className="space-y-5 pt-2">
              {/* Цвет */}
              <div className="space-y-2">
                <p className="text-sm font-medium text-foreground">{t(M.color)}</p>
                <ColorPalette
                  value={managedGroup.color}
                  onChange={(c) => handleChangeColor(managedGroup.id, c)}
                />
              </div>

              {/* Устройства */}
              <div className="space-y-2">
                <p className="text-sm font-medium text-foreground">{t(M.devices)}</p>
                <Input
                  placeholder={t(M.deviceFilterPh)}
                  value={deviceQuery}
                  onChange={(e) => setDeviceQuery(e.target.value)}
                  className="h-8 text-sm"
                />
                <div className="space-y-1 max-h-40 overflow-auto">
                  {visibleDevices.map((d) => {
                    const inGroup = managedGroup.device_ids.includes(d.id)
                    return (
                      <div key={d.id} className="flex items-center justify-between text-sm py-1">
                        <span className={inGroup ? "font-medium text-foreground" : "text-muted-foreground"}>{d.hostname}</span>
                        <Button
                          size="sm"
                          variant={inGroup ? "destructive" : "outline"}
                          className="h-6 text-xs px-2"
                          onClick={() => inGroup
                            ? handleRemoveDevice(managedGroup.id, d.id)
                            : handleAddDevice(managedGroup.id, d.id)
                          }
                        >
                          {inGroup ? t(M.remove) : t(M.add)}
                        </Button>
                      </div>
                    )
                  })}
                  {visibleDevices.length === 0 && (
                    <p className="text-xs text-muted-foreground">
                      {devices.length === 0 ? t(M.noDevices) : t(M.nothingFound)}
                    </p>
                  )}
                </div>
              </div>

              {/* Скрипт-политики */}
              <div className="space-y-2">
                <p className="text-sm font-medium text-foreground">{t(M.scriptPolicies)}</p>
                <div className="space-y-1 max-h-40 overflow-auto">
                  {scriptPolicies.map((p) => {
                    const assigned = managedGroup.policy_ids.includes(p.id)
                    return (
                      <div key={p.id} className="flex items-center justify-between text-sm py-1">
                        <span className={assigned ? "font-medium text-foreground" : "text-muted-foreground"}>{p.name}</span>
                        <Button
                          size="sm"
                          variant={assigned ? "destructive" : "outline"}
                          className="h-6 text-xs px-2"
                          onClick={() => assigned
                            ? handleUnassignPolicy(managedGroup.id, p.id)
                            : handleAssignPolicy(managedGroup.id, p.id)
                          }
                        >
                          {assigned ? t(M.unassign) : t(M.assign)}
                        </Button>
                      </div>
                    )
                  })}
                  {scriptPolicies.length === 0 && <p className="text-xs text-muted-foreground">{t(M.noScriptPolicies)}</p>}
                </div>
              </div>

              {/* Софт-правила */}
              <div className="space-y-2">
                <p className="text-sm font-medium text-foreground flex items-center gap-1.5">
                  <ShieldCheck className="h-4 w-4 text-muted-foreground" strokeWidth={2} />
                  {t(M.softwareRules)}
                </p>
                <div className="space-y-1 max-h-40 overflow-auto">
                  {managedGroup.software_rules.map((rule) => (
                    <div key={rule.id} className="flex items-center justify-between text-sm py-1">
                      <span className="flex items-center gap-2">
                        <span className="font-medium text-foreground">{rule.software_name}</span>
                        <Badge variant={rule.rule_type === "forbidden" ? "destructive" : "secondary"}>
                          {rule.rule_type === "forbidden" ? t(M.ruleForbidden) : t(M.ruleAllowed)}
                        </Badge>
                      </span>
                      <Button
                        size="sm"
                        variant="destructive"
                        className="h-6 text-xs px-2"
                        onClick={() => handleRemoveSoftwareRule(managedGroup.id, rule.id)}
                      >
                        {t(M.removeRule)}
                      </Button>
                    </div>
                  ))}
                  {managedGroup.software_rules.length === 0 && (
                    <p className="text-xs text-muted-foreground">{t(M.noSoftwareRules)}</p>
                  )}
                </div>
                <div className="flex items-end gap-2 pt-1">
                  <div className="flex-1 space-y-1">
                    <Label className="text-xs text-soft">{t(M.softwareLabel)}</Label>
                    <Input
                      placeholder="chrome.exe"
                      value={softwareForm.software_name}
                      onChange={(e) => setSoftwareForm({ ...softwareForm, software_name: e.target.value })}
                    />
                  </div>
                  <div className="w-36 space-y-1">
                    <Label className="text-xs text-soft">{t(M.typeLabel)}</Label>
                    <Select
                      value={softwareForm.rule_type}
                      onChange={(v) => setSoftwareForm({ ...softwareForm, rule_type: v as "allowed" | "forbidden" })}
                      options={[
                        { value: "forbidden", label: t(M.ruleForbidden) },
                        { value: "allowed", label: t(M.ruleAllowed) },
                      ]}
                    />
                  </div>
                  <Button
                    size="sm"
                    onClick={() => handleAddSoftwareRule(managedGroup.id)}
                    disabled={submitting || !softwareForm.software_name.trim()}
                  >
                    {t(M.add)}
                  </Button>
                </div>
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* Run Script Dialog */}
      <Dialog open={!!runGroupId} onOpenChange={(o) => !o && setRunGroupId(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.runScriptOnGroup)}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.scriptLabel)}</Label>
              <Select
                value={runForm.script_id}
                onChange={(v) => setRunForm({ ...runForm, script_id: v })}
                placeholder={t(M.selectScriptPh)}
                options={scripts.map((s) => ({ value: s.id, label: `${s.name} (${s.platform})` }))}
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.priorityLabel)}</Label>
              <Select
                value={runForm.priority}
                onChange={(v) => setRunForm({ ...runForm, priority: v as typeof runForm.priority })}
                options={[
                  { value: "low", label: t(M.priorityLow) },
                  { value: "medium", label: t(M.priorityMedium) },
                  { value: "high", label: t(M.priorityHigh) },
                ]}
              />
            </div>
            <p className="text-xs text-muted-foreground">
              {t(M.runHint)}
            </p>
            <Button className="w-full" onClick={handleRunScript} disabled={submitting || !runForm.script_id}>
              {submitting ? t(M.running) : t(M.run)}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
