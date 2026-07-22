-- 032: телеметрия ресурсов устройства (time-series).
-- Приходит от агента батчами (ReportResourceMetrics → InsertResourceMetrics).
--
-- Обычная таблица Postgres, БЕЗ TimescaleDB (нет в стеке): объём умеренный
-- (при 15с ≈ 5760 строк/сутки на устройство), глубину режет ретенция
-- (CleanupOldData по ts, DataRetentionDays). Диапазонные запросы истории и
-- «последний сэмпл» фильтруют по (device_id, ts) — покрыто индексом ниже.
--
-- Недоступная на данной ОС метрика приезжает нулём («не собрано»), а не NULL:
-- сэмпл всё равно валиден по времени, а нулевые пробы редки и не искажают график.
CREATE TABLE IF NOT EXISTS device_metrics (
  device_id       UUID        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  ts              TIMESTAMPTZ NOT NULL,      -- момент сэмпла (агентские часы, клампятся сервером)
  cpu_percent     DOUBLE PRECISION,          -- загрузка CPU, 0..100
  mem_used_bytes  BIGINT,                    -- использовано RAM, байт
  mem_total_bytes BIGINT,                    -- всего RAM, байт
  disk_percent    DOUBLE PRECISION,          -- загрузка системного тома, 0..100
  net_rx_bps      BIGINT,                    -- входящий трафик, байт/с (дельта счётчиков)
  net_tx_bps      BIGINT                     -- исходящий трафик, байт/с (дельта счётчиков)
);

-- (device_id, ts DESC): и история за диапазон (WHERE device_id=? AND ts>=?),
-- и «последний сэмпл» (ORDER BY ts DESC LIMIT 1), и ретенция (DELETE ... ts<cutoff).
CREATE INDEX IF NOT EXISTS idx_device_metrics_device_ts
  ON device_metrics (device_id, ts DESC);
