import { useEffect, useState } from "react"
import { GitBranch, Plus, Minus, CheckCircle2, AlertTriangle } from "lucide-react"
import api, { PolicyAsCodeState, DesiredPolicyRule, PolicyApplyResult, errStatus } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

const M = {
  title: { ru: "Policy-as-code / GitOps", en: "Policy-as-code / GitOps" },
  intro: {
    ru: "Декларативное управление ГЛОБАЛЬНЫМИ software-политиками. Опишите желаемый набор правил в JSON — сервер хранит его как source-of-truth и приводит живые правила в точное соответствие декларации (создаёт недостающие, удаляет лишние). Дрейф показывает, что кто-то менял правила в обход декларации.",
    en: "Declarative management of GLOBAL software policies. Describe the desired set of rules in JSON — the server stores it as the source of truth and reconciles live rules to match the declaration exactly (creating missing ones, deleting extras). Drift shows where someone changed rules outside the declaration.",
  },
  gitopsNote: {
    ru: "GitOps: держите декларацию в git на стороне деплойера и заливайте её этим эндпоинтом (например, из CI: POST /api/v1/policy-as-code/apply). Реконсиляция затрагивает только глобальные software-правила — правила устройств и групп не трогаются.",
    en: "GitOps: keep the declaration in git on the deployer side and push it via this endpoint (e.g. from CI: POST /api/v1/policy-as-code/apply). Reconciliation touches only global software rules — device- and group-scoped rules are left alone.",
  },
  unavailableTitle: { ru: "Policy-as-code недоступен в этой редакции", en: "Policy-as-code is not available in this edition" },
  unavailableBody: {
    ru: "Декларативные политики (GitOps) — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Declarative policies (GitOps) are an Enterprise-edition feature. They require an active license covering this feature.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить данные", en: "Failed to load data" },

  driftTitle: { ru: "Дрейф от декларации", en: "Drift from declaration" },
  noDeclaration: {
    ru: "Декларация ещё не применялась — source-of-truth не задан. Опишите правила ниже и примените.",
    en: "No declaration applied yet — the source of truth is not set. Describe rules below and apply.",
  },
  inSync: { ru: "В соответствии", en: "In sync" },
  toCreate: { ru: "Будет создано", en: "To create" },
  toDelete: { ru: "Будет удалено", en: "To delete" },
  noDrift: { ru: "Дрейфа нет — живые правила соответствуют декларации.", en: "No drift — live rules match the declaration." },
  lastApplied: { ru: "Последнее применение: {who}, {when}", en: "Last applied: {who}, {when}" },
  unknownWho: { ru: "неизвестно", en: "unknown" },

  declTitle: { ru: "Декларация (JSON-массив правил)", en: "Declaration (JSON array of rules)" },
  declHint: {
    ru: "Массив правил: software_name (обязательно), rule_type (allowed|forbidden), platforms (подмножество [\"macOS\",\"Windows\",\"Linux\"]; пусто или все три — все платформы). Применение ПРИВОДИТ живые глобальные правила в точное соответствие этому массиву.",
    en: "Array of rules: software_name (required), rule_type (allowed|forbidden), platforms (a subset of [\"macOS\",\"Windows\",\"Linux\"]; empty or all three — all platforms). Applying MAKES live global rules match this array exactly.",
  },
  apply: { ru: "Применить и реконсилить", en: "Apply and reconcile" },
  applying: { ru: "Применение...", en: "Applying..." },
  format: { ru: "Форматировать JSON", en: "Format JSON" },
  badJson: { ru: "Текст не является корректным JSON-массивом правил", en: "The text is not a valid JSON array of rules" },
  applied: { ru: "Применено. Создано: {c}, удалено: {d}", en: "Applied. Created: {c}, deleted: {d}" },

  confirmTitle: { ru: "Применить декларацию?", en: "Apply the declaration?" },
  confirmDesc: {
    ru: "Живые глобальные software-правила будут приведены в точное соответствие декларации: недостающие создадутся, а правила, которых нет в декларации, будут УДАЛЕНЫ. Действие выполняется в транзакции и попадает в журнал аудита.",
    en: "Live global software rules will be reconciled to match the declaration exactly: missing ones are created, and rules not present in the declaration are DELETED. The action runs in a transaction and is written to the audit log.",
  },
  confirmEmptyTitle: { ru: "Пустая декларация — удалить ВСЕ глобальные правила?", en: "Empty declaration — delete ALL global rules?" },
  confirmEmptyDesc: {
    ru: "Декларация пуста. Применение УДАЛИТ все глобальные software-правила. Это необратимо (восстановить — повторным применением непустой декларации). Продолжить?",
    en: "The declaration is empty. Applying it will DELETE all global software rules. This is irreversible (restore by re-applying a non-empty declaration). Proceed?",
  },
  confirmApply: { ru: "Применить", en: "Apply" },

  colSoftware: { ru: "ПО", en: "Software" },
  ruleAllowed: { ru: "разрешено", en: "allowed" },
  ruleForbidden: { ru: "запрещено", en: "forbidden" },
  allPlatforms: { ru: "все платформы", en: "all platforms" },
}

