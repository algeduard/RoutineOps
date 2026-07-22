-- 036: аудит выданных админ-прав — дельта инвентаря ПО за JIT-сессию.
-- Пока у сотрудника действовали временные админ-права, агент снимает снимок ПО на
-- выдаче (baseline) и на снятии, diff даёт что установлено/удалено за сессию. Едет в
-- ReportAdminAccess (поля software_added/removed) на REVOKED. Привязано к заявке;
-- CASCADE — при удалении заявки дельта уходит с ней.
CREATE TABLE IF NOT EXISTS admin_access_software_delta (
  request_id    UUID NOT NULL REFERENCES admin_access_requests(id) ON DELETE CASCADE,
  change_type   TEXT NOT NULL,            -- 'added' | 'removed'
  software_name TEXT NOT NULL,
  version       TEXT NOT NULL DEFAULT '',
  -- Идемпотентность: outbox даёт at-least-once, повторный REVOKED-отчёт не дублирует.
  PRIMARY KEY (request_id, change_type, software_name, version)
);

CREATE INDEX IF NOT EXISTS idx_admin_delta_request ON admin_access_software_delta (request_id);
