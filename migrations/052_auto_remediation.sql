-- 052: автоматическое устранение запрещённого ПО (auto-remediation, enterprise-фича).
-- Пока forbidden-ПО (software-политика rule_type='forbidden') только ДЕТЕКТится и алертится;
-- эта фича опционально АВТОМАТИЧЕСКИ удаляет его, ПЕРЕИСПОЛЬЗУЯ существующий путь удаления ПО
-- (task_type='remove_software' + worker-доставка), а не заводя новый механизм деинсталляции.

-- Конфиг — singleton (id boolean PK, единственная строка id=true). enabled ПО УМОЛЧАНИЮ ВЫКЛ:
-- авто-удаление ПО деструктивно, включать его должен осознанно администратор. dry_run — режим
-- безопасной обкатки: нарушения только ЛОГИРУЮТСЯ (что удалили бы), задачи удаления не создаются.
-- Строки может не быть вовсе — GetAutoRemediationConfig трактует её отсутствие как «всё выкл».
CREATE TABLE auto_remediation_config (
  id         BOOLEAN     PRIMARY KEY DEFAULT true CHECK (id),
  enabled    BOOLEAN     NOT NULL DEFAULT false,
  dry_run    BOOLEAN     NOT NULL DEFAULT false,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Лог ремедиаций — история того, что фоновый ремедиатор сделал (или сделал бы в dry_run).
-- task_id ссылается на созданную remove_software-задачу (NULL для dry_run — задачи нет).
-- ON DELETE SET NULL у task_id: чистка/удаление задачи не сносит запись истории. ON DELETE
-- CASCADE у device_id: снятое устройство уносит свою историю ремедиаций (как device_software).
CREATE TABLE auto_remediation_log (
  id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
  device_id     UUID        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  software_name TEXT        NOT NULL,
  task_id       UUID        REFERENCES tasks(id) ON DELETE SET NULL,
  action        TEXT        NOT NULL, -- 'removed' (создана задача удаления) | 'dry_run' (только лог)
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Индекс под дедуп dry_run-логирования (EXISTS по device+software) и историю устройства.
CREATE INDEX idx_auto_remediation_log_device ON auto_remediation_log (device_id, software_name);
-- Индекс под GET /auto-remediation/log (последние сверху).
CREATE INDEX idx_auto_remediation_log_created ON auto_remediation_log (created_at DESC);
