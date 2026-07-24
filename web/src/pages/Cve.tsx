import { useEffect, useRef, useState, type FormEvent } from "react"
import { useNavigate } from "react-router-dom"
import { Upload, Trash2, ScanSearch, ShieldAlert, RefreshCw } from "lucide-react"
import api, { CVEFinding, CVESummary, CVESeverity, CVEFeedSource, errStatus } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { Select } from "@/components/ui/select"
import { Label } from "@/components/ui/label"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

const M = {
  title: { ru: "Уязвимости ПО (CVE)", en: "Software vulnerabilities (CVE)" },
  intro: {
    ru: "Сопоставление инвентаря установленного ПО с фидом известных уязвимостей. Фид заливает администратор (выгрузка из NVD/OSV), скан пересобирает находки по всему парку. Матчинг по имени продукта (регистронезависимая подстрока) и версии — короткие имена могут пере-совпасть, задавайте специфичные названия продуктов.",
    en: "Matches the installed-software inventory against a feed of known vulnerabilities. An admin uploads the feed (an NVD/OSV export); a scan rebuilds findings across the fleet. Matching is by product name (case-insensitive substring) and version — short names may over-match, so use specific product names.",
  },
  unavailableTitle: { ru: "CVE-сканирование недоступно в этой редакции", en: "CVE scanning is not available in this edition" },
  unavailableBody: {
    ru: "Сканирование ПО на уязвимости — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Software vulnerability scanning is an Enterprise-edition feature. It requires an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить данные", en: "Failed to load data" },

  feedTitle: { ru: "Фид уязвимостей", en: "Vulnerability feed" },
  feedLoaded: { ru: "Записей в фиде: {n}", en: "Feed entries: {n}" },
  feedEmpty: { ru: "Фид пуст — загрузите выгрузку CVE, затем запустите скан.", en: "The feed is empty — upload a CVE export, then run a scan." },
  loadFeed: { ru: "Загрузить фид (JSON)", en: "Upload feed (JSON)" },
  clearFeed: { ru: "Очистить фид", en: "Clear feed" },
  feedHint: {
    ru: "JSON-массив записей: cve_id, product, version_constraint (пусто/\"*\" — любая версия; \"<2.0.0\"; \"=1.2.3\" или голое \"1.2.3\"), severity (low|medium|high|critical), cvss (опц.), summary, published_at. Загрузка ЗАМЕНЯЕТ фид целиком.",
    en: "JSON array of records: cve_id, product, version_constraint (empty/\"*\" — any version; \"<2.0.0\"; \"=1.2.3\" or bare \"1.2.3\"), severity (low|medium|high|critical), cvss (opt.), summary, published_at. Upload REPLACES the whole feed.",
  },
  feedLoadedToast: { ru: "Загружено записей: {n}", en: "Loaded {n} records" },
  feedBadJson: { ru: "Файл не является JSON-массивом записей фида", en: "The file is not a JSON array of feed records" },
  feedCleared: { ru: "Фид очищен", en: "Feed cleared" },
  clearFeedTitle: { ru: "Очистить фид уязвимостей?", en: "Clear the vulnerability feed?" },
  clearFeedDesc: {
    ru: "Будут удалены все записи фида. Находки исчезнут при следующем скане. Восстановить — повторной загрузкой выгрузки.",
    en: "All feed records will be deleted. Findings will disappear on the next scan. Restore by re-uploading an export.",
  },

  runScan: { ru: "Запустить скан", en: "Run scan" },
  scanning: { ru: "Сканирование...", en: "Scanning..." },
  scanDone: { ru: "Скан завершён: находок {n}", en: "Scan complete: {n} findings" },
  scanHintStale: { ru: "Фид изменён — запустите скан, чтобы обновить находки.", en: "The feed changed — run a scan to refresh findings." },

  sourceTitle: { ru: "Внешний источник фида", en: "External feed source" },
  sourceIntro: {
    ru: "Вместо ручной загрузки — автоматически тянуть фид с внешнего URL по расписанию. Ожидается тот же JSON-массив записей, что и при ручной загрузке (реальные выгрузки NVD/OSV приведите к нему прокси/скриптом). Синк ЗАМЕНЯЕТ фид целиком.",
    en: "Instead of manual upload — automatically pull the feed from an external URL on a schedule. The same JSON array of records as the manual upload is expected (map real NVD/OSV exports to it via a proxy/script). A sync REPLACES the whole feed.",
  },
  sourceEnabled: { ru: "Включить авто-синхронизацию", en: "Enable auto-sync" },
  sourceAutoScan: { ru: "Пересканировать после синка", en: "Rescan after sync" },
  sourceUrl: { ru: "URL источника (http/https)", en: "Source URL (http/https)" },
  sourceUrlPlaceholder: { ru: "https://feeds.example.com/cve.json", en: "https://feeds.example.com/cve.json" },
  sourceInterval: { ru: "Интервал синка (часы)", en: "Sync interval (hours)" },
  sourceSave: { ru: "Сохранить источник", en: "Save source" },
  sourceSaving: { ru: "Сохранение...", en: "Saving..." },
  sourceSaved: { ru: "Источник сохранён", en: "Source saved" },
  syncNow: { ru: "Синхронизировать сейчас", en: "Sync now" },
  syncing: { ru: "Синхронизация...", en: "Syncing..." },
  syncDone: { ru: "Синхронизация выполнена", en: "Sync complete" },
  sourceOn: { ru: "Авто-синк включён", en: "Auto-sync on" },
  sourceOff: { ru: "Авто-синк выключен", en: "Auto-sync off" },
  lastSync: { ru: "Последний синк: {when} — {status}", en: "Last sync: {when} — {status}" },
  lastSyncNever: { ru: "Синхронизаций ещё не было.", en: "No syncs yet." },
  sourceHint: {
    ru: "«Синхронизировать сейчас» использует СОХРАНЁННЫЙ источник (сохраните изменения перед синком). Внутренние адреса разрешены; размер ответа и таймаут ограничены.",
    en: "\"Sync now\" uses the SAVED source (save changes before syncing). Internal addresses are allowed; response size and timeout are capped.",
  },

  summaryTitle: { ru: "Сводка по парку", en: "Fleet summary" },
  totalFindings: { ru: "Находок", en: "Findings" },
  affectedDevices: { ru: "Устройств затронуто", en: "Affected devices" },

  findingsTitle: { ru: "Находки", en: "Findings" },
  filterSeverity: { ru: "Критичность", en: "Severity" },
  filterDevice: { ru: "Устройство", en: "Device" },
  all: { ru: "Все", en: "All" },
  reset: { ru: "Сбросить", en: "Reset" },
  noFindings: { ru: "Уязвимостей не найдено.", en: "No vulnerabilities found." },

  colDevice: { ru: "Устройство", en: "Device" },
  colProduct: { ru: "ПО", en: "Software" },
  colVersion: { ru: "Версия", en: "Version" },
  colCve: { ru: "CVE", en: "CVE" },
  colSeverity: { ru: "Критичность", en: "Severity" },
  colCvss: { ru: "CVSS", en: "CVSS" },
}

