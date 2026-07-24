package storage

import (
	"context"
	"fmt"
	"math"
	"time"
)

// ComplianceCheck — результат ОДНОЙ проверки соответствия. Passed/Total — сколько
// объектов (устройств/аккаунтов/правил) в области проверки её проходит; Status —
// светофор поверх доли (pass/warn/fail), Detail — человекочитаемое пояснение с числами.
// Проверки НЕ вводят новых данных от агента: агрегируют то, что уже есть (инвентарь,
// статусы устройств, MFA пользователей, политики, журнал аудита).
type ComplianceCheck struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"` // CIS / SOC2 / access / inventory / audit
	Status   string `json:"status"`   // pass / warn / fail
	Passed   int    `json:"passed"`
	Total    int    `json:"total"`
	Detail   string `json:"detail"`
}

// ComplianceReport — агрегированный отчёт соответствия: общий скор (0..100) + набор
// проверок. Score — взвешенное среднее доли (Passed/Total) по проверкам, у которых есть
// что оценивать (Total>0); проверки без области действия (пустой парк, аудит не настроен)
// в скор не входят, поэтому пустая БД даёт Score=100, а не деление на ноль.
type ComplianceReport struct {
	Score       int               `json:"score"`
	GeneratedAt time.Time         `json:"generated_at"`
	Checks      []ComplianceCheck `json:"checks"`
}

// complianceStatus переводит долю Passed/Total в светофор. Порог passAt — «зелёный»,
// warnAt — «жёлтый», ниже — «красный». Total==0 (нечего проверять) считаем pass:
// отсутствие объектов не является нарушением (0 устройств → не «fail»).
func complianceStatus(passed, total int, passAt, warnAt float64) string {
	if total == 0 {
		return "pass"
	}
	r := float64(passed) / float64(total)
	switch {
	case r >= passAt:
		return "pass"
	case r >= warnAt:
		return "warn"
	default:
		return "fail"
	}
}

// compliancePct — доля в процентах (0..100); при Total==0 возвращает 100 (нечего
// нарушать), чтобы детали не показывали NaN и не делили на ноль.
func compliancePct(passed, total int) int {
	if total == 0 {
		return 100
	}
	return int(math.Round(float64(passed) / float64(total) * 100))
}

// complianceSpec — внутренняя обёртка: сама проверка + её вес в общем скоре. Вес не
// сериализуется (score уже посчитан), поэтому держим его отдельно от ComplianceCheck.
type complianceSpec struct {
	check  ComplianceCheck
	weight int
}

