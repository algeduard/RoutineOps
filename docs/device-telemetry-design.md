# Device Telemetry — дизайн (Этап 0)

Ветка: `feature/device-telemetry`. Модуль: `github.com/Floodww/RoutineOps`.

Две новые возможности по каждому устройству:

1. **Метрики ресурсов** (как Zabbix): CPU %, RAM used/total, загрузка системного
   диска %, сетевой трафик (bytes/s in/out) — сэмплируются во времени, хранятся
   как time-series, отдаются по REST, показываются в веб-карточке устройства
   (живые значения + исторические графики за 1ч/24ч).
2. **Аналитика активности приложений и времени за ПК**: foreground-приложение и
   активное-vs-простой время; агрегируются по приложениям и по дням; хранятся как
   суточные агрегаты; отчёт в вебе (топ приложений, активное время по дням). Это
   ЧУВСТВИТЕЛЬНЫЕ данные → сбор ВЫКЛЮЧЕН по умолчанию и включается точечно на
   устройство (privacy/consent, см. ниже).

Обе подсистемы следуют существующей архитектуре агент↔сервер (ADR-1/4/5):
device_id в сообщениях НЕ едет — сервер резолвит его из mTLS-серта; сервер НЕ
пушит агенту — агент сам собирает по таймеру и репортит (как inventory/security).

---

## 1. Потоки данных

### 1.1 Метрики ресурсов

```
agent (internal/agent/telemetry)
  ├─ ResourceSampler: раз в SampleInterval (дефолт 15с) снимает CPU/RAM/disk/net
  │    через gopsutil/v4 (cpu, mem, disk, net). Сетевой throughput = дельта
  │    счётчиков (rx/tx bytes) между двумя сэмплами, делённая на интервал.
  ├─ буферизует сэмплы в памяти (кольцо, потолок MetricsBatchMax)
  └─ раз в ReportInterval (дефолт 60с) шлёт батч unary-RPC ReportResourceMetrics
         │
         ▼
server gateway.ReportResourceMetrics
  ├─ extractCertInfo → fingerprint → device_id (анти-IDOR, ADR-1)
  ├─ clampAgentTime по каждому сэмплу (защита от кривых часов агента)
  └─ storage.InsertResourceMetrics → таблица device_metrics (bulk INSERT)
         │
         ▼
REST (read-only, viewer+): GET /devices/{id}/metrics?range=1h|24h  (история)
                           GET /devices/{id}/metrics/latest         (живое значение)
         │
         ▼
web DeviceDetail: секция «Ресурсы» — живые тайлы + SVG-графики истории (polling 10с)
```

Метрики допустимо ТЕРЯТЬ при обрыве (в отличие от прав/алертов): батч шлётся
прямым unary c ретраем на следующем тике, durable-очередь (outbox) НЕ
используется — буферизация промежуточных сэмплов бессмысленна, следующий тик даёт
свежие. При временном сбое сети буфер сохраняется до потолка и досылается.

### 1.2 Аналитика активности приложений

```
agent (internal/agent/telemetry)
  ├─ FetchTelemetryConfig (pull, как FetchPolicy): узнаёт, включён ли app-usage
  │    для этого устройства. Выключено (дефолт) → сэмплер активности НЕ работает.
  ├─ ActivitySampler (только при включённом флаге): раз в SampleInterval
  │    снимает foreground-приложение (foreground_windows.go) и idle-время ввода
  │    (GetLastInputInfo). active, если idle < IdleThreshold, иначе idle.
  ├─ агрегирует ДЕЛЬТЫ с прошлой отправки: per (day, app_name) foreground_seconds
  │    (считается только пока пользователь активен) и per day active/idle seconds.
  └─ раз в ReportInterval шлёт unary ReportAppUsage (дельты) и обнуляет их
         │
         ▼
server gateway.ReportAppUsage
  ├─ extractCertInfo → device_id
  ├─ gate: если app_usage_enabled=false для устройства → accept-and-drop (агент мог
  │    отставать от смены флага; данные не пишем)
  └─ storage.UpsertAppUsage: INSERT ... ON CONFLICT DO UPDATE
         foreground_seconds = existing + delta  (аккумуляция дельт)
     storage.UpsertDailyActivity: аналогично для active/idle секунд
         │
         ▼
REST (read-only, viewer+): GET /devices/{id}/app-usage?range=7d|30d
REST (it_admin, аудит):    PUT /devices/{id}/telemetry-config {app_usage_enabled}
         │
         ▼
web DeviceDetail: секция «Активность» — топ приложений (гориз. бары) + активное
                  время по дням; тумблер включения сбора (только it_admin)
```

