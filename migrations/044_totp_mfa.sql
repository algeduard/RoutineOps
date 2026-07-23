-- 044: TOTP MFA (RFC 6238) — БАЗОВАЯ фича (НЕ за лицензией), для всех человеческих ролей.
-- Безопасность наката на существующих юзерах: MFA ВЫКЛючена по умолчанию
-- (totp_enabled DEFAULT false) → ни один текущий логин/сессия не ломается, массового
-- разлогина нет.
--
-- Накат: scripts/migrate.sh ведёт schema_migrations и применяет КАЖДЫЙ файл РОВНО ОДИН
-- РАЗ в --single-transaction. Поэтому здесь плоские CREATE TABLE / ADD COLUMN БЕЗ
-- IF NOT EXISTS — как в 001..043 (повторный накат невозможен by design). uuid_generate_v4()
-- (расширение uuid-ossp) — идиома репозитория (ср. 012/029).

-- ── Поля TOTP на пользователе ────────────────────────────────────────────────
-- totp_secret_enc: секрет RFC 6238, ЗАШИФРОВАННЫЙ AES-256-GCM в приложении (это НЕ хеш —
--   секрет надо расшифровывать для проверки кода). Blob: 0x01(версия ключа) || nonce(12) ||
--   ciphertext+tag; AAD = 16 канонических байт user_id. Ключ — ROUTINEOPS_MFA_ENC_KEY в
--   env, НЕ в БД (атакующий с одной лишь БД секрет не восстановит). NULL = секрета нет.
-- totp_enabled: MFA включена → второй шаг логина обязателен. false до подтверждения кодом.
-- totp_confirmed_at: момент подтверждения секрета (когда enabled стал true).
-- totp_last_step: последний ПРИНЯТЫЙ RFC-6238 time-step counter (T). Replay-защита —
--   код принимается только если его counter > totp_last_step.
ALTER TABLE users ADD COLUMN totp_secret_enc   BYTEA;
ALTER TABLE users ADD COLUMN totp_enabled      BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN totp_confirmed_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN totp_last_step    BIGINT NOT NULL DEFAULT 0;

-- ── Recovery-коды ────────────────────────────────────────────────────────────
-- 10 кодов на юзера, каждый показывается ОДИН раз при выпуске, хранится SHA-256-хешем.
-- Код = 15 случайных байт = 120 бит энтропии: offline-перебор unsalted sha256 из дампа БД
-- инфизибелен (2^119), поэтому unsalted sha256 корректен (как в 028/029 с высокоэнтропийными
-- токенами) и даёт O(1)-поиск по UNIQUE-индексу + одноразовое удаление одним DELETE. ГЛАВНОЕ:
-- sha256 НЕ зависит от ROUTINEOPS_MFA_ENC_KEY → recovery-вход работает при потере ключа.
CREATE TABLE mfa_recovery_codes (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash  TEXT NOT NULL,                       -- hex(sha256(нормализованный код))
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_mfa_recovery_code_hash ON mfa_recovery_codes(code_hash);
CREATE INDEX idx_mfa_recovery_user ON mfa_recovery_codes(user_id);

-- ── Промежуточный challenge между шагом-1 (пароль) и шагом-2 (TOTP) ───────────
-- Короткоживущий (5 мин), одноразовый, single-purpose токен, привязан к user_id. Это НЕ
-- сессия: отдаётся в теле ответа (НЕ cookie), НЕ подписан jwtSecret, jwtMiddleware его не
-- разбирает; принимается ТОЛЬКО хендлером /auth/login/mfa и только по хешу. Хранится
-- sha256-хешем (дамп БД не даёт рабочий токен). consumed_at NULL = ещё не израсходован;
-- одноразовость обеспечивается атомарным UPDATE ... WHERE consumed_at IS NULL RETURNING.
CREATE TABLE mfa_challenges (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL,                       -- hex(sha256(plaintext challenge))
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at  TIMESTAMPTZ NOT NULL,                -- created_at + 5 минут
  consumed_at TIMESTAMPTZ                          -- NULL = не израсходован
);
CREATE UNIQUE INDEX idx_mfa_challenge_hash ON mfa_challenges(token_hash);
CREATE INDEX idx_mfa_challenge_expiry ON mfa_challenges(expires_at);
