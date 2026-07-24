-- 056: SCIM group→role mapping (расширение enterprise-фичи SCIM за лицензией FeatureSCIM;
-- схема базовая — миграция применяется всегда). Раньше все SCIM-провижининг-юзеры получали
-- жёстко роль 'viewer'. Теперь IdP управляет правами: группы, пришедшие в SCIM User payload
-- (User.groups у Okta/Azure AD/OneLogin), маппятся на роль RoutineOps — it_admin через
-- allowlist admin-групп, иначе default_role.
--
-- Накат: scripts/migrate.sh, каждый файл РОВНО ОДИН РАЗ в --single-transaction. Плоский DDL
-- без IF NOT EXISTS (как 001..050), безопасные DEFAULT — существующие строки не трогает.

-- ── Конфиг маппинга групп на роли (singleton) ────────────────────────────────
-- admin_group_values: CSV значений групп (value/display из SCIM User.groups), дающих it_admin.
--   ALLOWLIST, fail-closed: пустая строка (DEFAULT) = it_admin через SCIM НЕ выдаётся никому
--   (все получают default_role). it_admin достижим ТОЛЬКО явным совпадением группы в этом
--   списке — как SSO SSO_ADMIN_VALUES (sso_enterprise.go roleFromClaim).
-- default_role: роль для юзеров БЕЗ admin-группы. DEFAULT 'viewer' (least privilege). it_admin
--   здесь запрещён на уровне API (валидация PUT /scim/role-mapping) — «it_admin по умолчанию
--   НИКОГДА», эскалация только явной admin-группой.
-- Строки нет на старте → код отдаёт дефолт (admin-групп нет, роль viewer): текущий провижининг
--   сохраняет прежнее поведение (все новые SCIM-юзеры — viewer), пока админ не настроит маппинг.
CREATE TABLE scim_role_mapping (
  id                 BOOLEAN     PRIMARY KEY DEFAULT true CHECK (id = true),
  admin_group_values TEXT        NOT NULL DEFAULT '',
  default_role       TEXT        NOT NULL DEFAULT 'viewer',
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