Модель ДЕЛЬТ (а не абсолютных суток): при рестарте агента in-memory счётчик
сбрасывается; абсолютные значения регрессировали бы, а сервер-side аккумуляция
дельт (existing + delta) корректна. Потеря — только последнее неотправленное окно
(≤ ReportInterval).

---

## 2. Модель хранения (time-series на обычном Postgres)

TimescaleDB НЕ вводим (нет в стеке) — обычные таблицы + индексы (device_id, ts) +
периодическая ретенция чисткой старше N дней.

### migrations/032_device_metrics.sql
```
device_metrics(
  device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
  ts TIMESTAMPTZ NOT NULL,
  cpu_percent DOUBLE PRECISION,
  mem_used_bytes BIGINT, mem_total_bytes BIGINT,
  disk_percent DOUBLE PRECISION,
  net_rx_bps BIGINT, net_tx_bps BIGINT)
INDEX (device_id, ts DESC)
```
Сырые сэмплы (без пред-агрегации): объём умеренный (при 15с ≈ 5760 строк/сутки на
устройство), ретенция режет глубину. Диапазонные запросы фильтруют по (device_id,
ts) — покрыто индексом.

### migrations/033_device_app_usage.sql
```
device_app_usage(
  device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
  day DATE, app_name TEXT, foreground_seconds BIGINT DEFAULT 0,
  updated_at TIMESTAMPTZ, PRIMARY KEY(device_id, day, app_name))
INDEX (device_id, day)

device_activity_daily(
  device_id UUID REFERENCES devices(id) ON DELETE CASCADE,
  day DATE, active_seconds BIGINT DEFAULT 0, idle_seconds BIGINT DEFAULT 0,
  updated_at TIMESTAMPTZ, PRIMARY KEY(device_id, day))

devices.app_usage_enabled BOOLEAN NOT NULL DEFAULT false  -- privacy-тумблер
```
Агрегаты, а не события: суточные строки на приложение — компактно и достаточно для
отчёта «топ приложений / активное время по дням».

### Ретенция
Обе time-series-подсистемы чистятся в СУЩЕСТВУЮЩЕМ 24-часовом cleanup-воркере
(cmd/server/main.go, рядом с CleanupOldData): `CleanupOldData` расширен на
покрытие device_metrics (по ts) и app-usage-таблиц (по day) тем же
`DataRetentionDays` (дефолт 7 суток; переопределяется env DATA_RETENTION_DAYS).
Метрики короткоживущие → отдельный длинный срок не заводим.

---

## 3. Proto-контракт (аддитивно, ADR-4)

Только НОВЫЕ message и НОВЫЕ unary-RPC. Существующие поля/номера/Task не тронуты
(параллельная фича «удалённый рабочий стол» расширяет Task — конфликт исключён).
`buf breaking --against main` проходит (изменения аддитивны).

Новые сообщения: `ResourceSample`, `ResourceMetricsReport`, `ResourceMetricsAck`,
`AppUsageEntry`, `DailyActivity`, `AppUsageReport`, `AppUsageAck`,
`FetchTelemetryConfigRequest`, `FetchTelemetryConfigResponse`.

Новые RPC в `AgentService`:
- `ReportResourceMetrics(ResourceMetricsReport) → ResourceMetricsAck` — батч метрик.
- `ReportAppUsage(AppUsageReport) → AppUsageAck` — дельты активности.
- `FetchTelemetryConfig(FetchTelemetryConfigRequest) → FetchTelemetryConfigResponse`
  — pull privacy-флага app-usage + (опц.) серверный оверрайд интервала сэмплирования.

device_id ни в одном сообщении НЕ передаётся (ADR-1).

---

## 4. Приватность / согласие (app-usage)

Аналитика приложений и времени за ПК — чувствительные данные о поведении
сотрудника. Меры:

- **Выключено по умолчанию.** Столбец `devices.app_usage_enabled` = false, пока
  it_admin не включит явно. Агент не собирает foreground/idle, пока
  FetchTelemetryConfig не вернул `app_usage_enabled=true`.
- **Включение только it_admin + аудит.** `PUT /devices/{id}/telemetry-config` под
  `requireRole("it_admin")` пишет запись аудита `set_telemetry_config`
  (кто/когда/значение) — включение слежки прослеживается.
