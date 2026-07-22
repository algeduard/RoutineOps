import { useEffect, useRef, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Upload, Trash2 } from "lucide-react"
import api, {
  MigrationRosterRow,
  MigrationRosterEntry,
  MigrationSummary,
  MigrationRosterResponse,
  DEVICE_STATUS,
  DeviceStatus,
} from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from "@/components/ui/table"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import ConfirmDialog from "@/components/ConfirmDialog"
import { formatDistanceToNow } from "@/lib/time"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

// ── Разбор CSV ────────────────────────────────────────────────────────────────
// Вынесено и экспортировано ради юнит-теста (см. Migration.test.ts): парсер — самое
// хрупкое место (кавычки, запятые внутри значений, регистр заголовков из разных MDM).

// parseCSVRows — CSV-текст → массив записей (RFC4180-ish): кавычки, экранирование ""
// внутри кавычек, запятые внутри кавычек, CRLF/LF. Пустые строки выкидываются.
export function parseCSVRows(text: string): string[][] {
  const rows: string[][] = []
  let field = ""
  let row: string[] = []
  let inQuotes = false
  let started = false // хоть один символ/поле в текущей строке — чтобы отличить '' от конца файла
  const pushField = () => { row.push(field); field = ""; started = true }
  const pushRow = () => { pushField(); rows.push(row); row = []; started = false }
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (inQuotes) {
      if (c === '"') {
        if (text[i + 1] === '"') { field += '"'; i++; continue } // экранированная кавычка
        inQuotes = false; continue
      }
      field += c; continue
    }
    if (c === '"') { inQuotes = true; started = true; continue }
    if (c === ",") { pushField(); continue }
    // Переводы строк: CRLF (\r\n) — \r пропускаем, строку завершает \n; LF (\n) — завершает;
    // одиночный \r (classic-Mac CSV) — тоже завершает, иначе весь файл схлопнулся бы в строку.
    if (c === "\r") { if (text[i + 1] !== "\n") pushRow(); continue }
    if (c === "\n") { pushRow(); continue }
    field += c; started = true
  }
  if (started || field !== "") pushRow() // хвост без завершающего перевода строки
  // Полностью пустые строки (один пустой единственный столбец) — не данные.
  return rows.filter((r) => !(r.length === 1 && r[0].trim() === ""))
}

// Синонимы заголовков: экспорты из Intune / Kandji / Jamf / AD называют колонки по-разному.
// Голого "name" в hostname НЕТ намеренно: в user-центричных выгрузках (AD/Entra) колонка
// «Name» — это ФИО сотрудника, и жадный синоним молча уводил бы её в hostname, минуя
// проверку «нет колонки». Имя машины несут явные "computer name" / "device name" / "hostname".
const HEADER_SYNONYMS: Record<keyof MigrationRosterRow, string[]> = {
  hostname:      ["hostname", "host", "computer", "computer name", "computername", "device name", "devicename", "machine", "machine name"],
  serial_number: ["serial", "serial number", "serialnumber", "serial_number", "sn", "serial no", "service tag"],
  assigned_user: ["user", "assigned user", "assigned_user", "assigneduser", "owner", "primary user", "primaryuser", "username", "user name", "email", "user email"],
  asset_tag:     ["asset", "asset tag", "asset_tag", "assettag", "tag", "inventory", "inventory number", "inventory no"],
  group_hint:    ["group", "group_hint", "collection", "ou", "department", "dept", "org unit", "organizational unit"],
  notes:         ["notes", "note", "comment", "comments", "description", "remark", "remarks"],
}

// ParseError.code маппится страницей в человекочитаемое сообщение (i18n).
export class ParseError extends Error {
  constructor(public code: "empty" | "no-column") { super(code) }
}

