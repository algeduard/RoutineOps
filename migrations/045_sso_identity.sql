-- 045: SSO / OIDC — идентичность SSO-юзеров и server-side state авторизационного флоу.
-- Enterprise-фича (за лицензией FeatureSSO), но схема базовая (миграция применяется всегда).
--
-- Накат: scripts/migrate.sh, каждый файл РОВНО ОДИН РАЗ в --single-transaction. Плоский
-- DDL без IF NOT EXISTS — как 001..044.

-- ── Источник аутентификации юзера ────────────────────────────────────────────
-- auth_source: 'local' (пароль+опц. TOTP) | 'oidc' (внешний IdP). DEFAULT 'local' не
--   ломает ни один текущий логин/сессию. Для 'oidc' password-login и forgot/reset
--   запрещены (нет локального креденшла в обход IdP/IdP-side MFA).
-- oidc_issuer/oidc_subject: НЕИЗМЕНЯЕМЫЙ ключ SSO-идентичности (iss, sub) из ID-токена.
--   Матчинг ТОЛЬКО по этой паре, НИКОГДА по мутабельному email (иначе захват аккаунта
--   через email, в т.ч. seed-admin по SEED_ADMIN_EMAIL).
ALTER TABLE users ADD COLUMN auth_source TEXT NOT NULL DEFAULT 'local';
ALTER TABLE users ADD COLUMN oidc_issuer TEXT;
ALTER TABLE users ADD COLUMN oidc_subject TEXT;

-- Партиал-уник по стабильному субъекту: атомарно ловит гонку двух параллельных callback,
-- создающих одного и того же SSO-юзера. Только для 'oidc' (у 'local' поля NULL).
CREATE UNIQUE INDEX users_oidc_identity ON users (oidc_issuer, oidc_subject) WHERE auth_source = 'oidc';

-- ── Server-authoritative state авторизационного флоу ─────────────────────────
-- state (PK), nonce и PKCE-verifier между /sso/login и /sso/callback. Server-side (не
-- кука) → настоящий single-use: ConsumeSSOFlow делает SELECT+DELETE в одной транзакции по
-- PK, поэтому валиден РОВНО одному callback (детект replay/конкурентного callback — то,
-- чего stateless-кука дать не может). PKCE-verifier здесь и НИКОГДА в куке. TTL ~10 мин,
-- чистка DeleteExpiredSSOFlows.
CREATE TABLE sso_auth_flows (
  state         TEXT PRIMARY KEY,               -- случайный, сверяется с __Host-sso_flow кукой (CSRF) и query.state
  nonce         TEXT NOT NULL,                  -- сверяется с idToken.Nonce (replay-защита; go-oidc её не делает сам)
  pkce_verifier TEXT NOT NULL,                  -- code_verifier для обмена кода (PKCE S256)
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sso_auth_flows_created ON sso_auth_flows (created_at);
