package storage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Авто-устранение запрещённого ПО (auto-remediation, enterprise-фича). Детект нарушений — по
// ИНВЕНТАРЮ (device_software) против forbidden-правил (software_policy_rules), тем же матчером
// (регистронезависимая подстрока), что ListSoftwarePolicyCompliance и findForbidden у агента.
// Устранение ПЕРЕИСПОЛЬЗУЕТ существующий путь удаления ПО (CreateRemoveSoftwareTask →
// task_type='remove_software' → worker-доставка), новый механизм деинсталляции здесь НЕ пишется.

// AutoRemediationConfig — singleton-конфиг авто-устранения. Enabled ПО УМОЛЧАНИЮ false
// (авто-удаление деструктивно). DryRun — режим обкатки: нарушения только логируются, задачи
// удаления не создаются.
type AutoRemediationConfig struct {
	Enabled   bool      `json:"enabled"`
	DryRun    bool      `json:"dry_run"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetAutoRemediationConfig отдаёт конфиг. Пустой (Enabled=false, DryRun=false) — если строки
// ещё нет (фичу ни разу не настраивали): дефолт безопасный — авто-устранение выключено.
func (db *DB) GetAutoRemediationConfig(ctx context.Context) (AutoRemediationConfig, error) {
	var c AutoRemediationConfig
	err := db.pool.QueryRow(ctx, `
		SELECT enabled, dry_run, updated_at
		FROM auto_remediation_config WHERE id = true`).
		Scan(&c.Enabled, &c.DryRun, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return AutoRemediationConfig{}, nil
		}
		return AutoRemediationConfig{}, err
	}
	return c, nil
}

// SetAutoRemediationConfig апсертит singleton-конфиг (id=true). Управляет тем, включено ли
// авто-устранение и работает ли оно в dry_run.
func (db *DB) SetAutoRemediationConfig(ctx context.Context, enabled, dryRun bool) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO auto_remediation_config (id, enabled, dry_run, updated_at)
		VALUES (true, $1, $2, now())
		ON CONFLICT (id) DO UPDATE SET
			enabled = $1,
			dry_run = $2,
			updated_at = now()
	`, enabled, dryRun)
	return err
}

// ForbiddenViolation — активное устройство с УСТАНОВЛЕННЫМ ПО, нарушающим forbidden-правило.
// SoftwareName/Version — фактическая запись инвентаря (её и передаём в задачу удаления), а не
// имя-паттерн из правила.
type ForbiddenViolation struct {
	DeviceID     string `json:"device_id"`
	Hostname     string `json:"hostname"`
	SoftwareName string `json:"software_name"`
	Version      string `json:"version"`
}

// ListForbiddenViolations возвращает нарушения forbidden-политик по инвентарю: пары
// (устройство, установленное ПО), где ПО подпадает под forbidden-правило в области действия
// устройства. Только устройства в статусе 'active': задачу удаления имеет смысл ставить лишь
// на машину, которая её примет и выполнит (парный гейт к software_removal_handler).
//
// Область действия и матчер повторяют ListSoftwarePolicyDeviceCompliance: глобальное правило
// (device_id и group_id пусты) ∪ device-оверрайд ∪ правила групп устройства, платформенный
// фильтр с тем же fail-safe (ОС неизвестна → не фильтруем), имя — регистронезависимая
// подстрока. DISTINCT: одно и то же ПО может подпасть под несколько правил — нарушение одно.
// Дедуп по уже висящим задачам здесь НЕ делаем — это ответственность вызывающего (ремедиатора),
// чтобы dry_run и реальный режим решали про задачи по-разному.
func (db *DB) ListForbiddenViolations(ctx context.Context) ([]ForbiddenViolation, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT DISTINCT d.id, d.hostname, s.software_name, COALESCE(s.version, '')
		FROM software_policy_rules r
		JOIN devices d
		  ON d.status = 'active'
		 AND (
		       (r.device_id IS NULL AND r.group_id IS NULL)   -- глобальное
		    OR d.id = r.device_id                             -- оверрайд устройства
		    OR (r.group_id IS NOT NULL AND EXISTS (           -- группа устройства
		          SELECT 1 FROM device_group_members gm
		          WHERE gm.device_id = d.id AND gm.group_id = r.group_id))
		     )
		 AND (
		       r.platforms IS NULL OR cardinality(r.platforms) = 0
		    OR COALESCE(d.os, '') = '' OR lower(d.os) = 'unknown'
		    OR (CASE
		          WHEN lower(d.os) LIKE '%win%' THEN 'Windows'
		          WHEN lower(d.os) LIKE '%mac%' OR lower(d.os) LIKE '%darwin%' THEN 'macOS'
		          ELSE 'Linux'
		        END) = ANY (r.platforms)
		     )
		JOIN device_software s
		  ON s.device_id = d.id
		 AND r.software_name <> ''
		 AND strpos(lower(s.software_name), lower(r.software_name)) > 0
		WHERE r.rule_type = 'forbidden'
		ORDER BY d.hostname, s.software_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ForbiddenViolation
	for rows.Next() {
		var v ForbiddenViolation
		if err := rows.Scan(&v.DeviceID, &v.Hostname, &v.SoftwareName, &v.Version); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// HasOpenRemoveSoftwareTask сообщает, есть ли уже НЕЗАВЕРШЁННАЯ (pending/acked) задача удаления
// этого ПО с этого устройства. Дедуп ремедиатора: пока прежняя remove_software-задача не
// доставлена/не выполнена, новую не создаём — иначе на каждом тике плодились бы дубли (спам
// задачами) до того, как агент успеет удалить ПО и переслать инвентарь без него.
func (db *DB) HasOpenRemoveSoftwareTask(ctx context.Context, deviceID, softwareName string) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM tasks
			WHERE device_id = $1
			  AND task_type = 'remove_software'
			  AND software_name = $2
			  AND status IN ('pending', 'acked')
		)`, deviceID, softwareName).Scan(&exists)
	return exists, err
}

// HasDryRunRemediationLog сообщает, логировали ли мы уже dry_run по этой паре (устройство, ПО).
// Дедуп dry_run-логирования: без него ремедиатор писал бы одну и ту же «удалил бы» строку на
// каждом тике, пока нарушение висит. Реальный режим этот дедуп не использует (там дедуп — по
// незавершённой задаче).
func (db *DB) HasDryRunRemediationLog(ctx context.Context, deviceID, softwareName string) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM auto_remediation_log
			WHERE device_id = $1 AND software_name = $2 AND action = 'dry_run'
		)`, deviceID, softwareName).Scan(&exists)
	return exists, err
}

