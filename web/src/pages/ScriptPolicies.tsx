import { useEffect, useState } from "react"
import { Plus, Trash2, ScrollText } from "lucide-react"
import api, { Script, ScriptPolicy, ScriptResult, DeviceGroup, ScriptPolicyCompliance } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Select } from "@/components/ui/select"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { formatDistanceToNow } from "@/lib/time"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

const triggerLabel: Record<string, Msg> = {
  schedule: { ru: "По расписанию", en: "Scheduled" },
  event_trigger: { ru: "По событию", en: "Event-based" },
  on_connect: { ru: "При подключении", en: "On connect" },
}

const M = {
  loadResultsErr: { ru: "Не удалось загрузить результаты", en: "Failed to load results" },
  loadDataErr: { ru: "Не удалось загрузить данные", en: "Failed to load data" },
  createErr: { ru: "Не удалось создать политику", en: "Failed to create policy" },
  deleteErr: { ru: "Не удалось удалить политику", en: "Failed to delete policy" },
  toggleErr: { ru: "Не удалось изменить статус политики", en: "Failed to change policy status" },
  loading: { ru: "Загрузка...", en: "Loading..." },
  title: { ru: "Политики скриптов", en: "Script policies" },
  newPolicy: { ru: "Новая политика", en: "New policy" },
  noScriptsHint: { ru: "Сначала создайте скрипты в разделе «Скрипты».", en: "Create scripts in the Scripts section first." },
  searchPlaceholder: { ru: "Поиск по названию...", en: "Search by name..." },
  colName: { ru: "Название", en: "Name" },
  colScript: { ru: "Скрипт", en: "Script" },
  colPassFailTitle: { ru: "Устройств прошло / не прошло по последнему прогону", en: "Devices passed / failed on the last run" },
  colTrigger: { ru: "Триггер", en: "Trigger" },
  colAssignment: { ru: "Назначение", en: "Assignment" },
  colActive: { ru: "Активна", en: "Active" },
  colCreated: { ru: "Создана", en: "Created" },
  noPolicies: { ru: "Нет политик", en: "No policies" },
  nothingFound: { ru: "Ничего не найдено", en: "Nothing found" },
  notAssignedTitle: {
    ru: "Политика не назначена ни одной группе — она не выполнится. Назначьте группу в разделе «Группы».",
    en: "The policy is not assigned to any group and will not run. Assign a group in the Groups section.",
  },
  notAssigned: { ru: "⚠ Не назначена", en: "⚠ Not assigned" },
  deactivateAria: { ru: "Деактивировать политику", en: "Deactivate policy" },
  activateAria: { ru: "Активировать политику", en: "Activate policy" },
  resultsAria: { ru: "Результаты запусков", en: "Run results" },
  deletePolicyAria: { ru: "Удалить политику", en: "Delete policy" },
  newPolicyTitle: { ru: "Новая политика скрипта", en: "New script policy" },
  fieldName: { ru: "Название", en: "Name" },
  namePlaceholder: { ru: "Обновление Chrome по расписанию", en: "Scheduled Chrome update" },
  fieldScript: { ru: "Скрипт", en: "Script" },
  selectScriptPlaceholder: { ru: "Выберите скрипт...", en: "Select a script..." },
  fieldTrigger: { ru: "Триггер", en: "Trigger" },
  optSchedule: { ru: "По расписанию", en: "On schedule" },
  optEvent: { ru: "По событию", en: "On event" },
  optOnConnect: { ru: "При подключении к сети", en: "On network connect" },
  fieldCron: { ru: "Cron-выражение", en: "Cron expression" },
  cronExamplePre: { ru: "Пример:", en: "Example:" },
  cronExampleSuffix: { ru: "— каждый будний день в 9:00", en: "— every weekday at 9:00" },
  cronLocalTime: { ru: "Время — локальное на устройстве, не серверное.", en: "The time is local to the device, not the server." },
  fieldEvent: { ru: "Событие", en: "Event" },
  optLogin: { ru: "Вход пользователя", en: "User login" },
  optLogout: { ru: "Выход пользователя", en: "User logout" },
  optNetworkChange: { ru: "Смена сети", en: "Network change" },
  fieldGroup: { ru: "Группа устройств", en: "Device group" },
  createGroupFirst: { ru: "Сначала создайте группу", en: "Create a group first" },
  selectGroupPlaceholder: { ru: "Выберите группу...", en: "Select a group..." },
  groupHintAssigned: { ru: "Политика будет применяться к устройствам этой группы.", en: "The policy will apply to devices in this group." },
  groupHintUnassigned: {
    ru: "⚠ Без группы политика не выполняется. Можно назначить позже в разделе «Группы».",
    en: "⚠ Without a group the policy will not run. You can assign one later in the Groups section.",
  },
  creating: { ru: "Создание...", en: "Creating..." },
  create: { ru: "Создать", en: "Create" },
  resultsTitle: { ru: "Результаты запусков", en: "Run results" },
  noRuns: { ru: "Запусков пока не было.", en: "No runs yet." },
  runSuccess: { ru: "Успех", en: "Success" },
  runCode: { ru: "Код {code}", en: "Code {code}" },
  passfailNoScope: { ru: "Политика не назначена ни на одну группу с устройствами", en: "The policy is not assigned to any group with devices" },
  passfailPassTitle: { ru: "Последний прогон завершился с кодом 0", en: "The last run finished with exit code 0" },
  passfailFailTitle: { ru: "Последний прогон завершился с ненулевым кодом", en: "The last run finished with a non-zero exit code" },
  passfailUnknownTitle: { ru: "Устройств ещё не отчиталось", en: "Devices have not reported yet" },
}

