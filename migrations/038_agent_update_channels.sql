-- 038: каналы обновления агента (stable / beta). Сейчас парк всегда тянет последний
-- релиз; после этого — устройство получает только релизы своего канала. Устройство на
-- 'stable' НИКОГДА не видит beta-билд; устройство на 'beta' видит beta+stable (в
-- манифест уходит новейший из этих двух). Всё это — политика раскатки, а НЕ граница
-- безопасности: бинарь публичен и защищён sha256 + ed25519-подписью манифеста, канал
-- лишь решает, какую подписанную версию показать устройству.
--
-- Аддитивно и обратно-совместимо: обе колонки NOT NULL DEFAULT 'stable', поэтому все
-- существующие релизы и устройства становятся 'stable' — поведение до миграции
-- (весь парк на последнем стабильном) сохраняется, пока админ явно не переведёт
-- устройство на beta и не опубликует beta-релиз.
ALTER TABLE agent_releases ADD COLUMN IF NOT EXISTS channel        TEXT NOT NULL DEFAULT 'stable';
ALTER TABLE devices        ADD COLUMN IF NOT EXISTS update_channel TEXT NOT NULL DEFAULT 'stable';

-- Домен значений держим в БД (как lock_mode в 022): невалидный канал не должен
-- просочиться ни через publish-release, ни через админскую ручку. Guard по pg_constraint
-- делает добавление идемпотентным (повторный прогон файла не падает на дубле).
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_releases_channel_chk') THEN
    ALTER TABLE agent_releases ADD CONSTRAINT agent_releases_channel_chk CHECK (channel IN ('stable','beta'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'devices_update_channel_chk') THEN
    ALTER TABLE devices ADD CONSTRAINT devices_update_channel_chk CHECK (update_channel IN ('stable','beta'));
  END IF;
END $$;

-- Выборка «последний релиз для os/arch, видимый каналу» фильтрует по (os,arch,channel)
-- и сортирует по created_at DESC — этот индекс её обслуживает (расширяет idx из 009,
-- добавляя channel в ключ перед created_at).
CREATE INDEX IF NOT EXISTS idx_agent_releases_os_arch_channel
  ON agent_releases(os, arch, channel, created_at DESC);
