# Дизайн: Удалённый рабочий стол (Remote Desktop)

Статус: черновик реализации (feature/remote-desktop). Автор-ветка форка algeduard.

Позволяет IT-администратору из веб-интерфейса подключиться к рабочему столу
управляемого устройства: видеть экран в реальном времени и (этап 2) управлять
мышью/клавиатурой. Первая платформа — **Windows**; macOS/Linux — заглушки по
build-тегам (как у остальных платформенных фич).

---

## 1. Ограничения архитектуры, из которых всё следует

Эти факты определяют форму решения (см. `ARCHITECTURE.md`, `proto/agent.proto`):

1. **Агент за NAT, сервер к агенту не дозванивается.** В `AgentService` только один
   bidi-стрим `Connect` (heartbeat↑ / `Task`↓); все прочие RPC — unary и
   инициируются агентом. Значит поток «экран↔ввод» должен открывать **агент**, а
   сервер — проксировать его в браузер администратора.
2. **Нельзя смешивать потоки (ADR-5).** `Connect`-стрим — только «я живой» + доставка
   команд. Буфер канала задач в `registry` — 16, отправка неблокирующая (дропает при
   переполнении). Гнать через него видео нельзя. → **отдельный bidi-RPC**
   `RemoteDesktop`.
3. **Служба агента живёт в session 0** и не имеет доступа к экрану/вводу интерактивной
   сессии. Захват экрана и `SendInput` возможны только в процессе, запущенном в
   активной консольной сессии → используем существующий
   `winsession.LaunchInActiveSession` (`CreateProcessAsUser`, desktop `winsta0\default`),
   тем же механизмом, что и `agent lock-screen`.
4. **Идентичность устройства — только mTLS-сертификат (ADR-1).** device_id в теле
   сообщений не передаётся; сервер берёт его из отпечатка серта. Процесс-хелпер
   захвата переиспользует те же серт/ключ, что и служба, и открывает свой gRPC-стрим —
   сервер увидит тот же fingerprint = то же устройство.
5. **Контракт расширяется только аддитивно (ADR-4, CI `buf breaking`).** Номера полей
   не менять; новые поля — в конец; новые message/RPC — можно.
6. **WebSocket в стеке нет** (ни в Go, ни во фронте, ни в nginx). Всё добавляется с
   нуля: WS-эндпоинт в chi под `jwtMiddleware`+`requireRole("it_admin")`, WS-клиент во
   фронте, upgrade-заголовки в nginx.
7. **Захвата экрана / SendInput в коде нет вообще** — реализуются с нуля (GDI `BitBlt`
   + `image/jpeg` для MVP; `SendInput` через `syscall.NewLazyDLL`, стиль — как
   `lockui_windows.go`).

---

## 2. Топология сессии

```
 Браузер админа                Сервер (routineops-server)                 Устройство
 ┌────────────┐   WSS /api/v1  ┌─────────────────────────────┐  gRPC/mTLS  ┌──────────────────────┐
 │ RemoteDesk │◀──────────────▶│  api: WS-хендлер            │◀───Connect──│ служба агента (sess 0)│
 │ .tsx       │  кадры JPEG ↓  │        │                    │   Task↓     │   Executor.handle     │
 │ <canvas>   │  ввод ↑        │  session bridge (по sid)    │             │        │ launch        │
 └────────────┘                │        │                    │             │        ▼               │
                               │  gateway: RemoteDesktop     │  RemoteDesktop (bidi, отдельный)     │
                               │           bidi-стрим        │◀───────────▶│ хелпер `remote-desktop`│
                               └─────────────────────────────┘  frames↑    │  (интерактивная сессия)│
                                                                 input↓     │  GDI capture + SendInput│
                                                                            └──────────────────────┘
```

### Хореография запуска
1. Админ на странице устройства жмёт «Удалённый рабочий стол». Фронт открывает
   `wss://<host>/api/v1/devices/{id}/remote-desktop`.
