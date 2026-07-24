-- 055: внешний источник CVE-фида (singleton) — автоматическая периодическая синхронизация
-- фида уязвимостей вместо ручной заливки POST /cve/feed. Расширяет CVE-сканирование
-- (миграция 049, enterprise-фича FeatureCVEScan). Фоновый синкер (internal/server/cvesync)
-- по расписанию скачивает фид с url в том же JSON-формате, что принимает POST /cve/feed,
-- ЗАМЕНЯЕТ фид (LoadCVEFeed) и, если auto_scan, пересобирает находки (ScanCVE).
--
-- Singleton: ровно одна строка (id = true). url задаёт it_admin через GET/PUT /cve/feed-source
-- (не SSRF-вектор извне) — синкер всё равно лимитирует таймаут и размер тела ответа (анти-DoS).
CREATE TABLE cve_feed_source (
  id                  BOOLEAN     PRIMARY KEY DEFAULT true CHECK (id = true), -- singleton: только id=true
  url                 TEXT        NOT NULL DEFAULT '',    -- URL внешнего фида (тот же JSON-массив, что POST /cve/feed)
  sync_interval_hours INTEGER     NOT NULL DEFAULT 24,    -- период авто-синка в часах
  enabled             BOOLEAN     NOT NULL DEFAULT false, -- включён ли фоновый авто-синк
  auto_scan           BOOLEAN     NOT NULL DEFAULT true,  -- пересобирать находки (ScanCVE) после успешной заливки
  last_synced_at      TIMESTAMPTZ,                        -- время последней ПОПЫТКИ синка (успех или ошибка)
  last_status         TEXT        NOT NULL DEFAULT '',    -- итог последней попытки ('ok: ...' / 'error: ...')
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