// parseRosterCSV — CSV → строки ростера. Первая непустая строка = заголовок; колонки
// матчатся по синонимам (регистронезависимо). Если ни hostname, ни serial не нашлись —
// файл не про то (ParseError "no-column"): матчить машины будет не по чему. Строки, где
// и hostname, и serial пусты, отбрасываются (сервер их всё равно бы отбросил).
export function parseRosterCSV(text: string): MigrationRosterRow[] {
  const rows = parseCSVRows(text)
  if (rows.length === 0) throw new ParseError("empty")

  const header = rows[0].map((h) => h.trim().toLowerCase())
  const col: Partial<Record<keyof MigrationRosterRow, number>> = {}
  for (const key of Object.keys(HEADER_SYNONYMS) as (keyof MigrationRosterRow)[]) {
    const idx = header.findIndex((h) => HEADER_SYNONYMS[key].includes(h))
    if (idx >= 0) col[key] = idx
  }
  if (col.hostname === undefined && col.serial_number === undefined) {
    throw new ParseError("no-column")
  }

  const out: MigrationRosterRow[] = []
  for (let r = 1; r < rows.length; r++) {
    const cells = rows[r]
    const get = (k: keyof MigrationRosterRow): string => {
      const idx = col[k]
      return idx === undefined ? "" : (cells[idx] ?? "").trim()
    }
    const row: MigrationRosterRow = {
      hostname: get("hostname"),
      serial_number: get("serial_number"),
      assigned_user: get("assigned_user"),
      asset_tag: get("asset_tag"),
      group_hint: get("group_hint"),
      notes: get("notes"),
    }
    if (row.hostname === "" && row.serial_number === "") continue
    out.push(row)
  }
  return out
}

// ── Страница ──────────────────────────────────────────────────────────────────

const M = {
  title: { ru: "Миграция из MDM", en: "MDM migration" },
  subtitle: {
    ru: "Импортируйте список парка из старого MDM и отслеживайте, кто уже переехал. Ростер справочный: он не создаёт устройств и не выдаёт доступ — машины по-прежнему заезжают через энроллмент и очередь одобрения.",
    en: "Import your fleet list from the old MDM and track who has already migrated. The roster is advisory: it does not create devices or grant access — machines still arrive via enrollment and the approval queue.",
  },
  importTitle: { ru: "Импорт ростера", en: "Import roster" },
  batchLabel: { ru: "Метка партии", en: "Batch label" },
  batchPlaceholder: { ru: "напр. Intune 2026-07", en: "e.g. Intune 2026-07" },
  sourceLabel: { ru: "Откуда мигрируем", en: "Source MDM" },
  sourcePlaceholder: { ru: "напр. Intune", en: "e.g. Intune" },
  chooseFile: { ru: "Выбрать CSV", en: "Choose CSV" },
  columnsHint: {
    ru: "CSV с заголовком. Распознаются колонки: hostname/computer name, serial, user/owner, asset tag, group, notes. Нужна хотя бы одна из hostname или serial.",
    en: "CSV with a header row. Recognized columns: hostname/computer name, serial, user/owner, asset tag, group, notes. At least one of hostname or serial is required.",
  },
  parsed: { ru: "Распознано строк: {n}", en: "Rows parsed: {n}" },
  parseEmpty: { ru: "Файл пуст.", en: "The file is empty." },
  parseNoColumn: {
    ru: "Не нашлось колонки hostname или serial — по чему матчить машины? Проверьте заголовок CSV.",
    en: "No hostname or serial column found — what would machines match on? Check the CSV header.",
  },
  importBtn: { ru: "Импортировать", en: "Import" },
  importing: { ru: "Импорт...", en: "Importing..." },
  imported: { ru: "Импортировано новых строк: {n} из {total}", en: "Imported {n} new rows of {total}" },
  importFailed: { ru: "Импорт не удался", en: "Import failed" },
  progressTitle: { ru: "Прогресс миграции", en: "Migration progress" },
  arrived: { ru: "Приехали", en: "Arrived" },
  pending: { ru: "Ожидаются", en: "Pending" },
  total: { ru: "Всего", en: "Total" },
  clearBtn: { ru: "Очистить ростер", en: "Clear roster" },
  clearTitle: { ru: "Очистить весь ростер миграции?", en: "Clear the entire migration roster?" },
  clearDesc: {
    ru: "Будет удалено строк: {n}. Устройства это не затронет — уйдёт только справочный список. Восстановить можно повторным импортом CSV.",
    en: "Rows to delete: {n}. This does not affect devices — only the advisory list is removed. It can be restored by re-importing the CSV.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  emptyRoster: {
    ru: "Ростер пуст. Импортируйте CSV выгрузки из старого MDM, чтобы увидеть прогресс переезда.",
    en: "The roster is empty. Import a CSV export from your old MDM to see migration progress.",
  },
  colHost: { ru: "Имя", en: "Name" },
  colSerial: { ru: "Серийный номер", en: "Serial number" },
  colUser: { ru: "Сотрудник", en: "Assigned user" },
  colGroup: { ru: "Группа", en: "Group" },
  colSource: { ru: "Источник", en: "Source" },
  colState: { ru: "Статус", en: "State" },
  notArrived: { ru: "Не приехал", en: "Not arrived" },
  loadFailed: { ru: "Не удалось загрузить ростер", en: "Failed to load the roster" },
}

