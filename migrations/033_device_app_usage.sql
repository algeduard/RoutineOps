-- 033: аналитика активности приложений и времени за ПК (суточные агрегаты).
-- Приходит от агента дельтами (ReportAppUsage → UpsertAppUsage/UpsertDailyActivity).
--
-- ЧУВСТВИТЕЛЬНЫЕ данные о поведении сотрудника: сбор ВЫКЛЮЧЕН по умолчанию и
-- включается точечно на устройство (devices.app_usage_enabled, privacy/consent;
-- см. docs/device-telemetry-design.md §4). Гранулярность — имя процесса, НЕ
-- заголовки окон/URL.
--
-- Модель ДЕЛЬТ: агент шлёт прирост с прошлой отправки, сервер аккумулирует
-- (existing + delta) — переживает рестарт агента (in-memory счётчик сбрасывается,
-- абсолют не регрессирует). Ретенция — CleanupOldData по day, DataRetentionDays.

-- Использование приложений: одна строка на (устройство, день, приложение).
CREATE TABLE IF NOT EXISTS device_app_usage (
  device_id          UUID        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  day                DATE        NOT NULL,   -- локальный день устройства
  app_name           TEXT        NOT NULL,   -- имя foreground-процесса (напр. 'chrome.exe')
  foreground_seconds BIGINT      NOT NULL DEFAULT 0, -- суммарно секунд на переднем плане (при активном вводе)
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (device_id, day, app_name)
);
CREATE INDEX IF NOT EXISTS idx_device_app_usage_device_day
  ON device_app_usage (device_id, day);

-- Активное/простойное время за ПК: одна строка на (устройство, день).
CREATE TABLE IF NOT EXISTS device_activity_daily (
  device_id      UUID        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  day            DATE        NOT NULL,       -- локальный день устройства
  active_seconds BIGINT      NOT NULL DEFAULT 0, -- секунд активности (ввод в пределах idle-порога)
  idle_seconds   BIGINT      NOT NULL DEFAULT 0, -- секунд простоя
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (device_id, day)
);

-- Privacy-тумблер сбора аналитики приложений на устройство. Дефолт false:
-- ничего не собирается, пока it_admin явно не включит (PUT /devices/{id}/telemetry-config).
ALTER TABLE devices ADD COLUMN IF NOT EXISTS app_usage_enabled BOOLEAN NOT NULL DEFAULT false;
