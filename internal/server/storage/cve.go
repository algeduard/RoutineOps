package storage

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// CVE-сканирование: матчинг инвентаря установленного ПО (device_software) против фида
// известных уязвимостей (cve_feed). Источник данных — уже собираемый инвентарь, нового
// сбора от агента нет. Фид заливает деплойер (LoadCVEFeed), скан пересобирает cve_findings.

// CVEFeedEntry — запись фида уязвимостей (одна пара CVE×продукт с ограничением версии).
// Приезжает JSON-массивом в POST /cve/feed. CVSS/PublishedAt опциональны (nil = не задано).
type CVEFeedEntry struct {
	CVEID             string     `json:"cve_id"`
	Product           string     `json:"product"`
	VersionConstraint string     `json:"version_constraint"`
	Severity          string     `json:"severity"`
	CVSS              *float64   `json:"cvss,omitempty"`
	Summary           string     `json:"summary"`
	PublishedAt       *time.Time `json:"published_at,omitempty"`
}

// CVEFinding — уязвимость, найденная на устройстве последним сканом. product/installed_version
// — то, что реально стоит на машине (из device_software), а не имя из фида.
type CVEFinding struct {
	ID               string    `json:"id"`
	DeviceID         string    `json:"device_id"`
	Hostname         string    `json:"hostname"`
	CVEID            string    `json:"cve_id"`
	Product          string    `json:"product"`
	InstalledVersion string    `json:"installed_version"`
	Severity         string    `json:"severity"`
	CVSS             *float64  `json:"cvss,omitempty"`
	DetectedAt       time.Time `json:"detected_at"`
}