// ComplianceReport собирает отчёт соответствия из УЖЕ существующих данных. Каждая
// проверка — один SQL-агрегат (COUNT ... FILTER), парк в память не тянется. Мягкие
// проверки (шифрование, check-in) допускают порог; критичные (MFA админов) требуют 100%.
// Проверки политик переиспользуют ListSoftwarePolicyCompliance/ListScriptPolicyCompliance,
// чтобы UI compliance и страницы политик считали одно и то же.
func (db *DB) ComplianceReport(ctx context.Context) (ComplianceReport, error) {
	rep := ComplianceReport{GeneratedAt: time.Now().UTC()}

	// pair выполняет запрос вида SELECT <passed>, <total> и возвращает пару счётчиков.
	pair := func(query string) (passed, total int, err error) {
		err = db.pool.QueryRow(ctx, query).Scan(&passed, &total)
		return
	}

	var specs []complianceSpec
	add := func(id, title, category string, passed, total int, passAt, warnAt float64, weight int, detail string) {
		specs = append(specs, complianceSpec{
			check: ComplianceCheck{
				ID:       id,
				Title:    title,
				Category: category,
				Status:   complianceStatus(passed, total, passAt, warnAt),
				Passed:   passed,
				Total:    total,
				Detail:   detail,
			},
			weight: weight,
		})
	}

	// ── Идентичность / доступ (SOC2) ─────────────────────────────────────────
	// it_admin без 2FA — критичная находка: любой такой аккаунт валит проверку в fail
	// (passAt=warnAt=1.0). Область — только console-роли (validRoles), сервисные токены
	// (api_tokens) сюда не входят: у них своя аутентификация, не пароль+TOTP.
	p, tot, err := pair(`SELECT count(*) FILTER (WHERE totp_enabled), count(*) FROM users WHERE role = 'it_admin' AND is_active`)
	if err != nil {
		return rep, err
	}
	add("admin_mfa", "Admin accounts with MFA enabled", "SOC2", p, tot, 1.0, 1.0, 3,
		fmt.Sprintf("%d of %d it_admin accounts have TOTP MFA enabled (%d%%).", p, tot, compliancePct(p, tot)))

	// Более широкий охват: все console-пользователи (it_admin + viewer) c MFA. Не
	// критично (viewer только читает) — мягкий порог, но низкая доля тянет скор вниз.
	p, tot, err = pair(`SELECT count(*) FILTER (WHERE totp_enabled), count(*) FROM users WHERE role IN ('it_admin','viewer') AND is_active`)
	if err != nil {
		return rep, err
	}
	add("mfa_adoption", "Console users with MFA enabled", "SOC2", p, tot, 0.9, 0.6, 1,
		fmt.Sprintf("%d of %d console users have MFA enabled (%d%%).", p, tot, compliancePct(p, tot)))

	// ── Устройства (CIS) ─────────────────────────────────────────────────────
	// Шифрование системного тома (FileVault/BitLocker/LUKS). disk_encryption приходит от
	// агента ('enabled'/'disabled'/''); '' («не знаю») в passed не попадает — считаем
	// подтверждённо зашифрованные. Область — только active-устройства.
	p, tot, err = pair(`SELECT count(*) FILTER (WHERE lower(coalesce(disk_encryption,'')) = 'enabled'), count(*) FROM devices WHERE status = 'active'`)
	if err != nil {
		return rep, err
	}
	add("disk_encryption", "Full-disk encryption on active devices", "CIS", p, tot, 0.9, 0.75, 2,
		fmt.Sprintf("%d of %d active devices report full-disk encryption enabled (%d%%).", p, tot, compliancePct(p, tot)))

	// Устройства «на связи»: видевшиеся за 14 дней. Давно не выходившие на связь —
	// возможно потерянные/выведенные де-факто, но всё ещё active в базе.
	p, tot, err = pair(`SELECT count(*) FILTER (WHERE last_seen_at >= now() - interval '14 days'), count(*) FROM devices WHERE status = 'active'`)
	if err != nil {
		return rep, err
	}
	add("device_checkin", "Active devices seen in the last 14 days", "access", p, tot, 0.95, 0.8, 1,
		fmt.Sprintf("%d of %d active devices checked in within 14 days (%d%%).", p, tot, compliancePct(p, tot)))

	// Владелец назначен: инвентарная гигиена — устройство без owner_id ни с кем не
	// связано (некому эскалировать инцидент).
	p, tot, err = pair(`SELECT count(*) FILTER (WHERE owner_id IS NOT NULL), count(*) FROM devices WHERE status = 'active'`)
	if err != nil {
		return rep, err
	}
	add("device_ownership", "Active devices with an assigned owner", "inventory", p, tot, 0.9, 0.7, 1,
		fmt.Sprintf("%d of %d active devices have an assigned owner (%d%%).", p, tot, compliancePct(p, tot)))

	// Очередь энроллмента не залежалась: pending/pending_approval не старше 7 дней.
	// passed = свежие заявки; старые нерешённые — жёлтый/красный.
	p, tot, err = pair(`SELECT count(*) FILTER (WHERE created_at >= now() - interval '7 days'), count(*) FROM devices WHERE status IN ('pending','pending_approval')`)
	if err != nil {
		return rep, err
	}
	add("enrollment_backlog", "Enrollment requests resolved within 7 days", "access", p, tot, 1.0, 0.8, 1,
		fmt.Sprintf("%d of %d pending enrollments are younger than 7 days (%d stale).", p, tot, tot-p))

	// Заявки на admin-права не зависли: pending, не прошедшие свой pending_expires_at.
	// Просроченная, но всё ещё pending заявка = таймер истечения не отработал.
	p, tot, err = pair(`SELECT count(*) FILTER (WHERE pending_expires_at >= now()), count(*) FROM admin_access_requests WHERE status = 'pending'`)
	if err != nil {
		return rep, err
	}
	add("stale_admin_access", "Pending admin-access requests within their timeout", "SOC2", p, tot, 1.0, 0.9, 1,
		fmt.Sprintf("%d of %d pending admin-access requests are still within their approval window (%d overdue).", p, tot, tot-p))

	// ── Инвентарь / политики ─────────────────────────────────────────────────
	// Соответствие софт-политикам: доля (устройство × forbidden-правило) без нарушения.
	// Переиспользуем агрегат ListSoftwarePolicyCompliance (тот же скоуп, что у агента),
	// суммируя лишь проверяемые правила (Checked = forbidden; allowed не проверяется).
	swRows, err := db.ListSoftwarePolicyCompliance(ctx)
	if err != nil {
		return rep, err
	}
	var swPass, swScope int
	for _, r := range swRows {
		if r.Checked {
			swScope += r.InScope
			swPass += r.Pass
		}
	}
	add("software_policy", "No forbidden software across the fleet", "inventory", swPass, swScope, 1.0, 0.9, 2,
		fmt.Sprintf("%d of %d device checks against forbidden-software policies pass (%d violation(s)).", swPass, swScope, swScope-swPass))

	// Соответствие скрипт-политикам: доля назначенных устройств с успешным ПОСЛЕДНИМ
	// прогоном. Unknown (ещё не отчитались) в passed не входит — не рисуем зелёный тем,
	// кто не проверялся.
	scRows, err := db.ListScriptPolicyCompliance(ctx)
	if err != nil {
		return rep, err
	}
	var scPass, scScope, scUnknown int
	for _, r := range scRows {
		scScope += r.InScope
		scPass += r.Pass
		scUnknown += r.Unknown
	}
	add("script_policy", "Script policies passing on assigned devices", "inventory", scPass, scScope, 0.9, 0.7, 1,
		fmt.Sprintf("%d of %d assigned device-policy checks pass on their latest run (%d not yet reported).", scPass, scScope, scUnknown))

	// ── Аудит ────────────────────────────────────────────────────────────────
	// Tamper-evident журнал: настроен (ROUTINEOPS_AUDIT_HMAC_KEY) и цепочка цела.
	// Не настроен — рекомендация (warn), но в скор НЕ входит (Total=0, weight=0): open-core
	// без ключа не должен уходить в 0. Настроен и нарушен — fail, вес в скоре.
	ai, err := db.VerifyAuditIntegrity(ctx, 0)
	if err != nil {
		return rep, err
	}
	switch {
	case !ai.Configured:
		specs = append(specs, complianceSpec{
			check: ComplianceCheck{
				ID: "audit_tamper_evident", Title: "Tamper-evident audit log", Category: "audit",
				Status: "warn", Passed: 0, Total: 0,
				Detail: "Audit-log signing is not configured (set ROUTINEOPS_AUDIT_HMAC_KEY to enable tamper-evidence).",
			},
			weight: 0,
		})
	case ai.Tampered:
		detail := fmt.Sprintf("Audit-log integrity check FAILED after verifying %d entries.", ai.Checked)
		if ai.TailTruncated {
			detail = fmt.Sprintf("Audit-log integrity check FAILED: tail truncated (%d entries verified).", ai.Checked)
		} else if ai.FirstTamperedSeq > 0 {
			detail = fmt.Sprintf("Audit-log integrity check FAILED at seq %d (%d entries verified).", ai.FirstTamperedSeq, ai.Checked)
		}
		specs = append(specs, complianceSpec{
			check: ComplianceCheck{
				ID: "audit_tamper_evident", Title: "Tamper-evident audit log", Category: "audit",
				Status: "fail", Passed: 0, Total: 1, Detail: detail,
			},
			weight: 2,
		})
	default:
		specs = append(specs, complianceSpec{
			check: ComplianceCheck{
				ID: "audit_tamper_evident", Title: "Tamper-evident audit log", Category: "audit",
				Status: "pass", Passed: 1, Total: 1,
				Detail: fmt.Sprintf("Audit-log signing configured; hash chain intact (%d entries verified).", ai.Checked),
			},
			weight: 2,
		})
	}

	// ── Общий скор ───────────────────────────────────────────────────────────
	// Взвешенное среднее доли по проверкам с областью действия (Total>0, weight>0).
	// Ни одной оцениваемой проверки (пустая БД) → 100: нечему падать.
	var acc, wsum float64
	for _, s := range specs {
		rep.Checks = append(rep.Checks, s.check)
		if s.check.Total > 0 && s.weight > 0 {
			acc += float64(s.weight) * float64(s.check.Passed) / float64(s.check.Total)
			wsum += float64(s.weight)
		}
	}
	if wsum == 0 {
		rep.Score = 100
	} else {
		rep.Score = int(math.Round(acc / wsum * 100))
	}
	return rep, nil
}
