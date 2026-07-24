package storage

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Policy-as-code / GitOps: декларативные ГЛОБАЛЬНЫЕ software-политики. Админ хранит желаемый
// набор правил как JSON source-of-truth (policy_declaration), сервер реконсилит живые
// software_policy_rules и показывает дрейф. Реконсиляция ТРОГАЕТ только глобальные правила
// (device_id IS NULL AND group_id IS NULL): device/group-scoped правила управляются отдельно
// (карточка устройства / группа) и декларацией намеренно не затрагиваются.

// DesiredPolicyRule — одно желаемое software-правило в декларации. Формат совпадает с телом
// CreatePolicyRule по смысловым полям (без scope): platforms пусто/nil = все платформы.
type DesiredPolicyRule struct {
	SoftwareName string   `json:"software_name"`
	RuleType     string   `json:"rule_type"`
	Platforms    []string `json:"platforms"`
}

// PolicyDeclaration — сохранённая декларация (последнее применение). Content — желаемые
// правила (нормализованные при apply). Created/Deleted — сколько правил создано/удалено ТЕМ
// применением, что записало эту строку (для истории/аудита).
type PolicyDeclaration struct {
	ID        string              `json:"id"`
	Content   []DesiredPolicyRule `json:"content"`
	RuleCount int                 `json:"rule_count"`
	Created   int                 `json:"created"`
	Deleted   int                 `json:"deleted"`
	AppliedBy string              `json:"applied_by"`
	AppliedAt time.Time           `json:"applied_at"`
}

// PolicyDrift — расхождение живых глобальных правил с сохранённой декларацией. ToCreate — есть
// в декларации, но нет в БД; ToDelete — есть в БД, но нет в декларации; InSync — совпадающих.
type PolicyDrift struct {
	ToCreate []DesiredPolicyRule `json:"to_create"`
	ToDelete []DesiredPolicyRule `json:"to_delete"`
	InSync   int                 `json:"in_sync"`
}

// canonicalPlatforms приводит набор платформ к каноничному виду для сравнения: отсортированная
// копия. Пустой/nil → пустой (все платформы). Значения уже провалидированы хендлером.
func canonicalPlatforms(p []string) []string {
	if len(p) == 0 {
		return nil
	}
	out := make([]string, len(p))
	copy(out, p)
	sort.Strings(out)
	return out
}

// ruleKey — стабильный ключ правила (rule_type + software_name + отсортированные платформы).
// software_name без управляющих символов (санитайзер хендлера), платформы из фикс-набора —
// поэтому \x00 как разделитель безопасен.
func ruleKey(name, ruleType string, platforms []string) string {
	return ruleType + "\x00" + name + "\x00" + strings.Join(canonicalPlatforms(platforms), ",")
}

// GetPolicyDeclaration возвращает текущую (последнюю применённую) декларацию или nil, если ни
// одна ещё не сохранена (source-of-truth не задан).
func (db *DB) GetPolicyDeclaration(ctx context.Context) (*PolicyDeclaration, error) {
	var (
		d   PolicyDeclaration
		raw []byte
	)
	err := db.pool.QueryRow(ctx, `
  SELECT id, content, rule_count, created, deleted, applied_by, applied_at
  FROM policy_declaration ORDER BY applied_at DESC LIMIT 1`).
		Scan(&d.ID, &raw, &d.RuleCount, &d.Created, &d.Deleted, &d.AppliedBy, &d.AppliedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, &d.Content); err != nil {
		return nil, err
	}
	if d.Content == nil {
		d.Content = []DesiredPolicyRule{}
	}
	return &d, nil
}

