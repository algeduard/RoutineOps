-- 046: SCIM 2.0 provisioning (enterprise-фича за лицензией FeatureSCIM, схема базовая —
-- миграция применяется всегда). IdP (Okta/Azure AD/OneLogin) создаёт/деактивирует юзеров
-- RoutineOps через /scim/v2/* со своим bearer-токеном.
--
-- Накат: scripts/migrate.sh, каждый файл РОВНО ОДИН РАЗ в --single-transaction. Плоский DDL
-- без IF NOT EXISTS — как 001..045. Все ALTER — с безопасным DEFAULT: не ломают текущие
-- строки/сессии (существующие юзеры остаются активными и локальными).

-- ── Активность учётки ────────────────────────────────────────────────────────
-- is_active: SCIM-деактивация помечает юзера неактивным (не хард-удаление). DEFAULT true —
--   ни один текущий логин/сессия не ломается. Неактивного юзера отвергают login и
--   jwtMiddleware (см. auth.go: GetUserPasswordChangedAt фильтрует по is_active, login/loginMFA
--   гейтят до issueToken).
ALTER TABLE users ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT true;

-- ── SCIM-атрибуты имени ──────────────────────────────────────────────────────
-- name.givenName/familyName из SCIM User — чтобы GET/List отдавали ровно то, что прислал IdP
--   (faithful round-trip). users.name хранит собранное отображаемое имя. DEFAULT '' — не
--   влияет на не-SCIM юзеров.
ALTER TABLE users ADD COLUMN scim_given_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN scim_family_name TEXT NOT NULL DEFAULT '';

-- ── SCIM bearer-токен (singleton) ────────────────────────────────────────────
-- token_hash: sha256-хекс активного SCIM-токена. Сам токен показывается один раз при
--   генерации/ротации и НЕ хранится (только хеш; сравнение constant-time). '' = SCIM выключен
--   (эндпоинты отдают 401). Ротация = перезапись строки (created_at сдвигается на now()).
CREATE TABLE scim_config (
  id          BOOLEAN     PRIMARY KEY DEFAULT true CHECK (id = true),
  token_hash  TEXT        NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
