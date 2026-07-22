-- 032: обращения за помощью с устройств («Сообщить о проблеме» в трее агента).
-- Сотрудник пишет текст и опционально прикладывает скриншот (JPEG, сжат агентом);
-- служба агента доставляет обращение по mTLS (SubmitHelpRequest), IT-админ видит
-- его в вебе и закрывает. Скриншот храним в bytea: self-hosted инсталляции без
-- объектного стора, размер ограничен сервером (2МБ), в списки bytea не отдаём.
CREATE TABLE help_requests (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  reporter TEXT,                        -- логин консольного пользователя устройства
  message TEXT NOT NULL,
  screenshot BYTEA,                     -- JPEG; NULL = обращение без скриншота
  status TEXT NOT NULL DEFAULT 'new',   -- new / closed
  created_at TIMESTAMPTZ NOT NULL,      -- момент создания на устройстве (clamped сервером)
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- момент доставки на сервер (для кулдауна)
  closed_by UUID REFERENCES users(id),
  closed_at TIMESTAMPTZ
);

CREATE INDEX idx_help_requests_device ON help_requests(device_id);
CREATE INDEX idx_help_requests_status ON help_requests(status);