// listGlobalPolicyRules читает живые ГЛОБАЛЬНЫЕ software-правила (без device/group scope) —
// именно их реконсилит декларация. Отдаёт как DesiredPolicyRule (scope не нужен).
func listGlobalPolicyRules(ctx context.Context, q pgx.Tx) (map[string]DesiredPolicyRule, error) {
	rows, err := q.Query(ctx, `
  SELECT software_name, rule_type, platforms
  FROM software_policy_rules
  WHERE device_id IS NULL AND group_id IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[string]DesiredPolicyRule{}
	for rows.Next() {
		var r DesiredPolicyRule
		if err := rows.Scan(&r.SoftwareName, &r.RuleType, &r.Platforms); err != nil {
			return nil, err
		}
		live[ruleKey(r.SoftwareName, r.RuleType, r.Platforms)] = r
	}
	return live, rows.Err()
}

// dedupDesired схлопывает повторы в декларации по каноничному ключу (две одинаковые записи —
// одно желаемое правило).
func dedupDesired(rules []DesiredPolicyRule) map[string]DesiredPolicyRule {
	desired := map[string]DesiredPolicyRule{}
	for _, r := range rules {
		desired[ruleKey(r.SoftwareName, r.RuleType, r.Platforms)] = r
	}
	return desired
}

// diffDesiredLive считает дрейф между желаемым (декларация) и живым набором. ToCreate/ToDelete
// в стабильном порядке (по ключу) для детерминированного ответа.
func diffDesiredLive(desired, live map[string]DesiredPolicyRule) PolicyDrift {
	d := PolicyDrift{ToCreate: []DesiredPolicyRule{}, ToDelete: []DesiredPolicyRule{}}
	var createKeys, deleteKeys []string
	for k := range desired {
		if _, ok := live[k]; ok {
			d.InSync++
		} else {
			createKeys = append(createKeys, k)
		}
	}
	for k := range live {
		if _, ok := desired[k]; !ok {
			deleteKeys = append(deleteKeys, k)
		}
	}
	sort.Strings(createKeys)
	sort.Strings(deleteKeys)
	for _, k := range createKeys {
		d.ToCreate = append(d.ToCreate, desired[k])
	}
	for _, k := range deleteKeys {
		d.ToDelete = append(d.ToDelete, live[k])
	}
	return d
}

// PolicyDriftAgainstSaved считает дрейф живых глобальных правил против СОХРАНЁННОЙ декларации
// (без применения). Если декларация ещё не задана — дрейфа нет (пустой результат): source-of-
// truth отсутствует, сравнивать не с чем (иначе «удалить всё живое» вводило бы в заблуждение).
func (db *DB) PolicyDriftAgainstSaved(ctx context.Context) (*PolicyDrift, error) {
	decl, err := db.GetPolicyDeclaration(ctx)
	if err != nil {
		return nil, err
	}
	if decl == nil {
		return &PolicyDrift{ToCreate: []DesiredPolicyRule{}, ToDelete: []DesiredPolicyRule{}}, nil
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	live, err := listGlobalPolicyRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	d := diffDesiredLive(dedupDesired(decl.Content), live)
	return &d, nil
}

// ApplyPolicyDeclaration сохраняет декларацию как source-of-truth И реконсилит живые глобальные
// software-правила: создаёт недостающие, удаляет лишние — чтобы БД точно соответствовала
// декларации. Всё в ОДНОЙ транзакции. Возвращает (created, deleted). rules должны быть уже
// провалидированы/нормализованы хендлером (sanitizeSoftwareName, rule_type, платформы).
func (db *DB) ApplyPolicyDeclaration(ctx context.Context, rules []DesiredPolicyRule, appliedBy string) (created, deleted int, err error) {
	if rules == nil {
		rules = []DesiredPolicyRule{}
	}
	desired := dedupDesired(rules)

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)

	live, err := listGlobalPolicyRules(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	drift := diffDesiredLive(desired, live)

	// Создаём недостающие: глобальное правило (device_id/group_id = NULL). Пустой набор
	// платформ → NULL (правило на всех платформах), как в CreatePolicyRule.
	for _, r := range drift.ToCreate {
		var plat interface{}
		if len(r.Platforms) > 0 {
			plat = r.Platforms
		}
		if _, err := tx.Exec(ctx, `
  INSERT INTO software_policy_rules (software_name, rule_type, platforms)
  VALUES ($1, $2, $3)`, r.SoftwareName, r.RuleType, plat); err != nil {
			return 0, 0, err
		}
	}

	// Удаляем лишние: только глобальные правила, которых нет в декларации. Матчим по
	// (software_name, rule_type) + сравнение платформ в приложении здесь не нужно — удаляем
	// по каноничному совпадению, поэтому чистим точечно по строкам, попавшим в ToDelete.
	for _, r := range drift.ToDelete {
		var plat interface{}
		if len(r.Platforms) > 0 {
			plat = r.Platforms
		}
		// platforms сравниваем как массив: NULL-безопасно через IS NOT DISTINCT FROM.
		if _, err := tx.Exec(ctx, `
  DELETE FROM software_policy_rules
  WHERE device_id IS NULL AND group_id IS NULL
    AND software_name = $1 AND rule_type = $2
    AND platforms IS NOT DISTINCT FROM $3::text[]`, r.SoftwareName, r.RuleType, plat); err != nil {
			return 0, 0, err
		}
	}

	content, err := json.Marshal(rules)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `
  INSERT INTO policy_declaration (content, rule_count, created, deleted, applied_by)
  VALUES ($1, $2, $3, $4, $5)`,
		content, len(desired), len(drift.ToCreate), len(drift.ToDelete), appliedBy); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return len(drift.ToCreate), len(drift.ToDelete), nil
}