// CVESeverityCount / CVEDeviceCount / CVESummary — сводка по парку для страницы CVE.
type CVESeverityCount struct {
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

type CVEDeviceCount struct {
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname"`
	Count    int    `json:"count"`
	Critical int    `json:"critical"`
	High     int    `json:"high"`
}

type CVESummary struct {
	TotalFindings   int                `json:"total_findings"`
	AffectedDevices int                `json:"affected_devices"`
	FeedCount       int                `json:"feed_count"`
	BySeverity      []CVESeverityCount `json:"by_severity"`
	ByDevice        []CVEDeviceCount   `json:"by_device"`
}

// LoadCVEFeed ЗАМЕНЯЕТ фид целиком (снос + заливка в одной транзакции) — идемпотентная
// загрузка снапшота выгрузки NVD/OSV. Строки без cve_id/product пропускаются (для матчинга
// бесполезны). Возвращает число реально загруженных записей.
func (db *DB) LoadCVEFeed(ctx context.Context, entries []CVEFeedEntry) (int, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM cve_feed`); err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		cveID := strings.TrimSpace(e.CVEID)
		product := strings.TrimSpace(e.Product)
		if cveID == "" || product == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO cve_feed (cve_id, product, version_constraint, severity, cvss, summary, published_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, cveID, product, strings.TrimSpace(e.VersionConstraint), normalizeCVESeverity(e.Severity),
			e.CVSS, strings.TrimSpace(e.Summary), e.PublishedAt); err != nil {
			return 0, err
		}
		count++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

// ClearCVEFeed очищает фид. Возвращает число удалённых строк. Находки не трогает — они
// пересоберутся (в пустой набор) при следующем скане.
func (db *DB) ClearCVEFeed(ctx context.Context) (int64, error) {
	tag, err := db.pool.Exec(ctx, `DELETE FROM cve_feed`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CVEFeedCount — размер фида (для сводки/статуса на странице).
func (db *DB) CVEFeedCount(ctx context.Context) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx, `SELECT count(*) FROM cve_feed`).Scan(&n)
	return n, err
}

// ScanCVE пересобирает cve_findings: матчит инвентарь против фида и заменяет находки целиком.
//
// Кандидаты берём одним JOIN'ом (НЕ N+1): device_software × cve_feed по нормализованному
// product — регистронезависимая ПОДСТРОКА (та же конвенция, что findForbidden у агента и
// ListSoftwarePolicyCompliance). Ограничение честно: короткое product-имя может пере-
// совпасть (фид "Java" зацепит инвентарную "JavaScript ..."), поэтому деплойер задаёт
// специфичные имена продуктов. Pending-устройства исключаем: их инвентарь не подтверждён
// сертификатом (как в compliance). Ограничение версии считаем в Go (version-логика ниже) —
// SQL для покомпонентного сравнения был бы громоздким и хрупким. На пустом парке/фиде JOIN
// пуст → находки просто обнуляются, без ошибки.
func (db *DB) ScanCVE(ctx context.Context) (int, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT s.device_id, s.software_name, COALESCE(s.version, ''),
		       f.cve_id, f.version_constraint, f.severity, f.cvss
		FROM device_software s
		JOIN devices d ON d.id = s.device_id AND d.status <> 'pending'
		JOIN cve_feed f
		  ON btrim(f.product) <> ''
		 AND strpos(lower(s.software_name), lower(btrim(f.product))) > 0`)
	if err != nil {
		return 0, err
	}
	type match struct {
		deviceID, cveID, product, installed, severity string
		cvss                                          *float64
	}
	var matches []match
	for rows.Next() {
		var deviceID, product, installed, cveID, constraint, severity string
		var cvss *float64
		if err := rows.Scan(&deviceID, &product, &installed, &cveID, &constraint, &severity, &cvss); err != nil {
			rows.Close()
			return 0, err
		}
		if cveVersionVulnerable(constraint, installed) {
			matches = append(matches, match{deviceID, cveID, product, installed, severity, cvss})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// Пересборка атомарно: сносим старые находки и заливаем новые COPY-ем (одна операция,
	// а не INSERT на строку). rows уже закрыты — соединение пула освобождено под транзакцию.
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM cve_findings`); err != nil {
		return 0, err
	}
	if len(matches) > 0 {
		if _, err := tx.CopyFrom(ctx,
			pgx.Identifier{"cve_findings"},
			[]string{"device_id", "cve_id", "product", "installed_version", "severity", "cvss"},
			pgx.CopyFromSlice(len(matches), func(i int) ([]any, error) {
				m := matches[i]
				return []any{m.deviceID, m.cveID, m.product, m.installed, m.severity, m.cvss}, nil
			})); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(matches), nil
}

// ListCVEFindings отдаёт находки с фильтрами device_id/severity (пустые = без фильтра).
// Порядок: critical→low, затем hostname/product/cve. device_id::text — чтобы мусор вместо
// UUID не давал 22P02 (конвенция GetScript), а просто ничего не находил.
func (db *DB) ListCVEFindings(ctx context.Context, deviceID, severity string) ([]CVEFinding, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT f.id, f.device_id, COALESCE(d.hostname, ''), f.cve_id, f.product,
		       f.installed_version, f.severity, f.cvss, f.detected_at
		FROM cve_findings f
		JOIN devices d ON d.id = f.device_id
		WHERE ($1 = '' OR f.device_id::text = $1)
		  AND ($2 = '' OR f.severity = $2)
		ORDER BY
		  CASE f.severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
		  lower(d.hostname), f.product, f.cve_id`, deviceID, severity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CVEFinding
	for rows.Next() {
		var f CVEFinding
		if err := rows.Scan(&f.ID, &f.DeviceID, &f.Hostname, &f.CVEID, &f.Product,
			&f.InstalledVersion, &f.Severity, &f.CVSS, &f.DetectedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CVESummaryData собирает сводку: размер фида, всего находок, разбивка по severity
// (фиксированный порядок critical→low, нули включены) и по затронутым устройствам.
func (db *DB) CVESummaryData(ctx context.Context) (CVESummary, error) {
	out := CVESummary{BySeverity: []CVESeverityCount{}, ByDevice: []CVEDeviceCount{}}

	feedCount, err := db.CVEFeedCount(ctx)
	if err != nil {
		return out, err
	}
	out.FeedCount = feedCount

	sevRows, err := db.pool.Query(ctx, `SELECT severity, count(*) FROM cve_findings GROUP BY severity`)
	if err != nil {
		return out, err
	}
	sevMap := map[string]int{}
	for sevRows.Next() {
		var s string
		var c int
		if err := sevRows.Scan(&s, &c); err != nil {
			sevRows.Close()
			return out, err
		}
		sevMap[s] = c
		out.TotalFindings += c
	}
	sevRows.Close()
	if err := sevRows.Err(); err != nil {
		return out, err
	}
	for _, s := range []string{"critical", "high", "medium", "low"} {
		out.BySeverity = append(out.BySeverity, CVESeverityCount{Severity: s, Count: sevMap[s]})
	}

	devRows, err := db.pool.Query(ctx, `
		SELECT f.device_id, COALESCE(d.hostname, ''), count(*),
		       count(*) FILTER (WHERE f.severity = 'critical'),
		       count(*) FILTER (WHERE f.severity = 'high')
		FROM cve_findings f
		JOIN devices d ON d.id = f.device_id
		GROUP BY f.device_id, d.hostname
		ORDER BY count(*) FILTER (WHERE f.severity = 'critical') DESC,
		         count(*) FILTER (WHERE f.severity = 'high') DESC,
		         count(*) DESC, lower(d.hostname)`)
	if err != nil {
		return out, err
	}
	defer devRows.Close()
	for devRows.Next() {
		var d CVEDeviceCount
		if err := devRows.Scan(&d.DeviceID, &d.Hostname, &d.Count, &d.Critical, &d.High); err != nil {
			return out, err
		}
		out.ByDevice = append(out.ByDevice, d)
	}
	if err := devRows.Err(); err != nil {
		return out, err
	}
	out.AffectedDevices = len(out.ByDevice)
	return out, nil
}

// normalizeCVESeverity приводит severity к одному из четырёх значений (дефолт medium).
func normalizeCVESeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return "low"
	case "high":
		return "high"
	case "critical":
		return "critical"
	default:
		return "medium"
	}
}

// cveVersionVulnerable сообщает, попадает ли установленная версия под ограничение фида.
// Схема MVP (см. миграцию 049): пусто или "*" — любая версия; "<","<=",">",">=","=" + версия;
// голое "X.Y.Z" трактуется как "=X.Y.Z". Версия неизвестна (пусто) и ограничение конкретное —
// находку НЕ выдумываем (иначе ложные срабатывания на пустой version инвентаря).
func cveVersionVulnerable(constraint, installed string) bool {
	c := strings.TrimSpace(constraint)
	if c == "" || c == "*" {
		return true
	}
	op, ver := parseCVEConstraint(c)
	installed = strings.TrimSpace(installed)
	if installed == "" || ver == "" {
		return false
	}
	cmp := compareVersions(installed, ver)
	switch op {
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	default: // "="
		return cmp == 0
	}
}

// parseCVEConstraint разбирает "<=1.2.3" → ("<=", "1.2.3"). Без оператора — точное равенство.
func parseCVEConstraint(c string) (op, ver string) {
	for _, p := range []string{"<=", ">=", "<", ">", "="} {
		if strings.HasPrefix(c, p) {
			return p, strings.TrimSpace(c[len(p):])
		}
	}
	return "=", c
}

// compareVersions сравнивает версии покомпонентно (semver-подобно): у каждого компонента
// берётся ведущее целое ("120.0.1-beta" → [120,0,1]), недостающие компоненты = 0
// ("1.2" == "1.2.0"). Возвращает -1/0/1. Достаточно для MVP; полноценный semver с
// pre-release-упорядочиванием — follow-up.
func compareVersions(a, b string) int {
	as, bs := splitVersion(a), splitVersion(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(s string) []int {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i] = leadingInt(p)
	}
	return out
}

// leadingInt берёт ведущие цифры компонента ("0rc1" → 0, "17" → 17, "" → 0).
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
