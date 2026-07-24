-- 051: Policy-as-code / GitOps — декларативные software-политики + детект дрейфа
-- (enterprise-фича). Админ описывает ЖЕЛАЕМЫЙ набор глобальных software-правил
-- декларативно (JSON), сервер хранит его как source-of-truth и реконсилит живые
-- software_policy_rules (только ГЛОБАЛЬНЫЕ: device_id IS NULL AND group_id IS NULL —
-- device/group-scoped правила управляются отдельно и декларацией не трогаются).
--
-- policy_declaration — версионируемая (append-only): каждый apply вставляет новую строку,
-- «текущая» декларация = самая свежая по applied_at. История применений остаётся для аудита.
-- content — JSON-массив желаемых правил: [{"software_name","rule_type","platforms":[...]}].
-- rule_type: allowed|forbidden; platforms: подмножество {macOS,Windows,Linux}, пусто/null =
-- все платформы (та же семантика, что software_policy_rules.platforms).
CREATE TABLE policy_declaration (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  content     JSONB NOT NULL DEFAULT '[]'::jsonb, -- массив желаемых software-правил
  rule_count  INTEGER NOT NULL DEFAULT 0,         -- размер декларации (для дешёвого показа истории)
  created     INTEGER NOT NULL DEFAULT 0,         -- сколько правил создано этим применением
  deleted     INTEGER NOT NULL DEFAULT 0,         -- сколько правил удалено этим применением
  applied_by  TEXT NOT NULL DEFAULT '',           -- email применившего (из JWT-актора)
  applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- «Текущая» декларация выбирается ORDER BY applied_at DESC LIMIT 1 — индекс делает выборку
-- последней строки дешёвой при росте истории.
CREATE INDEX idx_policy_declaration_applied_at ON policy_declaration (applied_at DESC);