const triggerVariant: Record<string, "default" | "secondary" | "outline"> = {
  schedule: "default",
  event_trigger: "secondary",
  on_connect: "outline",
}

// PassFail — Pass/Fail по ПОСЛЕДНЕМУ прогону на каждом назначенном устройстве.
// Pass = exit_code 0. Хвост «·N» — устройства, где политика назначена, но результата
// ещё нет; без него сумма pass+fail не сходилась бы с охватом и это выглядело бы багом.
function PassFail({ c }: { c?: ScriptPolicyCompliance }) {
  const t = useT()
  if (!c) return <span className="text-muted-foreground text-xs">…</span>
  if (c.in_scope === 0) {
    return (
      <span className="text-muted-foreground text-xs" title={t(M.passfailNoScope)}>
        —
      </span>
    )
  }
  return (
    <span className="flex items-center gap-2 text-sm tabular-nums">
      <span className="text-emerald-600 dark:text-emerald-400 font-medium" title={t(M.passfailPassTitle)}>
        {c.pass}
      </span>
      <span className="text-muted-foreground/40">/</span>
      <span className={c.fail > 0 ? "text-red-600 dark:text-red-400 font-semibold" : "text-muted-foreground"} title={t(M.passfailFailTitle)}>
        {c.fail}
      </span>
      {c.unknown > 0 && (
        <span className="text-muted-foreground/70 text-xs" title={t(M.passfailUnknownTitle)}>
          ·{c.unknown}
        </span>
      )}
    </span>
  )
}