// Пример-заглушка для пустого редактора: показывает форму правила, не применяя ничего.
const EXAMPLE: DesiredPolicyRule[] = [
  { software_name: "uTorrent", rule_type: "forbidden", platforms: ["Windows"] },
  { software_name: "Slack", rule_type: "allowed" },
]

function ruleLabel(t: ReturnType<typeof useT>, r: DesiredPolicyRule) {
  const kind = r.rule_type === "allowed" ? t(M.ruleAllowed) : t(M.ruleForbidden)
  const plat = r.platforms && r.platforms.length > 0 ? r.platforms.join(", ") : t(M.allPlatforms)
  return { kind, plat }
}

export default function PolicyAsCode() {
  const t = useT()

  const [state, setState] = useState<PolicyAsCodeState | null>(null)
  const [text, setText] = useState("")
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [applying, setApplying] = useState(false)

  // Разобранные правила, ожидающие подтверждения (null = диалог закрыт).
  const [pending, setPending] = useState<{ rules: DesiredPolicyRule[]; empty: boolean } | null>(null)

  async function load(prefill: boolean) {
    setLoadError(false)
    try {
      const r = await api.get<PolicyAsCodeState>("/policy-as-code")
      setState(r.data)
      // Прелоад текстового поля декларацией — только на первичной загрузке, чтобы не
      // затирать правки пользователя при рефреше дрейфа после применения.
      if (prefill) {
        const content = r.data.declaration?.content ?? EXAMPLE
        setText(JSON.stringify(content, null, 2))
      }
    } catch (e) {
      if (errStatus(e) === 404 || errStatus(e) === 402) {
        setUnavailable(true)
      } else {
        setLoadError(true)
        toast({ title: t(M.loadErr), variant: "destructive" })
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function parseRules(): DesiredPolicyRule[] | null {
    try {
      const parsed = JSON.parse(text)
      if (!Array.isArray(parsed)) {
        toast({ title: t(M.badJson), variant: "destructive" })
        return null
      }
      return parsed as DesiredPolicyRule[]
    } catch {
      toast({ title: t(M.badJson), variant: "destructive" })
      return null
    }
  }

  function onApplyClick() {
    const rules = parseRules()
    if (rules === null) return
    setPending({ rules, empty: rules.length === 0 })
  }

  function formatJson() {
    const rules = parseRules()
    if (rules === null) return
    setText(JSON.stringify(rules, null, 2))
  }

  async function doApply() {
    if (!pending) return
    setApplying(true)
    try {
      const r = await api.post<PolicyApplyResult>("/policy-as-code/apply", {
        rules: pending.rules,
        confirm_empty: pending.empty,
      })
      toast({ title: t(M.applied, { c: r.data.created, d: r.data.deleted }), variant: "success" })
      await load(false) // обновить дрейф/декларацию, текст не трогаем
    } catch {
      // авто-тост интерсептора (400 на невалидном правиле и т.п.)
    } finally {
      setApplying(false)
      setPending(null)
    }
  }

  if (loading) return <p className="text-muted-foreground text-sm">{t(M.loading)}</p>

  if (unavailable) {
    return (
      <div className="flex flex-col gap-5 max-w-2xl">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <div className="glass px-5 py-[18px] space-y-2">
          <div className="flex items-center gap-2">
            <Badge variant="secondary">Free</Badge>
            <span className="text-[15px] font-semibold text-foreground">{t(M.unavailableTitle)}</span>
          </div>
          <p className="text-sm text-muted-foreground">{t(M.unavailableBody)}</p>
        </div>
      </div>
    )
  }

  const drift = state?.drift
  const decl = state?.declaration ?? null
  const noDrift = drift && drift.to_create.length === 0 && drift.to_delete.length === 0

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground flex items-center gap-2">
          <GitBranch className="h-5 w-5 text-muted-foreground" />
          {t(M.title)}
        </h1>
        <p className="text-sm text-muted-foreground max-w-3xl mt-1">{t(M.intro)}</p>
      </div>

      {loadError && (
        <div className="glass px-5 py-[18px] text-sm flex items-center gap-3">
          <p className="text-destructive">{t(M.loadErr)}</p>
          <Button variant="outline" size="sm" onClick={() => load(false)}>{t(M.loading)}</Button>
        </div>
      )}

      {/* Дрейф */}
      <div className="glass px-5 py-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-[15px] font-semibold text-foreground">{t(M.driftTitle)}</h2>
          {decl && (
            <span className="text-xs text-muted-foreground">
              {t(M.lastApplied, { who: decl.applied_by || t(M.unknownWho), when: new Date(decl.applied_at).toLocaleString() })}
            </span>
          )}
        </div>

        {!decl && <p className="text-sm text-soft">{t(M.noDeclaration)}</p>}

        {decl && (
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline" className="border-emerald-500/20 bg-emerald-500/15 text-emerald-700 dark:border-emerald-400/25 dark:bg-emerald-400/15 dark:text-emerald-300">
              <CheckCircle2 className="h-3.5 w-3.5 mr-1" />
              {t(M.inSync)}: {drift?.in_sync ?? 0}
            </Badge>
            <Badge variant="outline" className="border-sky-500/20 bg-sky-500/15 text-sky-700 dark:border-sky-400/25 dark:bg-sky-400/15 dark:text-sky-300">
              <Plus className="h-3.5 w-3.5 mr-1" />
              {t(M.toCreate)}: {drift?.to_create.length ?? 0}
            </Badge>
            <Badge variant="outline" className="border-red-500/20 bg-red-500/15 text-red-700 dark:border-red-400/25 dark:bg-red-400/15 dark:text-red-300">
              <Minus className="h-3.5 w-3.5 mr-1" />
              {t(M.toDelete)}: {drift?.to_delete.length ?? 0}
            </Badge>
          </div>
        )}

        {decl && noDrift && (
          <p className="text-sm text-emerald-700 dark:text-emerald-400 flex items-center gap-1.5">
            <CheckCircle2 className="h-4 w-4" />
            {t(M.noDrift)}
          </p>
        )}

        {drift && (drift.to_create.length > 0 || drift.to_delete.length > 0) && (
          <div className="grid gap-4 sm:grid-cols-2">
            {drift.to_create.length > 0 && (
              <div className="space-y-1.5">
                <p className="text-xs font-medium text-sky-700 dark:text-sky-400">{t(M.toCreate)}</p>
                <ul className="space-y-1">
                  {drift.to_create.map((r, i) => {
                    const { kind, plat } = ruleLabel(t, r)
                    return (
                      <li key={`c-${i}`} className="text-sm text-foreground">
                        <span className="font-medium">{r.software_name}</span>{" "}
                        <span className="text-muted-foreground">— {kind}, {plat}</span>
                      </li>
                    )
                  })}
                </ul>
              </div>
            )}
            {drift.to_delete.length > 0 && (
              <div className="space-y-1.5">
                <p className="text-xs font-medium text-red-700 dark:text-red-400 flex items-center gap-1">
                  <AlertTriangle className="h-3.5 w-3.5" />
                  {t(M.toDelete)}
                </p>
                <ul className="space-y-1">
                  {drift.to_delete.map((r, i) => {
                    const { kind, plat } = ruleLabel(t, r)
                    return (
                      <li key={`d-${i}`} className="text-sm text-foreground">
                        <span className="font-medium">{r.software_name}</span>{" "}
                        <span className="text-muted-foreground">— {kind}, {plat}</span>
                      </li>
                    )
                  })}
                </ul>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Редактор декларации */}
      <div className="glass px-5 py-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-[15px] font-semibold text-foreground">{t(M.declTitle)}</h2>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={formatJson}>{t(M.format)}</Button>
            <Button size="sm" onClick={onApplyClick} disabled={applying}>
              <GitBranch className="h-4 w-4 mr-1.5" />
              {applying ? t(M.applying) : t(M.apply)}
            </Button>
          </div>
        </div>
        <textarea
          className="flex min-h-64 w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm font-mono shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-y"
          spellCheck={false}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
        <p className="text-xs text-muted-foreground max-w-3xl">{t(M.declHint)}</p>
        <p className="text-xs text-muted-foreground max-w-3xl">{t(M.gitopsNote)}</p>
      </div>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(o) => { if (!o) setPending(null) }}
        title={pending?.empty ? t(M.confirmEmptyTitle) : t(M.confirmTitle)}
        description={pending?.empty ? t(M.confirmEmptyDesc) : t(M.confirmDesc)}
        confirmLabel={t(M.confirmApply)}
        destructive
        onConfirm={doApply}
      />
    </div>
  )
}