2. WS-хендлер (под `requireRole("it_admin")`) проверяет, что устройство онлайн
   (`registry.Connected`), генерирует **`session_id`** (uuid), регистрирует пустой
   мост `sessions[session_id] = {ws, agentStream:nil, ...}` и через
   `registry.Send(deviceCN, Task{remote_desktop:{session_id, action:START}})` шлёт
   команду агенту по `Connect`-стриму. Пишет аудит `remote_desktop_start`.
3. Служба агента получает `Task` с `RemoteDesktopCommand` → `Executor.handle` видит
   `task.GetRemoteDesktop() != nil` → (этап согласия, см. §5) → запускает
   `winsession.LaunchInActiveSession(exe, ["remote-desktop", "-session", sid, ...])`.
4. Хелпер в интерактивной сессии дозванивается до сервера (тот же mTLS-серт), открывает
   `RemoteDesktop`-стрим, первым сообщением шлёт `Hello{session_id}`.
5. Сервер по `session_id` находит ожидающий мост, связывает `agentStream` с `ws`.
   С этого момента:
   - кадры `VideoFrame` (агент→сервер) ретранслируются в WS (сервер→браузер);
   - события `InputEvent` (браузер→WS→сервер) ретранслируются в стрим (сервер→агент).
6. Завершение: закрытие WS или стрима, `Control{STOP}`, либо таймаут неактивности →
   сервер рвёт обе стороны, хелпер завершается, пишется аудит `remote_desktop_end`.

**Почему session_id, а не только fingerprint:** к одному устройству теоретически может
идти несколько сессий (разные админы) — sid однозначно связывает конкретный WS с
конкретным стримом хелпера. sid генерит сервер и передаёт в команде; хелпер лишь
возвращает его в `Hello`.

**Безопасность связывания:** `Hello.session_id` должен совпасть с ранее выданным
сервером sid для ЭТОГО устройства (fingerprint из mTLS). Сервер проверяет, что sid
принадлежит мосту, чей `deviceID == fingerprint-derived deviceID`. Чужой агент не может
подключиться к чужой сессии, т.к. sid привязан к device_id при создании.

---

## 3. Контракт proto (аддитивно)

