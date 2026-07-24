# Мультитенантность: per-query scoping данных

Как RoutineOps изолирует данные между тенантами (арендаторами). Дополняет
мультитенантность-MVP (модель `tenants` + привязка `tenant_id`, миграция 050).

## Модель доступа

- **Default-тенант — «провайдер»** (фиксированный id `00000000-…-0001`). Акторы в нём
  **не скоупятся** и видят/управляют всеми тенантами (провижининг, назначение
  устройств/юзеров, кросс-тенантные отчёты). Провайдер нужен, потому что назначение
  сущностей тенантам (`AssignDeviceTenant`/`AssignUserTenant`) и управление самими
  тенантами по определению кросс-тенантные операции.
- **Не-Default тенант — изолирован.** Актор такого тенанта читает только свои строки,
  а мутация чужой строки по id её «не находит» (0 строк → 404) — о чужих сущностях он
  даже не узнаёт.
- **Обратная совместимость.** В одно-организационном (single-org) деплое ВСЕ устройства,
  пользователи и сессии лежат в Default → scoping выключен → поведение ровно как до
  фичи. Изоляция «включается» только когда деплойер заводит НЕ-Default тенанты и
  переносит в них сущности/юзеров.
- **Управление тенантами — только провайдер.** CRUD тенантов и назначение сущностей
  (`TenantsRoutes`) закрыты `requireProvider` (403 для скоупленного не-Default актора):
  это кросс-тенантные операции. Без гарда скоупленный it_admin мог бы
  `POST /tenants/{DefaultTenantID}/assign` со своим id и самоповыситься до провайдера.
  `requireRole("it_admin")` этого не ловит — он про роль, а не про тенант актора.

## Механизм (шов)

`internal/server/storage/tenant_scope.go`:

- `jwtMiddleware` резолвит `tenant_id` актора (`GetUserTenantID`, для сервисного токена —
  тенант создавшего админа) и кладёт его в контекст запроса через
  `storage.WithTenantScope(ctx, tenantID)`. Резолв **per-request из БД** — переназначение
  тенанта действует сразу, без релогина.
- Tenant-aware storage-методы читают scope из ctx (`scopeParam`) и подмешивают единый
  предикат:

  ```sql
  AND ($k::uuid IS NULL OR <col> = $k::uuid)
  ```

  где `$k` = `nil` для провайдера (матчит ВСЕ тенанты) или `tenant_id` изолированного
  тенанта. Запрос структурно одинаков при любом режиме — планировщик коротко замыкает
  `NULL IS NULL`.
- **Различение вызывающих — автоматическое.** Scope ставится ТОЛЬКО в `jwtMiddleware`
  (человек/сервисный токен). Агент-mTLS, gateway, фоновые воркеры и cron идут БЕЗ scope
  → `scopeParam=nil` → провайдер (нескоуплено). Поэтому правится только SQL нужных
  методов, без изменения call-sites и без различения типа вызывающего в Go.
- **RLS не используется:** pgxpool переиспользует соединения без транзакции на запрос, и
  `SET LOCAL app.tenant` не пережил бы границу запроса (риск утечки scope между
  запросами). App-level per-query предикат безопаснее для этой архитектуры.

## Что скоуплено (данные, привязанные к устройству/пользователю)

- **Устройства:** `ListDevices`, `ListEnrolledDevices`, `GetDevice`, статус/канал/
  lock-состояние/decommission/delete/RD-unattended, privacy-тумблеры телеметрии.
- **Задачи:** `CreateTask`/`CreateLockTask`/`CreateRemoveSoftwareTask`/
  `CreateDecommissionTask` (создаются ТОЛЬКО на устройстве своего тенанта → иначе 404),
  `GetTask`, `ListDeviceTasks`.
- **Алерты:** `ListAlerts`, `AcknowledgeAlert`.
- **Заявки на admin-права:** `ListAdminAccessRequests`, `RespondToAdminRequest`,
  `RevokeAdminAccessRequest`, `GetAdminSoftwareDelta`.