function pct(arrived: number, total: number): number {
  return total === 0 ? 0 : Math.round((arrived / total) * 100)
}

export default function Migration() {
  const t = useT()
  const navigate = useNavigate()
  const fileRef = useRef<HTMLInputElement>(null)

  const [entries, setEntries] = useState<MigrationRosterEntry[]>([])
  const [summary, setSummary] = useState<MigrationSummary>({ total: 0, arrived: 0, pending: 0 })
  const [loading, setLoading] = useState(true)
  const [loadFailed, setLoadFailed] = useState(false)

  const [batchLabel, setBatchLabel] = useState("")
  const [sourceMDM, setSourceMDM] = useState("")
  const [parsedRows, setParsedRows] = useState<MigrationRosterRow[] | null>(null)
  const [fileName, setFileName] = useState("")
  const [importing, setImporting] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)

  async function load() {
    try {
      const r = await api.get<MigrationRosterResponse>("/migration-roster")
      setEntries(r.data.entries ?? [])
      setSummary(r.data.summary ?? { total: 0, arrived: 0, pending: 0 })
      setLoadFailed(false)
    } catch {
      setLoadFailed(true)
      toast({ title: t(M.loadFailed), variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  // Устройства заезжают асинхронно (раскатка агента растянута во времени) — поллим, чтобы
  // прогресс обновлялся сам, как в очереди энроллмента.
  useEffect(() => {
    load()
    const iv = setInterval(load, 30_000)
    return () => clearInterval(iv)
  }, [])

  async function onFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setFileName(file.name)
    try {
      const text = await file.text()
      const rows = parseRosterCSV(text)
      setParsedRows(rows)
    } catch (err) {
      setParsedRows(null)
      const code = err instanceof ParseError ? err.code : "empty"
      toast({ title: code === "no-column" ? t(M.parseNoColumn) : t(M.parseEmpty), variant: "destructive" })
    }
  }

  async function doImport() {
    if (!parsedRows || parsedRows.length === 0) return
    setImporting(true)
    try {
      const r = await api.post<{ inserted: number; received: number }>("/migration-roster/import", {
        batch_label: batchLabel.trim(),
        source_mdm: sourceMDM.trim(),
        rows: parsedRows,
      })
      toast({ title: t(M.imported, { n: r.data.inserted, total: r.data.received }), variant: "success" })
      setParsedRows(null)
      setFileName("")
      if (fileRef.current) fileRef.current.value = ""
      await load()
    } catch {
      // авто-тост интерсептора
    } finally {
      setImporting(false)
    }
  }

  async function clearRoster() {
    try {
      await api.delete<{ deleted: number }>("/migration-roster?all=true")
      await load()
    } catch {
      // авто-тост интерсептора
    }
  }

  if (loading) {
    return <div className="flex items-center justify-center h-48 text-muted-foreground text-sm">{t(M.loading)}</div>
  }

  const progress = pct(summary.arrived, summary.total)

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <p className="text-sm text-muted-foreground max-w-3xl mt-1">{t(M.subtitle)}</p>
      </div>

      {/* Импорт */}
      <div className="glass px-5 py-4">
        <h2 className="text-[15px] font-semibold text-foreground mb-3">{t(M.importTitle)}</h2>
        <div className="grid gap-4 sm:grid-cols-2 max-w-2xl">
          <div className="space-y-1.5">
            <Label>{t(M.batchLabel)}</Label>
            <Input value={batchLabel} onChange={(e) => setBatchLabel(e.target.value)} placeholder={t(M.batchPlaceholder)} />
          </div>
          <div className="space-y-1.5">
            <Label>{t(M.sourceLabel)}</Label>
            <Input value={sourceMDM} onChange={(e) => setSourceMDM(e.target.value)} placeholder={t(M.sourcePlaceholder)} />
          </div>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <input ref={fileRef} type="file" accept=".csv,text/csv" onChange={onFile} className="hidden" />
          <Button variant="outline" size="sm" onClick={() => fileRef.current?.click()}>
            <Upload className="h-4 w-4 mr-1.5" />
            {t(M.chooseFile)}
          </Button>
          {fileName && <span className="text-xs text-muted-foreground font-mono">{fileName}</span>}
          {parsedRows && <span className="text-xs text-foreground">{t(M.parsed, { n: parsedRows.length })}</span>}
        </div>
        <p className="text-xs text-muted-foreground mt-2 max-w-2xl">{t(M.columnsHint)}</p>
        {parsedRows && parsedRows.length > 0 && (
          <Button className="mt-4" size="sm" onClick={doImport} disabled={importing}>
            {importing ? t(M.importing) : t(M.importBtn)}
          </Button>
        )}
      </div>

      {/* Прогресс */}
      {summary.total > 0 && (
        <div className="glass px-5 py-4">
          <div className="flex items-center justify-between gap-3">
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.progressTitle)}</h2>
            <Button variant="ghost" size="sm" className="text-muted-foreground hover:text-destructive" onClick={() => setConfirmClear(true)}>
              <Trash2 className="h-4 w-4 mr-1.5" />
              {t(M.clearBtn)}
            </Button>
          </div>
          <div className="mt-3 flex items-center gap-6 text-sm">
            <span><span className="text-2xl font-semibold text-emerald-600 dark:text-emerald-500">{summary.arrived}</span> <span className="text-muted-foreground">{t(M.arrived)}</span></span>
            <span><span className="text-2xl font-semibold text-amber-600 dark:text-amber-500">{summary.pending}</span> <span className="text-muted-foreground">{t(M.pending)}</span></span>
            <span><span className="text-2xl font-semibold text-foreground">{summary.total}</span> <span className="text-muted-foreground">{t(M.total)}</span></span>
          </div>
          <div className="mt-3 h-2 w-full rounded-full bg-muted overflow-hidden">
            <div className="h-full rounded-full bg-emerald-500 transition-all" style={{ width: `${progress}%` }} />
          </div>
        </div>
      )}

      {/* Таблица ростера */}
      <div className="glass overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="text-xs">{t(M.colHost)}</TableHead>
              <TableHead className="text-xs">{t(M.colSerial)}</TableHead>
              <TableHead className="text-xs">{t(M.colUser)}</TableHead>
              <TableHead className="text-xs">{t(M.colGroup)}</TableHead>
              <TableHead className="text-xs">{t(M.colSource)}</TableHead>
              <TableHead className="text-xs text-right">{t(M.colState)}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={6} className="text-center py-8 text-sm">
                  {loadFailed
                    ? <span className="text-destructive">{t(M.loadFailed)}</span>
                    : <span className="text-muted-foreground">{t(M.emptyRoster)}</span>}
                </TableCell>
              </TableRow>
            )}
            {entries.map((e) => {
              const arrived = e.matched_device_id !== ""
              const status = e.matched_status as DeviceStatus
              return (
                <TableRow key={e.id} className={arrived ? "glass-hover" : "bg-amber-500/[0.06] hover:bg-amber-500/10"}>
                  <TableCell className="px-4 py-3">
                    {arrived
                      ? <button type="button" className="text-sm font-medium text-foreground hover:underline text-left" onClick={() => navigate(`/devices/${e.matched_device_id}`)}>{e.hostname || "—"}</button>
                      : <span className="text-sm font-medium text-foreground">{e.hostname || "—"}</span>}
                  </TableCell>
                  <TableCell className="px-4 py-3 text-muted-foreground font-mono text-xs">{e.serial_number || "—"}</TableCell>
                  <TableCell className="px-4 py-3 text-muted-foreground text-xs">{e.assigned_user || "—"}</TableCell>
                  <TableCell className="px-4 py-3 text-muted-foreground text-xs">{e.group_hint || "—"}</TableCell>
                  <TableCell className="px-4 py-3 text-muted-foreground text-xs">{e.source_mdm || "—"}</TableCell>
                  <TableCell className="px-4 py-3 text-right">
                    {arrived
                      ? <Badge variant={DEVICE_STATUS[status]?.variant ?? "default"}>{DEVICE_STATUS[status]?.label ?? status}</Badge>
                      : <Badge variant="secondary">{t(M.notArrived)}</Badge>}
                    {arrived && e.matched_last_seen && (
                      <div className="text-[11px] text-muted-foreground mt-0.5">{formatDistanceToNow(e.matched_last_seen)}</div>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <ConfirmDialog
        open={confirmClear}
        onOpenChange={setConfirmClear}
        title={t(M.clearTitle)}
        description={t(M.clearDesc, { n: summary.total })}
        confirmLabel={t(M.clearBtn)}
        destructive
        onConfirm={clearRoster}
      />
    </div>
  )
}