export default function ScriptPolicies() {
  const t = useT()
  const [policies, setPolicies] = useState<ScriptPolicy[]>([])
  const [scripts, setScripts] = useState<Script[]>([])
  const [groups, setGroups] = useState<DeviceGroup[]>([])
  const [compliance, setCompliance] = useState<Record<string, ScriptPolicyCompliance>>({})
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState("")

  const [createPolicyOpen, setCreatePolicyOpen] = useState(false)
  const [policyForm, setPolicyForm] = useState({
    name: "",
    script_id: "",
    trigger_type: "schedule" as "schedule" | "event_trigger" | "on_connect",
    schedule_cron: "",
    event_name: "login",
    group_id: "",
  })

  const [submitting, setSubmitting] = useState(false)

  const [resultsPolicy, setResultsPolicy] = useState<ScriptPolicy | null>(null)
  const [results, setResults] = useState<ScriptResult[]>([])
  const [resultsLoading, setResultsLoading] = useState(false)

  async function openResults(p: ScriptPolicy) {
    setResultsPolicy(p)
    setResults([])
    setResultsLoading(true)
    try {
      const r = await api.get<ScriptResult[]>(`/script-policies/${p.id}/results`)
      setResults(r.data ?? [])
    } catch {
      toast({ title: t(M.loadResultsErr), variant: "destructive" })
    } finally {
      setResultsLoading(false)
    }
  }

  async function load() {
    try {
      const [p, s, g] = await Promise.all([
        api.get<ScriptPolicy[]>("/script-policies"),
        api.get<Script[]>("/scripts"),
        api.get<DeviceGroup[]>("/device-groups"),
      ])
      setPolicies(p.data ?? [])
      setScripts(s.data ?? [])
      setGroups(g.data ?? [])
    } catch {
      toast({ title: t(M.loadDataErr), variant: "destructive" })
    } finally {
      setLoading(false)
    }
    // Отдельным запросом: агрегация по script_results тяжелее списка политик, и её
    // отказ не должен прятать сами политики.
    try {
      const c = await api.get<ScriptPolicyCompliance[]>("/script-policies/compliance")
      setCompliance(Object.fromEntries((c.data ?? []).map((x) => [x.policy_id, x])))
    } catch {
      // молча: колонка останется с «…»
    }
  }

  useEffect(() => { load() }, [])

  async function handleCreatePolicy() {
    setSubmitting(true)
    try {
      const body: Record<string, unknown> = {
        name: policyForm.name,
        script_id: policyForm.script_id,
        trigger_type: policyForm.trigger_type,
      }
      if (policyForm.trigger_type === "schedule" && policyForm.schedule_cron) {
        body.schedule_config = { cron: policyForm.schedule_cron }
      }
      if (policyForm.trigger_type === "event_trigger") {
        body.event_trigger_config = { event: policyForm.event_name }
      }
      const created = await api.post<ScriptPolicy>("/script-policies", body)
      // Назначаем группу сразу при создании, иначе политика молча не выполняется (#4).
      // Два запроса не атомарны: если привязка не удалась, откатываем создание, иначе
      // остаётся политика-сирота, а пользователь видит только «не удалось создать».
      if (policyForm.group_id && created.data?.id) {
        try {
          await api.post(`/device-groups/${policyForm.group_id}/policies`, { policy_id: created.data.id })
        } catch (assignErr) {
          await api.delete(`/script-policies/${created.data.id}`).catch(() => {})
          throw assignErr
        }
      }
      setCreatePolicyOpen(false)
      setPolicyForm({ name: "", script_id: "", trigger_type: "schedule", schedule_cron: "", event_name: "login", group_id: "" })
      await load()
    } catch {
      toast({ title: t(M.createErr), variant: "destructive" })
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDeletePolicy(id: string) {
    try {
      await api.delete(`/script-policies/${id}`)
      setPolicies((prev) => prev.filter((p) => p.id !== id))
    } catch {
      toast({ title: t(M.deleteErr), variant: "destructive" })
    }
  }

  async function handleTogglePolicy(id: string, active: boolean) {
    try {
      await api.patch(`/script-policies/${id}/toggle`, { active })
      setPolicies((prev) => prev.map((p) => p.id === id ? { ...p, is_active: active } : p))
    } catch {
      toast({ title: t(M.toggleErr), variant: "destructive" })
    }
  }

  const q = query.trim().toLowerCase()
  const visiblePolicies = q
    ? policies.filter((p) => p.name.toLowerCase().includes(q) || p.script_name.toLowerCase().includes(q))
    : policies

  if (loading) return <div className="flex items-center justify-center h-48 text-muted-foreground text-sm">{t(M.loading)}</div>

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <Button size="sm" onClick={() => setCreatePolicyOpen(true)} disabled={scripts.length === 0}>
          <Plus className="h-4 w-4 mr-1.5" />
          {t(M.newPolicy)}
        </Button>
      </div>
      {scripts.length === 0 && (
        <p className="text-sm text-muted-foreground">{t(M.noScriptsHint)}</p>
      )}

      {/* Поиск — отдельная стеклянная панель, как на «Скриптах»: карте таблицы нужен
          overflow-hidden, и он обрезал бы всплывающие элементы фильтров. */}
      <div className="glass flex flex-wrap items-center gap-3 px-5 py-4">
        <Input
          placeholder={t(M.searchPlaceholder)}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="max-w-xs"
        />
      </div>

      <div className="glass overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow className="border-t-0 hover:bg-transparent">
              <TableHead className="text-xs">{t(M.colName)}</TableHead>
              <TableHead className="text-xs">{t(M.colScript)}</TableHead>
              <TableHead className="text-xs" title={t(M.colPassFailTitle)}>Pass / Fail</TableHead>
              <TableHead className="text-xs">{t(M.colTrigger)}</TableHead>
              <TableHead className="text-xs">{t(M.colAssignment)}</TableHead>
              <TableHead className="text-xs">{t(M.colActive)}</TableHead>
              <TableHead className="text-xs">{t(M.colCreated)}</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {visiblePolicies.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={8} className="text-center text-sm text-muted-foreground py-8">
                  {policies.length === 0 ? t(M.noPolicies) : t(M.nothingFound)}
                </TableCell>
              </TableRow>
            )}
            {visiblePolicies.map((p) => (
              <TableRow key={p.id} className="hover:bg-transparent">
                <TableCell className="px-4 py-3 text-sm font-medium text-foreground">{p.name}</TableCell>
                <TableCell className="px-4 py-3 text-sm text-soft">{p.script_name}</TableCell>
                <TableCell className="px-4 py-3">
                  <PassFail c={compliance[p.id]} />
                </TableCell>
                <TableCell className="px-4 py-3">
                  <Badge variant={triggerVariant[p.trigger_type] ?? "default"}>
                    {triggerLabel[p.trigger_type] ? t(triggerLabel[p.trigger_type]) : p.trigger_type}
                  </Badge>
                </TableCell>
                <TableCell className="px-4 py-3">
                  {p.group_names.length === 0 ? (
                    <Badge
                      variant="secondary"
                      title={t(M.notAssignedTitle)}
                    >
                      {t(M.notAssigned)}
                    </Badge>
                  ) : (
                    <div className="flex flex-wrap gap-1">
                      {p.group_names.map((g) => (
                        <Badge key={g} variant="outline">{g}</Badge>
                      ))}
                    </div>
                  )}
                </TableCell>
                <TableCell className="px-4 py-3">
                  {/* Включённая политика — фирменный градиент; выключенная остаётся
                      нейтральной. Зелёный тут читался бы как статус устройства. */}
                  <button
                    type="button"
                    aria-label={p.is_active ? t(M.deactivateAria) : t(M.activateAria)}
                    onClick={() => handleTogglePolicy(p.id, !p.is_active)}
                    className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${p.is_active ? "brand-gradient-h" : "bg-input"}`}
                  >
                    <span className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${p.is_active ? "translate-x-[19px]" : "translate-x-[3px]"}`} />
                  </button>
                </TableCell>
                <TableCell className="px-4 py-3 text-xs text-muted-foreground">
                  {formatDistanceToNow(p.created_at)}
                </TableCell>
                <TableCell className="px-4 py-3">
                  <div className="flex items-center gap-3 justify-end">
                    <button
                      type="button"
                      aria-label={t(M.resultsAria)}
                      onClick={() => openResults(p)}
                      className="text-muted-foreground hover:text-foreground transition-colors"
                    >
                      <ScrollText className="h-4 w-4" />
                    </button>
                    <button
                      type="button"
                      aria-label={t(M.deletePolicyAria)}
                      onClick={() => handleDeletePolicy(p.id)}
                      className="text-muted-foreground hover:text-destructive transition-colors"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Create Policy Dialog */}
      <Dialog open={createPolicyOpen} onOpenChange={setCreatePolicyOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.newPolicyTitle)}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label>{t(M.fieldName)}</Label>
              <Input
                placeholder={t(M.namePlaceholder)}
                value={policyForm.name}
                onChange={(e) => setPolicyForm({ ...policyForm, name: e.target.value })}
              />
            </div>
            <div className="space-y-1.5">
              <Label>{t(M.fieldScript)}</Label>
              <Select
                value={policyForm.script_id}
                onChange={(v) => setPolicyForm({ ...policyForm, script_id: v })}
                placeholder={t(M.selectScriptPlaceholder)}
                options={scripts.map((s) => ({ value: s.id, label: `${s.name} (${s.platform})` }))}
              />
            </div>
            <div className="space-y-1.5">
              <Label>{t(M.fieldTrigger)}</Label>
              <Select
                value={policyForm.trigger_type}
                onChange={(v) => setPolicyForm({ ...policyForm, trigger_type: v as typeof policyForm.trigger_type })}
                options={[
                  { value: "schedule",      label: t(M.optSchedule)  },
                  { value: "event_trigger", label: t(M.optEvent)     },
                  { value: "on_connect",    label: t(M.optOnConnect) },
                ]}
              />
            </div>
            {policyForm.trigger_type === "schedule" && (
              <div className="space-y-1.5">
                <Label>{t(M.fieldCron)}</Label>
                <Input
                  placeholder="0 9 * * 1-5"
                  value={policyForm.schedule_cron}
                  onChange={(e) => setPolicyForm({ ...policyForm, schedule_cron: e.target.value })}
                />
                <p className="text-xs text-muted-foreground">{t(M.cronExamplePre)} <code>0 9 * * 1-5</code> {t(M.cronExampleSuffix)}</p>
                <p className="text-xs text-muted-foreground">{t(M.cronLocalTime)}</p>
              </div>
            )}
            {policyForm.trigger_type === "event_trigger" && (
              <div className="space-y-1.5">
                <Label>{t(M.fieldEvent)}</Label>
                <Select
                  value={policyForm.event_name}
                  onChange={(v) => setPolicyForm({ ...policyForm, event_name: v })}
                  options={[
                    { value: "login",          label: t(M.optLogin)         },
                    { value: "logout",         label: t(M.optLogout)        },
                    { value: "network_change", label: t(M.optNetworkChange) },
                  ]}
                />
              </div>
            )}
            <div className="space-y-1.5">
              <Label>{t(M.fieldGroup)}</Label>
              <Select
                value={policyForm.group_id}
                onChange={(v) => setPolicyForm({ ...policyForm, group_id: v })}
                placeholder={groups.length === 0 ? t(M.createGroupFirst) : t(M.selectGroupPlaceholder)}
                options={groups.map((g) => ({ value: g.id, label: g.name }))}
              />
              <p className="text-xs text-muted-foreground">
                {policyForm.group_id
                  ? t(M.groupHintAssigned)
                  : t(M.groupHintUnassigned)}
              </p>
            </div>
            <Button
              className="w-full"
              onClick={handleCreatePolicy}
              disabled={submitting || !policyForm.name || !policyForm.script_id}
            >
              {submitting ? t(M.creating) : t(M.create)}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* Results Dialog */}
      <Dialog open={resultsPolicy !== null} onOpenChange={(o) => { if (!o) setResultsPolicy(null) }}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>{t(M.resultsTitle)}: {resultsPolicy?.name}</DialogTitle>
          </DialogHeader>
          <div className="pt-2 max-h-[70vh] overflow-auto">
            {resultsLoading ? (
              <p className="text-sm text-muted-foreground">{t(M.loading)}</p>
            ) : results.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t(M.noRuns)}</p>
            ) : (
              <div className="space-y-3">
                {results.map((r) => (
                  <div key={r.id} className="rounded-xl border border-border px-5 py-[18px] space-y-2">
                    <div className="flex items-center justify-between gap-2 flex-wrap">
                      <div className="flex items-center gap-2">
                        <Badge variant={r.exit_code === 0 ? "success" : "destructive"}>
                          {r.exit_code === 0 ? t(M.runSuccess) : t(M.runCode, { code: r.exit_code })}
                        </Badge>
                        <span className="text-sm font-medium text-foreground">{r.device_hostname || r.device_id}</span>
                        <Badge variant="outline">{triggerLabel[r.trigger] ? t(triggerLabel[r.trigger]) : r.trigger}</Badge>
                      </div>
                      <span className="text-xs text-muted-foreground">{formatDistanceToNow(r.finished_at)}</span>
                    </div>
                    {r.stdout && (
                      <pre className="rounded-md border border-border bg-muted px-3 py-2.5 text-xs font-mono text-soft whitespace-pre-wrap break-all max-h-40 overflow-auto">{r.stdout}</pre>
                    )}
                    {r.stderr && (
                      <pre className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2.5 text-xs font-mono text-destructive whitespace-pre-wrap break-all max-h-40 overflow-auto">{r.stderr}</pre>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