- **Телеметрия:** метрики ресурсов, app-usage/активность.
- **Обращения (help):** список, скриншот, смена статуса.
- **Пользователи:** `ListUsers`, `GetUserByID`, `UpdateUserPassword`, `CreateUser`
  (штампует тенант создающего актора), `DisableUserTOTP` (устраняет кросс-тенантный
  admin-reset MFA).
- **Аудит:** `ListAuditLog` — по тенанту действовавшего пользователя (системные события
  и действия чужих тенантов скрыты от скоупленного; провайдер видит всё).
- **Compliance / отчёты / CVE:** `ComplianceReport` (каждый под-агрегат), 4 stream-отчёта
  (`reports.go`), `ListCVEFindings`/`CVESummaryData`, `ListRemediationLog`.
- **Соответствие политикам:** `ListSoftwarePolicyCompliance`,
  `ListSoftwarePolicyDeviceCompliance`, `ListScriptPolicyCompliance`.
- **Группы (device-сторона):** `FanOutScriptToGroup` фанит скрипт ТОЛЬКО на устройства
  своего тенанта (иначе кросс-тенантный RCE через shared-группу); `AddDeviceToGroup`/
  `RemoveDeviceFromGroup` трогают только своё устройство.
- **Результаты скриптов:** `ListScriptResultsByPolicy` — stdout/stderr только своих
  устройств (script-политики deployment-shared, но их вывод по устройствам скоуплен).
- **Remote desktop:** `GetDeviceCN` скоуплен — RD-сессию нельзя стартовать на устройство
  чужого тенанта (ErrNoRows → не стартует); worker доставки задач идёт без scope → работает.
- **Ростер миграции (по устройству):** `MigrationRosterForDevice` скоуплен по tenant_id
  устройства.

Инвариант провайдера: `VerifyAuditIntegrity` остаётся **глобальной** — хеш-цепочка
аудита неделима на тенанты (нельзя проверить целостность «куска»).

## Deployment-shared / follow-up (осознанные границы)

Ряд таблиц не имеет ни `tenant_id`, ни device/user-FK, поэтому их нельзя скоупить
join'ом. В этом проходе они трактуются как **deployment-shared** (общие ресурсы
деплоя), а не per-tenant:

- **Библиотека скриптов** (`scripts`) и **скрипт-политики** (`policies`/
  `policy_assignments`).
- **Группы устройств** (`device_groups`) — сами группы (имена/членство) видны всем;
  но device-действия через них уже скоуплены (см. FanOut/Add/Remove выше).
- **Список ростера миграции** (`ListMigrationRoster`) — немэтченные строки (импортированные
  hostname/serial без заехавшего устройства) не имеют tenant_id; полный скоуп требует
  колонки. Ростер ПО УСТРОЙСТВУ (`MigrationRosterForDevice`) уже скоуплен.
- **Токены энроллмента/инвайтов** (`enrollment_tokens`, `invitation_tokens`) — новые
  устройства/юзеры попадают в Default (или назначаются вручную).
- **Метаданные групп** (`ListDeviceGroups`) и **правила политик** (`ListPolicyRules`) —
  могут раскрывать device-UUID/group-id между тенантами (LOW; device-действия уже скоуплены).

Сделать их per-tenant = добавить `tenant_id` (DEFAULT Default, как в 050) + штамповать
на INSERT + скоупить — отдельный проход (Phase B).

**Известный gap (нотификации):** `GetITAdminsWithTelegramChatID` шлёт алерт-уведомление
ВСЕМ it_admin деплоя (алерт устройства тенанта A пингует и админов B). Путь системный
(без актора в ctx), поэтому ctx-шов не помогает — нужен явный проброс тенанта
устройства-источника. Follow-up.

## Тесты

- `internal/server/storage/tenant_scope_test.go` — изоляция на уровне storage
  (A видит своё, не B; провайдер видит всё; кросс-тенант GetDevice/CreateTask/мутация).
- `internal/server/api/tenant_scope_test.go` — end-to-end через middleware (JWT →
  резолв тенанта → scope) + провайдер видит всё.
