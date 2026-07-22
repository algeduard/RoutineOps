import { useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { ChevronLeft, ChevronRight } from "lucide-react"
import api, { PolicyRule, PolicyDeviceCompliance, DeviceGroup } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { toast } from "@/lib/toast"
import { formatDistanceToNow } from "@/lib/time"
import { useT } from "@/lib/i18n"

const M = {
  loadErr: { ru: "Не удалось загрузить политику", en: "Failed to load policy" },
  loading: { ru: "Загрузка...", en: "Loading..." },
  backToPolicies: { ru: "Назад к политикам", en: "Back to policies" },
  loadFailed: { ru: "Не удалось загрузить политику — попробуй обновить страницу.", en: "Failed to load the policy — try refreshing the page." },
  notFound: { ru: "Политика не найдена — возможно, она была удалена.", en: "Policy not found — it may have been deleted." },
  forbidden: { ru: "Запрещено", en: "Forbidden" },
  allowed: { ru: "Разрешено", en: "Allowed" },
  scopeDevice: { ru: "Устройство {id}", en: "Device {id}" },
  scopeGroup: { ru: "Группа «{name}»", en: "Group «{name}»" },
  scopeGlobal: { ru: "Глобальное — весь парк", en: "Global — entire fleet" },
  updatedSep: { ru: " · обновлено ", en: " · updated " },
  inScope: { ru: "В охвате:", en: "In scope:" },
  allowRuleNote: {
    ru: "Правило-разрешение: агент его не проверяет. Ниже — справка, где это ПО установлено.",
    en: "Allow rule: the agent does not check it. Below is a reference of where this software is installed.",
  },
  noDevices: { ru: "Правило не действует ни на одно устройство.", en: "The rule does not apply to any device." },
  checkGroup: { ru: " Проверь, что в группе есть устройства.", en: " Check that the group has devices." },
  checkPlatforms: {
    ru: " Возможно, в парке нет устройств выбранных платформ.",
    en: " There may be no devices of the selected platforms in the fleet.",
  },
  colDevice: { ru: "Устройство", en: "Device" },
  colOS: { ru: "ОС", en: "OS" },
  colFound: { ru: "Найдено в инвентаре", en: "Found in inventory" },
  colVerdict: { ru: "Вердикт", en: "Verdict" },
  colInstalled: { ru: "Установлено", en: "Installed" },
  yes: { ru: "Да", en: "Yes" },
  no: { ru: "Нет", en: "No" },
}

// Разрез софт-правила по устройствам: кто в области действия, кто pass, кто fail
// и что именно совпало в инвентаре. Для allowed-правил вердикта нет (агент их не
// проверяет) — колонка показывает справку «установлено/нет».
export default function PolicyDetail() {
  const t = useT()
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [rule, setRule] = useState<PolicyRule | null>(null)
  const [rows, setRows] = useState<PolicyDeviceCompliance[]>([])
  const [groups, setGroups] = useState<DeviceGroup[]>([])
  const [loading, setLoading] = useState(true)
  // Ошибка загрузки ≠ «политика не найдена»: не приписываем удаление при упавшем API.
  const [loadFailed, setLoadFailed] = useState(false)

  useEffect(() => {
    if (!id) return
    // Флаг отмены: при back/forward ответ старого id не должен перезаписать новый.
    let stale = false
    Promise.all([
      // Отдельного GET /policies/{id} нет — правило достаём из общего списка.
      api.get<PolicyRule[]>("/policies"),
      api.get<PolicyDeviceCompliance[]>(`/policies/${id}/compliance`),
    ]).then(([r, c]) => {
      if (stale) return
      setRule((r.data ?? []).find((x) => x.id === id) ?? null)
      setRows(c.data ?? [])
    }).catch(() => {
      if (stale) return
      setLoadFailed(true)
      toast({ title: t(M.loadErr), variant: "destructive" })
    }).finally(() => { if (!stale) setLoading(false) })
    // Группы нужны только для имени в подзаголовке — best-effort, не валит страницу.
    api.get<DeviceGroup[]>("/device-groups")
      .then((g) => { if (!stale) setGroups(g.data ?? []) })
      .catch(() => {})
    return () => { stale = true }
  }, [id])

  if (loading) {
    return <div className="flex items-center justify-center h-48 text-muted-foreground text-sm">{t(M.loading)}</div>
  }

  const back = (
    <button
      type="button"
      onClick={() => navigate("/policies")}
      className="flex items-center gap-1.5 self-start text-sm text-muted-foreground hover:text-foreground transition-colors"
    >
      <ChevronLeft className="h-4 w-4" strokeWidth={2} />
      {t(M.backToPolicies)}
    </button>
  )

  if (!rule) {
    return (
      <div className="flex flex-col gap-5">
        {back}
        <div className="glass px-5 py-8 text-center text-sm text-muted-foreground">
          {loadFailed ? t(M.loadFailed) : t(M.notFound)}
        </div>
      </div>
    )
  }

  const forbidden = rule.rule_type === "forbidden"
  const fail = rows.filter((r) => r.installed).length
  const pass = rows.length - fail

  const scope = rule.device_id
    ? t(M.scopeDevice, { id: rule.device_id.slice(0, 8) })
    : rule.group_id
      ? t(M.scopeGroup, { name: groups.find((g) => g.id === rule.group_id)?.name ?? rule.group_id.slice(0, 8) })
      : t(M.scopeGlobal)

  return (
    <div className="flex flex-col gap-5">
      {back}

      <div>
        <div className="flex items-center gap-3 mb-1 flex-wrap">
          <h1 className="text-xl font-semibold font-mono text-foreground">{rule.software_name}</h1>
          <Badge variant={forbidden ? "destructive" : "success"}>
            {forbidden ? t(M.forbidden) : t(M.allowed)}
          </Badge>
        </div>
        <p className="text-xs text-muted-foreground">
          {scope}
          {rule.platforms && rule.platforms.length > 0 && rule.platforms.length < 3 && (
            <> · {rule.platforms.join(", ")}</>
          )}
          {t(M.updatedSep)}{formatDistanceToNow(rule.updated_at)}
        </p>

        {/* Сводка. Для allowed-правил pass/fail не считаются — агент проверяет только forbidden. */}
        {forbidden ? (
          <div className="flex items-center gap-6 mt-3 text-[13px]">
            <span className="text-soft">{t(M.inScope)} <span className="text-foreground font-medium tabular-nums">{rows.length}</span></span>
            <span className="text-emerald-600 dark:text-emerald-400">Pass: <span className="font-medium tabular-nums">{pass}</span></span>
            <span className={fail > 0 ? "text-red-600 dark:text-red-400 font-medium" : "text-muted-foreground"}>
              Fail: <span className="tabular-nums">{fail}</span>
            </span>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground mt-3">
            {t(M.allowRuleNote)}
          </p>
        )}
      </div>

      {rows.length === 0 ? (
        <div className="glass px-5 py-8 text-center text-sm text-muted-foreground">
          {t(M.noDevices)}
          {rule.group_id && t(M.checkGroup)}
          {rule.platforms && rule.platforms.length > 0 && rule.platforms.length < 3 && t(M.checkPlatforms)}
        </div>
      ) : (
        <div className="glass overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow className="border-t-0 hover:bg-transparent">
                <TableHead className="text-xs">{t(M.colDevice)}</TableHead>
                <TableHead className="text-xs">{t(M.colOS)}</TableHead>
                <TableHead className="text-xs">{t(M.colFound)}</TableHead>
                <TableHead className="text-xs">{forbidden ? t(M.colVerdict) : t(M.colInstalled)}</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow
                  key={r.device_id}
                  onClick={() => navigate(`/devices/${r.device_id}`)}
                  className="cursor-pointer glass-hover"
                >
                  <TableCell className="px-4 py-3 font-medium text-sm text-foreground">{r.hostname}</TableCell>
                  <TableCell className="px-4 py-3 text-xs text-muted-foreground">{r.os || "—"}</TableCell>
                  <TableCell className="px-4 py-3 text-xs font-mono text-soft">
                    {r.installed
                      ? <>{r.matched_software}{r.matched_version && <span className="text-muted-foreground"> {r.matched_version}</span>}</>
                      : <span className="text-muted-foreground font-sans">—</span>}
                  </TableCell>
                  <TableCell className="px-4 py-3">
                    {forbidden ? (
                      <Badge variant={r.installed ? "destructive" : "success"}>
                        {r.installed ? "Fail" : "Pass"}
                      </Badge>
                    ) : (
                      <span className="text-xs text-muted-foreground">{r.installed ? t(M.yes) : t(M.no)}</span>
                    )}
                  </TableCell>
                  <TableCell className="px-4 py-3 w-8">
                    <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" strokeWidth={2} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
