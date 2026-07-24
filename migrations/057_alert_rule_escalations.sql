-- 057: per-(alert, rule) состояние эскалации.
--
-- Раньше анти-спам эскалации считался по ЕДИНОЙ на алерт колонке alerts.last_escalated_at,
-- но порог свой у каждого правила (escalate_after_minutes). Если на один алерт матчатся
-- НЕСКОЛЬКО правил эскалации с РАЗНЫМИ порогами, быстрое правило на каждом тике сбрасывало
-- бы общий last_escalated_at, и медленное правило могло НИКОГДА не сработать (тихо терялся
-- целый канал эскалации). Здесь храним время последней эскалации отдельно для каждой пары
-- (алерт, правило), поэтому анти-спам каждого правила независим.
--
-- ON DELETE CASCADE по обеим FK: удаление алерта (например, каскадом за устройством) или
-- правила маршрутизации чистит и его строки эскалации — таблица не копит сироты.
CREATE TABLE alert_rule_escalations (
  alert_id          UUID        NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
  rule_id           UUID        NOT NULL REFERENCES alert_routing_rules(id) ON DELETE CASCADE,
  last_escalated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (alert_id, rule_id)
);
