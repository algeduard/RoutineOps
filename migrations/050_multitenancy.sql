-- 050: мультитенантность (MVP) — модель тенантов + привязка сущностей. Это АДДИТИВНЫЙ,
-- безопасный фундамент: ничего существующего он не ломает. ПОЛНАЯ per-query изоляция данных
-- (scoping КАЖДОГО запроса к devices/users/tasks/... по tenant_id) — это большой cross-cutting
-- рефактор и он FOLLOW-UP, здесь НЕ делается.
--
-- tenants — арендаторы (организации/подразделения). Строка "Default" с ФИКСИРОВАННЫМ id
-- создаётся тут же и неудаляема (guard в коде): в неё бэкфилятся все существующие устройства
-- и пользователи, и в неё же попадают новые (см. DEFAULT колонок ниже). slug — машинный
-- идентификатор (lowercase [a-z0-9-], валидируется в API).
CREATE TABLE tenants (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name       TEXT NOT NULL UNIQUE,
  slug       TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Default-тенант с известным фиксированным id: и код (storage.DefaultTenantID), и DEFAULT
-- колонок ниже ссылаются ровно на него. INSERT идёт ДО ADD COLUMN, чтобы бэкфилл существующих
-- строк удовлетворял FK.
INSERT INTO tenants (id, name, slug)
VALUES ('00000000-0000-0000-0000-000000000001', 'Default', 'default');

-- tenant_id на devices/users. NULLABLE + DEFAULT = id default-тенанта. КРИТИЧНО безопасно:
-- ADD COLUMN с константным DEFAULT бэкфилит ВСЕ существующие строки этим значением (ни одно
-- устройство/юзер не осиротеет), а новые devices/users без явного tenant_id тоже попадают в
-- default — INSERT-пути (CreatePendingDevice/CreateUser/heartbeat-upsert) НЕ трогаем, они
-- берут DEFAULT автоматически. Существующие SELECT-запросы колонку не читают → не ломаются.
-- ON DELETE SET NULL: удаление тенанта НЕ каскадит снос устройств/юзеров, а лишь осиротит их
-- tenant_id (в штатном потоке недостижимо — непустой тенант удалить нельзя, default неудаляем).
ALTER TABLE devices ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE SET NULL DEFAULT '00000000-0000-0000-0000-000000000001';
ALTER TABLE users   ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE SET NULL DEFAULT '00000000-0000-0000-0000-000000000001';

CREATE INDEX idx_devices_tenant_id ON devices (tenant_id);
CREATE INDEX idx_users_tenant_id ON users (tenant_id);
