-- 058: SAML 2.0 SSO — идентичность SAML-юзеров РЯДОМ с OIDC (миграция 045). НОВЫХ колонок нет:
-- переиспользуем users.auth_source/oidc_issuer/oidc_subject и таблицу sso_auth_flows.
--   auth_source='saml' — внешний IdP по SAML (ADFS/Okta SAML/Azure AD SAML). Как и 'oidc',
--     password-login и forgot/reset запрещены (login-гард уже режет любой не-'local').
--   oidc_issuer = SAML IdP EntityID (Issuer), oidc_subject = NameID — НЕИЗМЕНЯЕМЫЙ ключ
--     SAML-идентичности. Матчинг ТОЛЬКО по этой паре, НИКОГДА по мутабельному email.
--   sso_auth_flows переиспользуется для SP-initiated флоу: state=RelayState, nonce=AuthnRequest
--     ID (для сверки InResponseTo), pkce_verifier='' (в SAML не применяется). Single-use как у OIDC.
--
-- Накат: scripts/migrate.sh, каждый файл РОВНО ОДИН РАЗ в --single-transaction. Плоский DDL.

-- Партиал-уник по (issuer, subject) для 'saml' — атомарно ловит гонку двух параллельных ACS,
-- создающих одного и того же SAML-юзера (аналог users_oidc_identity для 'oidc'). Отдельный
-- индекс (а не общий с oidc), чтобы SAML EntityID и OIDC issuer не пересекались в одном ключе.
CREATE UNIQUE INDEX users_saml_identity ON users (oidc_issuer, oidc_subject) WHERE auth_source = 'saml';
