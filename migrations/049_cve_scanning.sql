-- 049: сканирование установленного ПО на известные уязвимости (CVE) — enterprise-фича.
-- Источник данных — уже собираемый инвентарь device_software (миграция 001), нового сбора
-- от агента НЕ добавляем. Фид уязвимостей заливает сам деплойер (POST /cve/feed из выгрузки
-- NVD/OSV), матчинг сопоставляет инвентарь с фидом и пересобирает cve_findings.

-- cve_feed — известные уязвимости (одна строка = одна пара CVE×продукт с ограничением версии).
-- version_constraint — простая вменяемая схема матчинга (см. internal/server/storage/cve.go):
--   ''/'*'          — уязвим ЛЮБОЙ установленной версии продукта;
--   '<X.Y.Z'        — уязвим, если установленная версия МЕНЬШЕ X.Y.Z (исправлено в X.Y.Z);
--   '<=', '>', '>=' — аналогично с соответствующим сравнением;
--   '=X.Y.Z' | 'X.Y.Z' — уязвима ровно эта версия (покомпонентное сравнение).
-- cvss — DOUBLE PRECISION (а не NUMERIC), как device_metrics.cpu_percent: pgx чисто
-- сканирует его в float64 и не тянет pgtype.Numeric ради одного балла 0..10.
CREATE TABLE cve_feed (
  id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  cve_id             TEXT NOT NULL,                    -- напр. CVE-2023-1234
  product            TEXT NOT NULL,                    -- имя продукта (нормализуется при матчинге)
  version_constraint TEXT NOT NULL DEFAULT '',         -- см. выше; пусто = любая версия
  severity           TEXT NOT NULL DEFAULT 'medium',   -- low|medium|high|critical
  cvss               DOUBLE PRECISION,                 -- опц. базовый балл CVSS (0.0..10.0)
  summary            TEXT NOT NULL DEFAULT '',
  published_at       TIMESTAMPTZ,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Матчинг ходит по lower(product) (регистронезависимо), поэтому индекс по выражению.
CREATE INDEX idx_cve_feed_product ON cve_feed (lower(product));

-- cve_findings — результат последнего скана (пересобирается целиком на каждом POST /cve/scan).
-- product/installed_version — то, что РЕАЛЬНО стоит на устройстве (из device_software), а не
-- имя из фида: строка находки отвечает «какое ПО на машине уязвимо и какой версии».
CREATE TABLE cve_findings (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  device_id         UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  cve_id            TEXT NOT NULL,
  product           TEXT NOT NULL,
  installed_version TEXT NOT NULL DEFAULT '',
  severity          TEXT NOT NULL DEFAULT 'medium',
  cvss              DOUBLE PRECISION,
  detected_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cve_findings_device_id ON cve_findings (device_id);
CREATE INDEX idx_cve_findings_severity ON cve_findings (severity);