```proto
// В message Task — новое поле 8 (следующий свободный номер).
//   RemoteDesktopCommand remote_desktop = 8; // задано ⇒ команда remote desktop
message RemoteDesktopCommand {
  string session_id = 1;                 // выдан сервером, хелпер вернёт его в Hello
  RemoteDesktopAction action = 2;        // START / STOP
  // поля качества/тайлинга — на будущее (fps, scale, quality)
}
enum RemoteDesktopAction {
  REMOTE_DESKTOP_ACTION_UNSPECIFIED = 0;
  REMOTE_DESKTOP_ACTION_START = 1;
  REMOTE_DESKTOP_ACTION_STOP  = 2;
}

// Новый bidi-RPC (открывает АГЕНТ-хелпер):
//   rpc RemoteDesktop(stream RemoteDesktopClientMsg) returns (stream RemoteDesktopServerMsg);

// Агент-хелпер → Сервер
message RemoteDesktopClientMsg {
  oneof payload {
    RDHello       hello  = 1;  // первым сообщением: связывание по session_id
    RDVideoFrame  frame  = 2;  // кадр экрана
    RDStatus      status = 3;  // ошибки/события (напр. отказ пользователя)
  }
}
message RDHello   { string session_id = 1; int32 screen_width = 2; int32 screen_height = 3; }
message RDVideoFrame {
  int64  seq        = 1;
  int64  ts_unix_ms = 2;
  RDImageFormat format = 3; // JPEG на MVP
  int32  width      = 4;
  int32  height     = 5;
  bytes  data       = 6;    // полный кадр (MVP); тайлы/дельты — позже
  bool   key_frame  = 7;
}
enum RDImageFormat { RD_IMAGE_FORMAT_UNSPECIFIED = 0; RD_IMAGE_FORMAT_JPEG = 1; }
message RDStatus  { RDStatusCode code = 1; string message = 2; }
enum RDStatusCode {
  RD_STATUS_CODE_UNSPECIFIED = 0;
  RD_STATUS_CODE_READY       = 1;
  RD_STATUS_CODE_USER_DENIED = 2; // пользователь отклонил согласие
  RD_STATUS_CODE_ERROR       = 3;
}

// Сервер → Агент-хелпер
message RemoteDesktopServerMsg {
  oneof payload {
    RDInputEvent input   = 1;  // мышь/клавиатура (этап 2)
    RDControl    control = 2;  // STOP / смена качества
  }
}
message RDInputEvent {
  RDInputType type = 1;
  // мышь: нормализованные координаты 0..1 (устойчивы к масштабу кадра)
  double x = 2; double y = 3;
  int32  button = 4;        // 0=left,1=right,2=middle
  int32  wheel_delta = 5;
  // клавиатура: код и модификаторы
  int32  key_code = 6;      // Windows virtual-key
  bool   key_down = 7;
  bool   ctrl = 8; bool alt = 9; bool shift = 10; bool meta = 11;
}
enum RDInputType {
  RD_INPUT_TYPE_UNSPECIFIED = 0;
  RD_INPUT_TYPE_MOUSE_MOVE  = 1;
  RD_INPUT_TYPE_MOUSE_DOWN  = 2;
  RD_INPUT_TYPE_MOUSE_UP    = 3;
  RD_INPUT_TYPE_WHEEL       = 4;
  RD_INPUT_TYPE_KEY         = 5;
}
message RDControl { RDControlAction action = 1; int32 fps = 2; int32 quality = 3; }
enum RDControlAction {
  RD_CONTROL_ACTION_UNSPECIFIED = 0;
  RD_CONTROL_ACTION_STOP = 1;
  RD_CONTROL_ACTION_SET_QUALITY = 2;
}
```

Регенерация: `buf generate` (protoc не требуется — плагины protoc-gen-go/-grpc + buf).
Проверка: `buf breaking --against '.git#branch=main'`.

**Замечание о слиянии с веткой телеметрии:** параллельная фича `feature/device-telemetry`
тоже начинает нумерацию новых сущностей независимо, но НЕ трогает `Task` — коллизии по
полю 8 не будет. Новые RPC у обеих фич разные.

---

## 4. Серверная часть

### 4.1. Мост сессий (новый пакет `internal/server/remotedesktop`)
- `type Bridge struct { sessions map[string]*session; mu sync.Mutex }`.
- `session{ deviceID string; ws *wsConn; agent chan *pb.RemoteDesktopServerMsg;
  frames chan *pb.RDVideoFrame; done chan struct{} }`.
- API: `NewSession(deviceID) (sid string)`, `AttachAgent(sid, deviceID, stream)`,
  `AttachWS(sid, ws)`, `Close(sid)`. Оба конца ретранслируют в память (без БД).
- Backpressure: если браузер не успевает — сервер ДРОПАЕТ старые кадры (video-friendly:
  показываем свежий, не копим лаг). Ввод не дропаем (маленький, важный порядок).

### 4.2. gRPC-хендлер `func (g *Gateway) RemoteDesktop(stream pb.AgentService_RemoteDesktopServer) error`
- `extractCertInfo(stream.Context())` → deviceID/fingerprint.
- Первое `Recv()` обязано быть `Hello`; `bridge.AttachAgent(hello.session_id, deviceID, stream)`.
  Если sid неизвестен/чужой → `codes.NotFound`/`PermissionDenied`.
- Далее две горутины: Recv (кадры/статус → мост→WS), Send (ввод/контроль из моста→агент).