// HasRecentRemovalRemediation сообщает, ставили ли мы РЕАЛЬНУЮ задачу удаления по этой паре
// (устройство, ПО) не раньше since. Это cooldown-дедуп реального режима В ДОПОЛНЕНИЕ к дедупу по
// открытой задаче (HasOpenRemoveSoftwareTask). Зачем нужен: remove_software-задача уходит в
// ТЕРМИНАЛЬНЫЙ статус (completed/failed) сразу после доставки, а device_software чистится лишь
// СЛЕДУЮЩИМ отчётом инвентаря агента И ЛИШЬ если удаление реально удалось. Для ПО, которое
// удалить нельзя (non-Windows removeSoftware всегда возвращает ошибку → задача failed; Windows-
// приложение без UninstallString), нарушение висит в инвентаре вечно, дедуп по pending/acked
// больше не матчит — и без этого cooldown ремедиатор плодил бы новую задачу и строку лога на
// КАЖДОМ тике (5 мин) бесконечно. С cooldown повторная попытка — не чаще раза в окно.
func (db *DB) HasRecentRemovalRemediation(ctx context.Context, deviceID, softwareName string, since time.Time) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM auto_remediation_log
			WHERE device_id = $1 AND software_name = $2 AND action = 'removed'
			  AND created_at >= $3
		)`, deviceID, softwareName, since).Scan(&exists)
	return exists, err
}

// RemediationLogEntry — строка лога ремедиаций (GET /auto-remediation/log). TaskID пуст у
// dry_run-записей (задача не создавалась).
type RemediationLogEntry struct {
	ID           string    `json:"id"`
	DeviceID     string    `json:"device_id"`
	Hostname     string    `json:"hostname"`
	SoftwareName string    `json:"software_name"`
	TaskID       string    `json:"task_id"`
	Action       string    `json:"action"` // 'removed' | 'dry_run'
	CreatedAt    time.Time `json:"created_at"`
}

// AddRemediationLog добавляет запись в лог ремедиаций. taskID пустой ("") пишется как NULL
// (dry_run — задачи нет). Возвращает созданную строку (без hostname — вызывающему обычно не
// нужен, GET /auto-remediation/log джойнит его отдельно).
func (db *DB) AddRemediationLog(ctx context.Context, deviceID, softwareName, taskID, action string) (RemediationLogEntry, error) {
	var tid *string
	if taskID != "" {
		tid = &taskID
	}
	var e RemediationLogEntry
	err := db.pool.QueryRow(ctx, `
		INSERT INTO auto_remediation_log (device_id, software_name, task_id, action)
		VALUES ($1, $2, $3, $4)
		RETURNING id, device_id, software_name, COALESCE(task_id::text, ''), action, created_at
	`, deviceID, softwareName, tid, action).
		Scan(&e.ID, &e.DeviceID, &e.SoftwareName, &e.TaskID, &e.Action, &e.CreatedAt)
	return e, err
}

// ListRemediationLog отдаёт лог ремедиаций (последние сверху), с hostname устройства. limit
// нормализуется в разумные пределы.
func (db *DB) ListRemediationLog(ctx context.Context, limit int) ([]RemediationLogEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.pool.Query(ctx, `
		SELECT l.id, l.device_id, COALESCE(d.hostname, ''), l.software_name,
		       COALESCE(l.task_id::text, ''), l.action, l.created_at
		FROM auto_remediation_log l
		LEFT JOIN devices d ON d.id = l.device_id
		ORDER BY l.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemediationLogEntry
	for rows.Next() {
		var e RemediationLogEntry
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.Hostname, &e.SoftwareName,
			&e.TaskID, &e.Action, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
