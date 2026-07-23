-- 048: уровни критичности алертов + маршрутизация (эскалация, дежурства).
--
-- severity — CORE-колонка: отдаётся в GET /alerts всем ролям (бейдж в UI), поэтому живёт
-- прямо в alerts, а не за лицензией. Значения info|warning|critical. DEFAULT 'warning'
-- безопасен и для существующих строк (бэкфилл), и для вставок, где severity не задана явно
-- (напр. agent_unreachable из DetectUnreachableDevices — эпизод недоступности = warning).
ALTER TABLE alerts ADD COLUMN severity TEXT NOT NULL DEFAULT 'warning';

-- routed_at — durable-курсор фонового маршрутизатора (enterprise): NULL = алерт ещё не
-- обработан. Помечается временем попытки доставки (маршрутизация best-effort: помечаем даже
-- если правил нет или канал недоступен — иначе алерт крутился бы в очереди вечно).
-- last_escalated_at — анти-спам эскалации: время последней повторной доставки критичного
-- алерта. Обе колонки — внутренняя механика, наружу (в /alerts) не отдаются.
ALTER TABLE alerts ADD COLUMN routed_at TIMESTAMPTZ;
ALTER TABLE alerts ADD COLUMN last_escalated_at TIMESTAMPTZ;

-- Частичный индекс под поллинг маршрутизатора: он выбирает только необработанные алерты
-- (routed_at IS NULL), и без индекса это был бы seq-scan по всей таблице на каждый тик.
CREATE INDEX idx_alerts_unrouted ON alerts (created_at) WHERE routed_at IS NULL;

-- Правила маршрутизации алертов (enterprise-фича FeatureAlertRouting). Алерт с
-- severity >= min_severity правила доставляется в channel/target. escalate_after_minutes>0
-- включает эскалацию: критичный алерт без ack за N минут доставляется повторно.
--   min_severity — порог срабатывания (info|warning|critical);
--   channel      — telegram | webhook;
--   target       — chat_id (telegram) либо http(s)-URL (webhook);
--   enabled      — выключенные правила пропускаются маршрутизатором.
CREATE TABLE alert_routing_rules (
  id                     UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
  min_severity           TEXT        NOT NULL DEFAULT 'warning',
  channel                TEXT        NOT NULL,
  target                 TEXT        NOT NULL,
  enabled                BOOLEAN     NOT NULL DEFAULT true,
  escalate_after_minutes INTEGER     NOT NULL DEFAULT 0,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