### 4.3. WebSocket-хендлер (chi, `internal/server/api/remotedesktop_handler.go`)
- Роут в RBAC-группе: `r.Get("/devices/{id}/remote-desktop", h.remoteDesktopWS)` под
  `requireRole("it_admin")` (и `requireHuman`, чтобы отсечь сервисные токены).
- Проверка: устройство существует, онлайн (`registry.Connected(cn)`); иначе 409.
- `NewSession(deviceID)` → `registry.Send(cn, Task{remote_desktop:START,sid})`.
- Upgrade соединения (библиотека — `github.com/coder/websocket`, минимальная,
  context-first; добавить в go.mod). `bridge.AttachWS(sid, ws)`.
- Мост: горутина «кадры→WS binary» + горутина «WS→InputEvent». Первый кадр — после
  `Hello` от агента (или таймаут ~15с → закрыть с сообщением «агент не поднял сессию»).
- Аудит: `h.audit(ctx, userID, email, "remote_desktop_start", "device", id, {session_id})`
  при старте и `remote_desktop_end` (с длительностью) при закрытии.

### 4.4. Протокол WS (браузер↔сервер)
- Сервер→браузер: **binary**-фреймы = сырой JPEG кадра (плюс маленький текстовый
  control-канал JSON для метаданных «размер экрана», «сессия готова/закрыта»). Проще:
  первый текстовый JSON `{"type":"ready","w":..,"h":..}`, затем binary-кадры.
- Браузер→сервер: **text** JSON события ввода
  `{"t":"mouse_move","x":0.42,"y":0.7}` и т.п. Сервер мапит в `RDInputEvent`.

### 4.5. nginx (`web/nginx.prod.conf`)
Добавить для WS в `location /api/`:
```
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection $connection_upgrade;   # map $http_upgrade → upgrade/close
proxy_read_timeout 3600s;
```
CSP уже `connect-src 'self'` — для same-origin `wss://` достаточно.

---

## 5. Согласие пользователя (приватность)

Удалённый доступ к живому экрану сотрудника — чувствительная операция. Политика по
умолчанию (конфигурируемо в `system_settings`/агентском конфиге):
- **attended (по умолчанию):** при `START` хелпер показывает в юзер-сессии
  запрос-оверлей (walk, как lockui) «IT запрашивает удалённый доступ. Разрешить?» с
  таймаутом. Отказ → `RDStatus{USER_DENIED}` → сервер закрывает WS с понятным
  сообщением, пишет аудит.
- **unattended (для серверов/киосков):** админ-настройка на устройство/группу,
  разрешающая доступ без запроса; всё равно показывается неблокирующая плашка «идёт
  удалённый сеанс» + запись в аудит. Для MVP допустимо начать с unattended+баннер и
  добавить attended-промпт следующим шагом, НО баннер «идёт сеанс» и аудит —
  обязательны с самого начала.

Всё логируется в `audit_log` (старт/стоп/актор/длительность/отказ).

---

## 6. Агент

### 6.1. Диспетч команды (служба, `internal/agent/command/executor.go`)
В `Executor.handle`, рядом с `GetLock()/GetDecommission()`:
```
if rd := task.GetRemoteDesktop(); rd != nil { return e.handleRemoteDesktop(rd) }
```
`handleRemoteDesktop`: на START — `winsession.LaunchInActiveSession(exe,
["remote-desktop","-session",sid])` (Windows); на других ОС — `ReportTaskResult` с
ошибкой «не поддерживается». Дедуп по sid (не плодить хелперы).

### 6.2. Хелпер `remote-desktop` (новая подкоманда, `cmd/agent/main.go`)
`case "remote-desktop": remotedesktop.RunHelper(cfg, sid, log)` (по образцу `lock-screen`).
Пакет `internal/agent/remotedesktop`:
- `RunHelper`: dial сервера (buildDialer, тот же серт) → `client.RemoteDesktop(ctx)` →
  `Send(Hello{sid, w, h})` → цикл захвата.