const SEV_LABEL: Record<CVESeverity, Msg> = {
  critical: { ru: "Критическая", en: "Critical" },
  high: { ru: "Высокая", en: "High" },
  medium: { ru: "Средняя", en: "Medium" },
  low: { ru: "Низкая", en: "Low" },
}

// Смысловые цвета критичности (полупрозрачный тинт держит стекло, как у Badge/CATEGORY_STYLE).
// critical/high читаются как тревога (red/orange), medium — amber, low — приглушённый.
const SEV_BADGE: Record<CVESeverity, string> = {
  critical: "border-red-500/20 bg-red-500/15 text-red-700 dark:border-red-400/25 dark:bg-red-400/15 dark:text-red-300",
  high: "border-orange-500/20 bg-orange-500/15 text-orange-700 dark:border-orange-400/25 dark:bg-orange-400/15 dark:text-orange-300",
  medium: "border-amber-500/20 bg-amber-500/15 text-amber-800 dark:border-amber-400/25 dark:bg-amber-400/15 dark:text-amber-300",
  low: "border-border bg-muted text-muted-foreground",
}

const SEV_ORDER: CVESeverity[] = ["critical", "high", "medium", "low"]

export default function Cve() {
  const t = useT()
  const navigate = useNavigate()
  const fileRef = useRef<HTMLInputElement>(null)

  const [summary, setSummary] = useState<CVESummary | null>(null)
  const [findings, setFindings] = useState<CVEFinding[]>([])
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)

  const [severity, setSeverity] = useState<"" | CVESeverity>("")
  const [deviceId, setDeviceId] = useState("")

  const [scanning, setScanning] = useState(false)
  const [loadingFeed, setLoadingFeed] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)
  // Фид меняли (залили/очистили) после последнего скана — находки могли устареть.
  const [feedStale, setFeedStale] = useState(false)

  // Внешний источник фида (авто-синк). Форма отражает сохранённый конфиг; синк использует
  // именно сохранённое состояние на сервере, а не несохранённые правки формы.
  const [source, setSource] = useState<CVEFeedSource | null>(null)
  const [srcUrl, setSrcUrl] = useState("")
  const [srcInterval, setSrcInterval] = useState(24)
  const [srcEnabled, setSrcEnabled] = useState(false)
  const [srcAutoScan, setSrcAutoScan] = useState(true)
  const [savingSource, setSavingSource] = useState(false)
  const [syncing, setSyncing] = useState(false)

  async function fetchFindings() {
    const params = new URLSearchParams()
    if (deviceId) params.set("device_id", deviceId)
    if (severity) params.set("severity", severity)
    const qs = params.toString()
    const r = await api.get<CVEFinding[]>(`/cve/findings${qs ? `?${qs}` : ""}`)
    setFindings(r.data ?? [])
  }

  async function fetchSummary() {
    const r = await api.get<CVESummary>("/cve/summary")
    setSummary(r.data)
  }

  // applySource синхронизирует форму с серверным конфигом источника.
  function applySource(s: CVEFeedSource) {
    setSource(s)
    setSrcUrl(s.url)
    setSrcInterval(s.sync_interval_hours)
    setSrcEnabled(s.enabled)
    setSrcAutoScan(s.auto_scan)
  }

  async function fetchFeedSource() {
    const r = await api.get<CVEFeedSource>("/cve/feed-source")
    applySource(r.data)
  }

  async function reloadAll() {
    setLoadError(false)
    try {
      await Promise.all([fetchSummary(), fetchFindings(), fetchFeedSource()])
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

  // Первичная загрузка.
  useEffect(() => {
    reloadAll()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Перезапрос находок при смене фильтров (сводка не зависит от фильтров).
  useEffect(() => {
    if (loading || unavailable) return
    fetchFindings().catch(() => {
      setLoadError(true)
      toast({ title: t(M.loadErr), variant: "destructive" })
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [severity, deviceId])

  async function runScan() {
    setScanning(true)
    try {
      const r = await api.post<{ findings: number }>("/cve/scan", {})
      setFeedStale(false)
      toast({ title: t(M.scanDone, { n: r.data.findings }), variant: "success" })
      await reloadAll()
    } catch {
      // авто-тост интерсептора
    } finally {
      setScanning(false)
    }
  }

  async function onFeedFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (fileRef.current) fileRef.current.value = "" // позволить повторный выбор того же файла
    if (!file) return
    setLoadingFeed(true)
    try {
      const parsed = JSON.parse(await file.text())
      if (!Array.isArray(parsed)) {
        toast({ title: t(M.feedBadJson), variant: "destructive" })
        return
      }
      const r = await api.post<{ loaded: number }>("/cve/feed", parsed)
      setFeedStale(true)
      toast({ title: t(M.feedLoadedToast, { n: r.data.loaded }), variant: "success" })
      await reloadAll()
    } catch (err) {
      // Ошибка парсинга JSON — своё сообщение; сетевые/серверные — авто-тост интерсептора.
      if (err instanceof SyntaxError) toast({ title: t(M.feedBadJson), variant: "destructive" })
    } finally {
      setLoadingFeed(false)
    }
  }

  async function clearFeed() {
    try {
      await api.delete("/cve/feed")
      setFeedStale(true)
      toast({ title: t(M.feedCleared), variant: "success" })
      await reloadAll()
    } catch {
      // авто-тост интерсептора
    }
  }

  async function saveSource(e: FormEvent) {
    e.preventDefault()
    setSavingSource(true)
    try {
      const r = await api.put<CVEFeedSource>("/cve/feed-source", {
        url: srcUrl.trim(),
        sync_interval_hours: srcInterval,
        enabled: srcEnabled,
        auto_scan: srcAutoScan,
      })
      applySource(r.data)
      toast({ title: t(M.sourceSaved), variant: "success" })
    } catch {
      // авто-тост интерсептора (400 «valid url» / 402 и т.п.)
    } finally {
      setSavingSource(false)
    }
  }

  async function syncNow() {
    setSyncing(true)
    try {
      const r = await api.post<CVEFeedSource>("/cve/feed-source/sync", {})
      applySource(r.data)
      // Успешный синк заменил фид; если auto_scan — он и пересканировал, иначе находки устарели.
      if (r.data.last_status.startsWith("ok")) setFeedStale(!r.data.auto_scan)
      toast({ title: t(M.syncDone), variant: "success" })
      await reloadAll()
    } catch {
      // авто-тост интерсептора
    } finally {
      setSyncing(false)
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

  const feedCount = summary?.feed_count ?? 0
  const sevMap = new Map(summary?.by_severity.map((s) => [s.severity, s.count]))

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <p className="text-sm text-muted-foreground max-w-3xl mt-1">{t(M.intro)}</p>
      </div>

      {loadError && (
        <div className="glass px-5 py-[18px] text-sm flex items-center gap-3">
          <p className="text-destructive">{t(M.loadErr)}</p>
          <Button variant="outline" size="sm" onClick={reloadAll}>{t(M.loading)}</Button>
        </div>
      )}

      {/* Фид + скан */}
      <div className="glass px-5 py-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-[15px] font-semibold text-foreground">{t(M.feedTitle)}</h2>
          <div className="flex flex-wrap items-center gap-2">
            <input ref={fileRef} type="file" accept=".json,application/json" onChange={onFeedFile} className="hidden" />
            <Button variant="outline" size="sm" onClick={() => fileRef.current?.click()} disabled={loadingFeed}>
              <Upload className="h-4 w-4 mr-1.5" />
              {t(M.loadFeed)}
            </Button>
            {feedCount > 0 && (
              <Button variant="ghost" size="sm" className="text-muted-foreground hover:text-destructive" onClick={() => setConfirmClear(true)}>
                <Trash2 className="h-4 w-4 mr-1.5" />
                {t(M.clearFeed)}
              </Button>
            )}
            <Button size="sm" onClick={runScan} disabled={scanning}>
              <ScanSearch className="h-4 w-4 mr-1.5" />
              {scanning ? t(M.scanning) : t(M.runScan)}
            </Button>
          </div>
        </div>
        <p className="text-sm text-soft">
          {feedCount > 0 ? t(M.feedLoaded, { n: feedCount }) : t(M.feedEmpty)}
        </p>
        {feedStale && (
          <p className="text-xs text-amber-700 dark:text-amber-400">{t(M.scanHintStale)}</p>
        )}
        <p className="text-xs text-muted-foreground max-w-3xl">{t(M.feedHint)}</p>
      </div>

      {/* Внешний источник фида (авто-синк по расписанию) */}
      <form onSubmit={saveSource} className="glass px-5 py-4 space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-[15px] font-semibold text-foreground">{t(M.sourceTitle)}</h2>
          <div className="flex items-center gap-2">
            {source?.enabled
              ? <Badge variant="success">{t(M.sourceOn)}</Badge>
              : <Badge variant="secondary">{t(M.sourceOff)}</Badge>}
            <Button type="button" variant="outline" size="sm" onClick={syncNow} disabled={syncing || !source?.url}>
              <RefreshCw className={`h-4 w-4 mr-1.5 ${syncing ? "animate-spin" : ""}`} />
              {syncing ? t(M.syncing) : t(M.syncNow)}
            </Button>
          </div>
        </div>
        <p className="text-xs text-muted-foreground max-w-3xl">{t(M.sourceIntro)}</p>

        <p className="text-sm text-soft">
          {source?.last_synced_at
            ? t(M.lastSync, { when: new Date(source.last_synced_at).toLocaleString(), status: source.last_status })
            : t(M.lastSyncNever)}
        </p>

        <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={srcEnabled} onChange={(e) => setSrcEnabled(e.target.checked)} />
            <span className="text-foreground">{t(M.sourceEnabled)}</span>
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={srcAutoScan} onChange={(e) => setSrcAutoScan(e.target.checked)} />
            <span className="text-foreground">{t(M.sourceAutoScan)}</span>
          </label>
        </div>

        <div className="flex flex-wrap gap-3">
          <div className="space-y-1.5 grow min-w-64">
            <Label htmlFor="cve-src-url" className="text-soft">{t(M.sourceUrl)}</Label>
            <Input id="cve-src-url" value={srcUrl} onChange={(e) => setSrcUrl(e.target.value)} placeholder={t(M.sourceUrlPlaceholder)} />
          </div>
          <div className="space-y-1.5 w-36">
            <Label htmlFor="cve-src-interval" className="text-soft">{t(M.sourceInterval)}</Label>
            <Input
              id="cve-src-interval"
              type="number"
              min={1}
              max={720}
              value={srcInterval}
              onChange={(e) => setSrcInterval(Number(e.target.value) || 1)}
            />
          </div>
        </div>

        <p className="text-xs text-muted-foreground max-w-3xl">{t(M.sourceHint)}</p>
        <Button type="submit" size="sm" disabled={savingSource || (srcEnabled && !srcUrl.trim())}>
          {savingSource ? t(M.sourceSaving) : t(M.sourceSave)}
        </Button>
      </form>

      {/* Сводка */}
      <div className="glass px-5 py-4">
        <h2 className="text-[15px] font-semibold text-foreground mb-3">{t(M.summaryTitle)}</h2>
        <div className="flex flex-wrap items-center gap-6 text-sm">
          <span>
            <span className="text-2xl font-semibold text-foreground">{summary?.total_findings ?? 0}</span>{" "}
            <span className="text-muted-foreground">{t(M.totalFindings)}</span>
          </span>
          <span>
            <span className="text-2xl font-semibold text-foreground">{summary?.affected_devices ?? 0}</span>{" "}
            <span className="text-muted-foreground">{t(M.affectedDevices)}</span>
          </span>
          <div className="flex flex-wrap items-center gap-2">
            {SEV_ORDER.map((s) => (
              <Badge key={s} variant="outline" className={SEV_BADGE[s]}>
                {t(SEV_LABEL[s])}: {sevMap.get(s) ?? 0}
              </Badge>
            ))}
          </div>
        </div>
      </div>

      {/* Фильтры + таблица находок */}
      <div className="glass px-5 py-[18px] flex flex-wrap items-end gap-3">
        <div className="space-y-1 min-w-40">
          <Label className="text-xs text-muted-foreground">{t(M.filterSeverity)}</Label>
          <Select
            value={severity}
            onChange={(v) => setSeverity(v as "" | CVESeverity)}
            options={[{ value: "", label: t(M.all) }, ...SEV_ORDER.map((s) => ({ value: s, label: t(SEV_LABEL[s]) }))]}
          />
        </div>
        <div className="space-y-1 min-w-56">
          <Label className="text-xs text-muted-foreground">{t(M.filterDevice)}</Label>
          <Select
            value={deviceId}
            onChange={setDeviceId}
            options={[
              { value: "", label: t(M.all) },
              ...(summary?.by_device ?? []).map((d) => ({ value: d.device_id, label: `${d.hostname || d.device_id.slice(0, 8)} (${d.count})` })),
            ]}
          />
        </div>
        {(severity || deviceId) && (
          <button
            type="button"
            onClick={() => { setSeverity(""); setDeviceId("") }}
            className="h-9 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            {t(M.reset)}
          </button>
        )}
      </div>

      <div className="glass overflow-hidden">
        <div className="px-5 pt-4 pb-3">
          <h2 className="text-[15px] font-semibold text-foreground">{t(M.findingsTitle)}</h2>
        </div>
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>{t(M.colDevice)}</TableHead>
              <TableHead>{t(M.colProduct)}</TableHead>
              <TableHead>{t(M.colVersion)}</TableHead>
              <TableHead>{t(M.colCve)}</TableHead>
              <TableHead>{t(M.colSeverity)}</TableHead>
              <TableHead className="text-right">{t(M.colCvss)}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {findings.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={6} className="text-center py-8 text-sm text-muted-foreground">
                  <ShieldAlert className="h-5 w-5 mx-auto mb-1.5 text-muted-foreground/60" />
                  {t(M.noFindings)}
                </TableCell>
              </TableRow>
            )}
            {findings.map((f) => (
              <TableRow key={f.id}>
                <TableCell>
                  <button
                    type="button"
                    className="text-sm font-medium text-foreground hover:underline text-left"
                    onClick={() => navigate(`/devices/${f.device_id}`)}
                  >
                    {f.hostname || f.device_id.slice(0, 8)}
                  </button>
                </TableCell>
                <TableCell className="text-sm text-foreground">{f.product}</TableCell>
                <TableCell className="text-xs font-mono text-muted-foreground">{f.installed_version || "—"}</TableCell>
                <TableCell className="text-xs font-mono">
                  <a
                    href={`https://nvd.nist.gov/vuln/detail/${encodeURIComponent(f.cve_id)}`}
                    target="_blank"
                    rel="noreferrer"
                    className="text-brand hover:underline"
                  >
                    {f.cve_id}
                  </a>
                </TableCell>
                <TableCell>
                  <Badge variant="outline" className={SEV_BADGE[f.severity]}>{t(SEV_LABEL[f.severity])}</Badge>
                </TableCell>
                <TableCell className="text-right text-xs font-mono text-muted-foreground">
                  {f.cvss != null ? f.cvss.toFixed(1) : "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <ConfirmDialog
        open={confirmClear}
        onOpenChange={setConfirmClear}
        title={t(M.clearFeedTitle)}
        description={t(M.clearFeedDesc)}
        confirmLabel={t(M.clearFeed)}
        destructive
        onConfirm={clearFeed}
      />
    </div>
  )
}
