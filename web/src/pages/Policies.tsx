import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Trash2, ChevronLeft } from "lucide-react"
import api, { PolicyRule, SoftwarePolicyCompliance } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { formatDistanceToNow } from "@/lib/time"
import { useT } from "@/lib/i18n"

const PLATFORMS = ["macOS", "Windows", "Linux"] as const

const M = {
  ruleAllowNotChecked: { ru: "Правило-разрешение: агент его не проверяет", en: "Allow rule: the agent does not check it" },
  devPass: { ru: "Устройств соответствует правилу", en: "Devices compliant with the rule" },
  devFail: { ru: "Устройств нарушает правило", en: "Devices violating the rule" },
  loadErr: { ru: "Не удалось загрузить политики", en: "Failed to load policies" },
  deleted: { ru: "Политика удалена", en: "Policy deleted" },
  deleteErr: { ru: "Не удалось удалить политику", en: "Failed to delete policy" },
  backToPolicies: { ru: "Назад к политикам", en: "Back to policies" },
  newPolicy: { ru: "Новая политика", en: "New policy" },
  condition: { ru: "Условие", en: "Condition" },
  softwareNameAria: { ru: "Имя программы", en: "Software name" },
  conditionHint: {
    ru: "Подстрока имени программы без учёта регистра: «chrome» поймает и Google Chrome, и Chromium.",
    en: "Case-insensitive substring of the software name: «chrome» matches both Google Chrome and Chromium.",
  },
  ruleType: { ru: "Тип правила", en: "Rule type" },
  allowed: { ru: "Разрешено", en: "Allowed" },
  forbidden: { ru: "Запрещено", en: "Forbidden" },
  compatibleWith: { ru: "Совместимо с", en: "Compatible with" },
  device: { ru: "Устройство", en: "Device" },
  optional: { ru: "(необязательно)", en: "(optional)" },
  deviceIdPlaceholder: { ru: "UUID устройства — пусто = глобальное", en: "Device UUID — empty = global" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  save: { ru: "Сохранить", en: "Save" },
  cancel: { ru: "Отмена", en: "Cancel" },
  title: { ru: "Политики", en: "Policies" },
  newPolicyBtn: { ru: "+ Новая политика", en: "+ New policy" },
  searchPlaceholder: { ru: "Поиск по программе...", en: "Search by software..." },
  loading: { ru: "Загрузка...", en: "Loading..." },
  colSoftware: { ru: "Программа", en: "Software" },
  colType: { ru: "Тип", en: "Type" },
  passFailTitle: { ru: "Устройств соответствует / нарушает", en: "Devices compliant / violating" },
  colScope: { ru: "Охват", en: "Scope" },
  scopeTitle: { ru: "Устройств в области действия правила", en: "Devices in the rule's scope" },
  colUpdated: { ru: "Обновлено", en: "Updated" },
  noPolicies: { ru: "Нет политик", en: "No policies" },
  nothingFound: { ru: "Ничего не найдено", en: "Nothing found" },
  scopeGroup: { ru: "Группа", en: "Group" },
  scopeGlobal: { ru: "Глобальное", en: "Global" },
  deleteTitle: { ru: "Удалить политику?", en: "Delete policy?" },
  deleteDesc: { ru: "Правило для «{name}» будет удалено.", en: "The rule for «{name}» will be deleted." },
  delete: { ru: "Удалить", en: "Delete" },
}

// PassFail — счётчики соответствия правилу. Пока агрегация не приехала, показываем «…»,
// а не нули: ноль нарушителей и «ещё не посчитали» — разные вещи.
function PassFail({ c }: { c?: SoftwarePolicyCompliance }) {
  const t = useT()
  if (!c) return <span className="text-muted-foreground text-xs">…</span>
  if (!c.checked) {
    return (
      <span className="text-muted-foreground text-xs" title={t(M.ruleAllowNotChecked)}>
        —
      </span>
    )
  }
  return (
    <span className="flex items-center gap-2 text-sm tabular-nums">
      <span className="text-emerald-600 dark:text-emerald-400" title={t(M.devPass)}>
        {c.pass}
      </span>
      <span className="text-muted-foreground/40">/</span>
      <span className={c.fail > 0 ? "text-red-600 dark:text-red-400 font-semibold" : "text-muted-foreground"} title={t(M.devFail)}>
        {c.fail}
      </span>
    </span>
  )
}

export default function Policies() {
  const t = useT()
  const navigate = useNavigate()
  const [rules, setRules] = useState<PolicyRule[]>([])
  const [compliance, setCompliance] = useState<Record<string, SoftwarePolicyCompliance>>({})
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ software_name: "", rule_type: "allowed" as "allowed" | "forbidden", device_id: "" })
  const [platforms, setPlatforms] = useState({ macOS: true, Windows: true, Linux: true })
  const [submitting, setSubmitting] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<PolicyRule | null>(null)

  async function load() {
    try {
      const r = await api.get<PolicyRule[]>("/policies")
      setRules(r.data ?? [])
    } catch {
      toast({ title: t(M.loadErr), variant: "destructive" })
    } finally {
      setLoading(false)
    }
    // Счётчики отдельным запросом: агрегация ходит по всему парку и инвентарю, список
    // правил не должен её ждать. Ошибка не критична — колонки останутся с «…».
    try {
      const c = await api.get<SoftwarePolicyCompliance[]>("/policies/compliance")
      setCompliance(Object.fromEntries((c.data ?? []).map((x) => [x.rule_id, x])))
    } catch {
      // молча: соответствие — справочная информация, не блокирует страницу
    }
  }

  useEffect(() => { load() }, [])

  async function addRule() {
    setSubmitting(true)
    try {
      const body: Record<string, unknown> = { software_name: form.software_name, rule_type: form.rule_type }
      if (form.device_id) body.device_id = form.device_id
      body.platforms = (Object.keys(platforms) as (keyof typeof platforms)[]).filter((p) => platforms[p])
      await api.post("/policies", body)
      setForm({ software_name: "", rule_type: "allowed", device_id: "" })
      setPlatforms({ macOS: true, Windows: true, Linux: true })
      setCreating(false)
      await load()
    } finally {
      setSubmitting(false)
    }
  }

  async function deleteRule(id: string) {
    try {
      await api.delete(`/policies/${id}`)
      setRules((prev) => prev.filter((r) => r.id !== id))
      setConfirmDelete(null)
      toast({ title: t(M.deleted), variant: "success" })
    } catch {
      toast({ title: t(M.deleteErr), variant: "destructive" })
    }
  }

  function togglePlatform(p: typeof PLATFORMS[number]) {
    setPlatforms((prev) => ({ ...prev, [p]: !prev[p] }))
  }

  if (creating) {
    return (
      <div className="flex flex-col gap-5 max-w-2xl">
        <button
          type="button"
          onClick={() => setCreating(false)}
          className="flex items-center gap-1.5 self-start text-sm text-muted-foreground hover:text-foreground transition-colors"
        >
          <ChevronLeft className="h-4 w-4" strokeWidth={2} />
          {t(M.backToPolicies)}
        </button>

        <h1 className="text-xl font-semibold text-foreground">{t(M.newPolicy)}</h1>

        <div className="glass flex flex-col gap-6 px-5 py-[18px]">
          {/* Condition editor – Fleet-style */}
          <div>
            <Label className="text-sm font-medium text-soft mb-2 block">{t(M.condition)}</Label>
            <div className="rounded-md border border-input bg-transparent overflow-hidden font-mono text-sm focus-within:ring-1 focus-within:ring-ring">
              <div className="flex items-start gap-3 px-4 py-3">
                <span className="text-xs text-muted-foreground select-none mt-px">1</span>
                <input
                  id="policy-condition"
                  aria-label={t(M.softwareNameAria)}
                  className="flex-1 bg-transparent outline-none text-foreground placeholder:text-muted-foreground"
                  placeholder="chrome"
                  value={form.software_name}
                  onChange={(e) => setForm({ ...form, software_name: e.target.value })}
                />
              </div>
            </div>
            {/* Раньше placeholder показывал синтаксис `software_name = "chrome"` — его
                вводили буквально, правило молча не матчилось ни с чем. */}
            <p className="text-xs text-muted-foreground mt-1.5">
              {t(M.conditionHint)}
            </p>
          </div>

          {/* Rule type toggle */}
          <div>
            <Label className="text-sm font-medium text-soft mb-2 block">{t(M.ruleType)}</Label>
            <div className="flex gap-2">
              {(["allowed", "forbidden"] as const).map((rt) => (
                <button
                  type="button"
                  key={rt}
                  onClick={() => setForm({ ...form, rule_type: rt })}
                  className={
                    "px-3 py-1.5 rounded-md text-sm border transition-colors " +
                    (form.rule_type === rt
                      ? rt === "allowed"
                        ? "bg-emerald-600/20 border-emerald-600/50 text-emerald-600 dark:text-emerald-400"
                        : "bg-red-600/20 border-red-600/50 text-red-500 dark:text-red-400"
                      : "border-border text-muted-foreground hover:text-foreground")
                  }
                >
                  {rt === "allowed" ? t(M.allowed) : t(M.forbidden)}
                </button>
              ))}
            </div>
          </div>

          {/* Compatible with */}
          <div>
            <Label className="text-sm font-medium text-soft mb-2.5 block">{t(M.compatibleWith)}</Label>
            <div className="flex items-center gap-5">
              {PLATFORMS.map((p) => {
                const on = platforms[p]
                return (
                  <button
                    type="button"
                    key={p}
                    onClick={() => togglePlatform(p)}
                    className="flex items-center gap-1.5 text-sm transition-colors"
                  >
                    <span className={on ? "text-emerald-600 dark:text-emerald-400" : "text-muted-foreground"}>
                      {on ? "✓" : "—"}
                    </span>
                    <span className={on ? "text-foreground" : "text-muted-foreground"}>{p}</span>
                  </button>
                )
              })}
            </div>
          </div>

          {/* Optional device scope */}
          <div>
            <Label className="text-sm font-medium text-soft mb-2 block">{t(M.device)} <span className="text-muted-foreground font-normal">{t(M.optional)}</span></Label>
            <Input
              placeholder={t(M.deviceIdPlaceholder)}
              value={form.device_id}
              onChange={(e) => setForm({ ...form, device_id: e.target.value })}
              className="max-w-sm"
            />
          </div>

          {/* Actions */}
          <div className="flex items-center gap-4 pt-1">
            <Button
              onClick={addRule}
              disabled={submitting || !form.software_name || !Object.values(platforms).some(Boolean)}
            >
              {submitting ? t(M.saving) : t(M.save)}
            </Button>
            <button
              type="button"
              onClick={() => setCreating(false)}
              className="text-sm text-muted-foreground hover:text-foreground transition-colors"
            >
              {t(M.cancel)}
            </button>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <Button size="sm" onClick={() => setCreating(true)}>
          {t(M.newPolicyBtn)}
        </Button>
      </div>

      <div className="glass flex flex-wrap items-center gap-3 px-5 py-4">
        <Input
          placeholder={t(M.searchPlaceholder)}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="max-w-sm"
        />
      </div>

      {loading ? (
        <p className="text-muted-foreground text-sm">{t(M.loading)}</p>
      ) : (
        <div className="glass overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow className="border-t-0 hover:bg-transparent">
                <TableHead className="text-xs">{t(M.colSoftware)}</TableHead>
                <TableHead className="text-xs">{t(M.colType)}</TableHead>
                <TableHead className="text-xs" title={t(M.passFailTitle)}>Pass / Fail</TableHead>
                <TableHead className="text-xs" title={t(M.scopeTitle)}>{t(M.colScope)}</TableHead>
                <TableHead className="text-xs">{t(M.device)}</TableHead>
                <TableHead className="text-xs">{t(M.colUpdated)}</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(() => {
                const q = query.trim().toLowerCase()
                const filtered = q ? rules.filter((r) => r.software_name.toLowerCase().includes(q)) : rules
                if (filtered.length === 0) {
                  return (
                    <TableRow className="hover:bg-transparent">
                      <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                        {rules.length === 0 ? t(M.noPolicies) : t(M.nothingFound)}
                      </TableCell>
                    </TableRow>
                  )
                }
                return filtered.map((r) => (
                <TableRow
                  key={r.id}
                  onClick={() => navigate(`/policies/${r.id}`)}
                  className="cursor-pointer glass-hover"
                >
                  <TableCell className="px-4 py-3 font-medium font-mono text-sm text-foreground">
                    {r.software_name}
                    {r.platforms && r.platforms.length > 0 && r.platforms.length < 3 && (
                      <div className="text-[10px] text-muted-foreground font-sans mt-0.5">{r.platforms.join(", ")}</div>
                    )}
                  </TableCell>
                  <TableCell className="px-4 py-3">
                    <Badge variant={r.rule_type === "allowed" ? "success" : "destructive"}>
                      {r.rule_type === "allowed" ? t(M.allowed) : t(M.forbidden)}
                    </Badge>
                  </TableCell>
                  <TableCell className="px-4 py-3">
                    <PassFail c={compliance[r.id]} />
                  </TableCell>
                  <TableCell className="px-4 py-3 text-muted-foreground text-xs tabular-nums">
                    {compliance[r.id] ? compliance[r.id].in_scope : "…"}
                  </TableCell>
                  <TableCell className="px-4 py-3 text-muted-foreground text-xs font-mono">
                    {/* group_id раньше игнорировался — групповое правило выглядело как глобальное */}
                    {r.device_id ? r.device_id.slice(0, 8) : r.group_id ? t(M.scopeGroup) : t(M.scopeGlobal)}
                  </TableCell>
                  <TableCell className="px-4 py-3 text-xs text-muted-foreground">
                    {formatDistanceToNow(r.updated_at)}
                  </TableCell>
                  <TableCell className="px-4 py-3">
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); setConfirmDelete(r) }}
                      className="text-muted-foreground hover:text-destructive transition-colors"
                    >
                      <Trash2 className="h-4 w-4" strokeWidth={2} />
                    </button>
                  </TableCell>
                </TableRow>
                ))
              })()}
            </TableBody>
          </Table>
        </div>
      )}

      <ConfirmDialog
        open={!!confirmDelete}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
        title={t(M.deleteTitle)}
        description={confirmDelete ? t(M.deleteDesc, { name: confirmDelete.software_name }) : ""}
        confirmLabel={t(M.delete)}
        destructive
        onConfirm={() => { if (confirmDelete) deleteRule(confirmDelete.id) }}
      />
    </div>
  )
}