- Захват (Windows, `capture_windows.go`): GDI `GetDC(0)`/`CreateCompatibleDC`/`BitBlt`
  всего виртуального экрана → `GetDIBits` → `image.RGBA` → `image/jpeg` (quality ~50,
  downscale до ~1600px по ширине) → `RDVideoFrame`. Тикер ~8–10 fps. При неизменном
  кадре (хеш) — пропуск/replace (экономия).
- Ввод (этап 2, `input_windows.go`): приём `RDInputEvent` → `SendInput`
  (MOUSEINPUT/KEYBDINPUT) через `syscall.NewLazyDLL("user32.dll")`; координаты
  денормализуются в абсолютные (`SetCursorPos`/absolute mouse flags по SM_CXVIRTUALSCREEN).
- Другие платформы: `capture_other.go`/`input_other.go` — заглушки (build-теги).
- Завершение: по `Control{STOP}`, разрыву стрима или закрытию окна-баннера.

### 6.3. Зависимости
- MVP-захват — чистый GDI через `syscall`/`lxn/win` (в go.mod уже есть) + stdlib
  `image/jpeg`. Без новых внешних зависимостей на агенте. (DXGI Desktop Duplication —
  оптимизация следующего этапа.)

---

## 7. Веб

- Новая страница `web/src/pages/RemoteDesktop.tsx`, роут `devices/:id/remote-desktop`
  (в приватной зоне; действие только для админа — кнопка в `DeviceDetail.tsx`
  под `{isAdmin && ...}` в дропдауне действий, рядом с «Заблокировать экран»).
- WS-клиент: нативный `new WebSocket(\`wss://${location.host}/api/v1/devices/${id}/remote-desktop\`)`
  (cookie `token` уедет автоматически при same-origin). `binaryType='arraybuffer'`.
- Рендер: `<canvas>`; на каждый binary-кадр — `createImageBitmap(blob)` → `drawImage`.
  Первый JSON `ready` задаёт размер canvas.
- Ввод (этап 2): слушатели `mousemove/mousedown/mouseup/wheel/keydown/keyup` на canvas;
  координаты нормализуются к 0..1 по `getBoundingClientRect`; шлём JSON в WS. Троттлинг
  `mousemove` (~30–60 Гц).
- Состояния: «подключение», «ожидание согласия пользователя», «активна», «отказано»,
  «устройство офлайн», «сессия завершена».
- Индикатор качества/fps и кнопки «стоп», «полный экран», позже «Ctrl+Alt+Del»
  (спец-последовательность через сервер).

---

## 8. Этапы реализации

- **Этап 1 (MVP просмотра):** proto (Task поле 8 + RemoteDesktop RPC + message) →
  `buf generate` → сервер (мост + gRPC-хендлер + WS-эндпоинт + registry START + аудит +
  nginx WS) → агент (диспетч START + хелпер + GDI-захват + Hello/кадры) → web
  (страница-вьювер, canvas). Только просмотр. Баннер «идёт сеанс» + аудит обязательны.
- **Этап 2 (управление):** `RDInputEvent` end-to-end: web-слушатели → WS → сервер →
  стрим → `SendInput`. Attended-согласие (оверлей-промпт).
- **Этап 3 (качество/масштаб):** дельта/тайлы вместо полного кадра, DXGI Desktop
  Duplication, адаптивный fps/битрейт, буфер обмена, мультимонитор-выбор.

## 9. Гейты качества (для каждого PR)
`go build ./...`, `go vet ./...`, `gofmt -l` пусто, `go test ./... -race`,
кросс-компиляция агента (win/mac/linux), `buf breaking` против main, `web: npm run
build`. Хелпер-захват Windows минимально проверяется кросс-компиляцией (GOOS=windows) —
рантайм-проверка на реальной Windows-машине.
