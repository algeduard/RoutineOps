-- 040: полные URL активных вкладок браузера в app-usage (напр. "https://example.com/
-- page"), читаются через UI Automation. САМОЕ ЧУВСТВИТЕЛЬНОЕ из телеметрии активности:
-- URL точнее заголовка окна и может содержать личные данные/токены в query. Собирается
-- ТОЛЬКО при ОТДЕЛЬНОМ флаге capture_urls (строже capture_window_titles), включает
-- только it_admin с аудитом; приватные/инкогнито-окна исключаются всегда, читается
-- лишь из известных браузеров. См. docs/device-telemetry-design.md §4 и миграцию 035.

-- Гранулярность строки использования приложений: теперь (устройство, день,
-- приложение, ЗАГОЛОВОК ОКНА, URL). Пустой URL '' = «URL не собираются» (агент шлёт ""
-- при выключенном флаге) — обратная совместимость со строками 033/035.
ALTER TABLE device_app_usage ADD COLUMN IF NOT EXISTS url TEXT NOT NULL DEFAULT '';

ALTER TABLE device_app_usage DROP CONSTRAINT IF EXISTS device_app_usage_pkey;
ALTER TABLE device_app_usage ADD PRIMARY KEY (device_id, day, app_name, window_title, url);

-- Отдельный privacy-тумблер сбора URL на устройство. Дефолт false: даже при включённых
-- app_usage_enabled и capture_window_titles URL НЕ собираются, пока это не true.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS capture_urls BOOLEAN NOT NULL DEFAULT false;