- **Метрики ресурсов ≠ app-usage.** CPU/RAM/диск/сеть — обезличенная нагрузка
  железа, собираются всегда (не про поведение человека). Аналитика приложений
  гейтится флагом отдельно.
- **Тиеринг чувствительности + отдельные opt-in гейты.** Базовая гранулярность —
  имя foreground-процесса (напр. `chrome.exe`). Более инвазивные данные собираются
  ТОЛЬКО при явных, независимых, по умолчанию ВЫКЛЮЧЕННЫХ серверных флагах (каждый —
  it_admin + аудит, как `app_usage_enabled`):
  - `capture_window_titles` (миграция 035) — заголовок активного окна (напр. вкладки
    браузера, `AppUsageEntry.window_title`, поле 4);
  - `capture_urls` (миграция 040) — полный URL активной вкладки браузера
    (`AppUsageEntry.url`, поле 5), читается через UI Automation. Собирается ЛИШЬ из
    известных браузеров (`isBrowserProcess`) и не из приватных/инкогнито-окон — по
    best-effort-детекту маркера в заголовке (`isPrivateBrowsing`; при обрезанном
    заголовке URL не читается — fail-closed). Чтение best-effort: его ошибка/пустота
    не ломает сбор имени процесса/заголовка/активности.
- **Инкогнито/приватные окна исключаются (best-effort).** Заголовок такого окна
  отбрасывается (`sanitizeTitle`→""), URL из него не читается — приватный просмотр
  трактуется как явный сигнал «истории не надо». Детект — по локализованному маркеру
  в заголовке (`isPrivateBrowsing`), покрыты основные локали Chromium/Edge/Firefox;
  для непокрытой локали окно может не распознаться, поэтому это НЕ единственная защита
  (сбор URL по умолчанию выключен, только браузеры, fail-closed при обрезке заголовка).
- **Двойной гейт на сервере.** Даже если агент отстал от смены флага, gateway
  повторно проверяет `app_usage_enabled` (и обнуляет `window_title`/`url` при
  выключенных `capture_window_titles`/`capture_urls`) — accept-and-drop, без ошибки
  агенту.
- **Ретенция.** Агрегаты чистятся по DataRetentionDays, как прочая телеметрия.

Дефолтный конфиг агента: `-telemetry-app-usage=false`. Флаг локально форсит выкл;
серверный флаг может только включать при локально разрешённом сборе (AND-семантика).

---

## 5. Границы платформ

- **Метрики ресурсов** — кросс-платформенно через gopsutil/v4 (Windows/macOS/Linux).
- **App-usage (foreground/idle)** — платформо-зависимо, нативно:
  - **Windows (этап 1):** `GetForegroundWindow` + `GetWindowThreadProcessId` +
    имя процесса по PID (gopsutil/process); idle — `GetLastInputInfo`.
  - **macOS/Linux:** заглушка `foreground_other.go` (build-тег `!windows`) —
    возвращает «не поддерживается», сэмплер активности не запускается. Расширение —
    отдельным этапом (как прочие платформо-специфичные фичи агента через `*_other.go`).

---

## 6. Этапы реализации

- **Этап 0** — этот дизайн-док.
- **Этап 1 — Метрики ресурсов (вертикальный срез):** proto (ReportResourceMetrics +
  FetchTelemetryConfig) → buf generate → agent (пакет telemetry: gopsutil-сэмплер +
  репортер, wire в runAgent) → storage (032 + telemetry.go) → gateway → REST
  (history/latest) → web (секция «Ресурсы»).
- **Этап 2 — App-usage:** proto (ReportAppUsage) → buf generate → agent
  (foreground_windows/other + агрегатор + репортер за флагом) → storage (033 +
  методы) → gateway → REST (app-usage + telemetry-config) → web (секция «Активность»).

---

## 7. Гейты качества

`go build ./...`, `go vet ./...`, `gofmt -l`, `buf breaking --against main`,
`cd web && npm run build`, кросс-компиляция агента (windows/linux amd64),
`go mod tidy` (gopsutil → прямая зависимость). Юнит-тесты: агрегация app-usage и
дельта сетевых счётчиков.

## 8. Известные ограничения / следующий шаг

- App-usage foreground/idle реализован только на Windows; macOS/Linux — заглушки.
- Метрики хранятся сырыми (без rollup-агрегации): при очень большом парке и долгой
  ретенции стоит добавить даунсэмплинг (например, 1-минутные средние в отдельную
  таблицу) — не входит в MVP.
- Метрики теряются при обрыве связи (durable-очередь намеренно не используется).
