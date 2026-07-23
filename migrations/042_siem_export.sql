-- 042: экспорт аудита в SIEM (enterprise-фича). seq — МОНОТОННЫЙ курсор экспорта: UUID-PK
-- audit_log не упорядочен, а created_at имеет коллизии, поэтому форвардить «всё, что новее
-- курсора» по ним нельзя. IDENTITY бэкфилит существующие строки и растёт на каждой вставке.
ALTER TABLE audit_log ADD COLUMN seq BIGINT GENERATED ALWAYS AS IDENTITY;
CREATE UNIQUE INDEX idx_audit_log_seq ON audit_log (seq);

-- Конфиг экспорта — singleton (id=1). last_sent_seq durable: переживает рестарт, чтобы не
-- форвардить события повторно. При первом включении курсор инициализируется текущим
-- максимумом seq (форвардим только НОВЫЕ события, историю не выгружаем).
CREATE TABLE siem_export_config (
  id            INT         PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  enabled       BOOLEAN     NOT NULL DEFAULT false,
  webhook_url   TEXT        NOT NULL DEFAULT '',
  hmac_secret   TEXT        NOT NULL DEFAULT '', -- ключ подписи тела (X-RoutineOps-Signature)
  last_sent_seq BIGINT      NOT NULL DEFAULT 0,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
