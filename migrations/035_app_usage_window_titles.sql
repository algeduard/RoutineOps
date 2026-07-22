-- 034: заголовки активных окон в app-usage (напр. вкладки браузера — грубо «какой
-- сайт»). ЕЩЁ БОЛЕЕ ЧУВСТВИТЕЛЬНО, чем имя процесса: заголовок может содержать
-- личные данные. Собирается ТОЛЬКО при ОТДЕЛЬНОМ флаге capture_window_titles
-- (строже app_usage_enabled), включает только it_admin с аудитом. См.
-- docs/device-telemetry-design.md §4.

-- Гранулярность строки использования приложений: теперь (устройство, день,
-- приложение, ЗАГОЛОВОК ОКНА). Пустой заголовок '' = «заголовки не собираются»
-- (агент шлёт "" при выключенном флаге) — обратная совместимость со строками 033.
ALTER TABLE device_app_usage ADD COLUMN IF NOT EXISTS window_title TEXT NOT NULL DEFAULT '';

ALTER TABLE device_app_usage DROP CONSTRAINT IF EXISTS device_app_usage_pkey;
ALTER TABLE device_app_usage ADD PRIMARY KEY (device_id, day, app_name, window_title);

-- Отдельный privacy-тумблер сбора заголовков окон на устройство. Дефолт false:
-- даже при включённом app_usage_enabled заголовки НЕ собираются, пока это не true.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS capture_window_titles BOOLEAN NOT NULL DEFAULT false;
