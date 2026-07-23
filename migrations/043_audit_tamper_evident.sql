-- 043: tamper-evident аудит — КЕЙД ХЕШ-ЦЕПОЧКА. Каждая подписанная строка:
--   entry_hmac = HMAC-SHA256(key, prev_hmac || canonical(строки)),
-- где prev_hmac — entry_hmac предыдущей подписанной строки, key — ROUTINEOPS_AUDIT_HMAC_KEY
-- (в env, НЕ в БД). Цепочка ловит: модификацию (звено не сходится), удаление/вставку/
-- перестановку (рвётся связь с соседом), replay (позиция в цепочке иная). Всё это требует
-- ПЕРЕсчёта звеньев → нужен ключ (в env), которого у атакующего с доступом только к БД нет.
-- Голова цепочки (audit_chain, singleton) хранит hmac/seq ПОСЛЕДНЕЙ строки — якорь против
-- НАИВНОГО усечения хвоста: удалишь последнюю строку — last_hash не сойдётся. ОГОВОРКА:
-- усечение хвоста ключа не требует, поэтому решительный атакующий может удалить хвост И
-- поправить audit_chain на hmac новой последней строки — это НЕ задетектится. Полная защита
-- от усечения — только внешний якорь (SIEM-экспорт вывозит строки из БД до любого удаления).
-- HMAC считается в SQL (pgcrypto). Запись сериализуется FOR UPDATE головы (порядок цепочки
-- = порядок seq).
CREATE EXTENSION IF NOT EXISTS pgcrypto;
ALTER TABLE audit_log ADD COLUMN entry_hmac TEXT NOT NULL DEFAULT '';

CREATE TABLE audit_chain (
  id        BOOLEAN PRIMARY KEY DEFAULT true CHECK (id), -- singleton (одна строка id=true)
  last_hash TEXT   NOT NULL DEFAULT '',                  -- entry_hmac последней подписанной строки
  last_seq  BIGINT NOT NULL DEFAULT 0                     -- её seq
);
INSERT INTO audit_chain (id) VALUES (true);
