package storage

import "context"

// Мультитенантный per-query scoping. Актора привязывает к тенанту API-слой (jwtMiddleware),
// кладя его tenant_id в контекст запроса через WithTenantScope; tenant-aware storage-методы
// читают его из ctx (scopeParam) и подмешивают в SQL единый предикат
//
//	AND ($k::uuid IS NULL OR <col> = $k::uuid)
//
// где $k = scopeParam(ctx): nil для «провайдера» (матчит ВСЕ тенанты) или tenant_id
// изолированного тенанта (матчит только его строки). Предикат структурно одинаков при любом
// режиме — планировщик коротко замыкает `NULL IS NULL`, — поэтому запрос не ветвится в Go.
//
// МОДЕЛЬ ДОСТУПА. Default-тенант (DefaultTenantID) — «провайдер»: акторы в нём НЕ скоупятся и
// видят все тенанты (провижининг, назначение устройств/юзеров, кросс-тенантные отчёты). Это ещё
// и полная обратная совместимость: в одно-организационном деплое ВСЕ сущности и пользователи
// лежат в Default → scopeParam=nil → поведение ровно как до фичи. Актор НЕ-Default тенанта
// скоупится жёстко: читает только свои строки, а мутация чужой строки по id не находит её
// (RowsAffected=0 → 404), то есть о чужих сущностях он даже не узнаёт. Более строгая модель
// (Default тоже изолирован + отдельный provider-флаг) — follow-up.
type tenantScopeKey struct{}

// WithTenantScope возвращает ctx, скоупящий tenant-aware storage-запросы на tenantID. Зовётся
// API-слоем на каждый запрос. Пустой tenantID или DefaultTenantID = ПРОВАЙДЕР (без scoping):
// ключ в контекст не кладём, scopeParam вернёт nil. Так single-org (всё в Default) не меняет
// поведения, а provider-акторы сохраняют кросс-тенантную видимость.
func WithTenantScope(ctx context.Context, tenantID string) context.Context {
	if tenantID == "" || tenantID == DefaultTenantID {
		return ctx
	}
	return context.WithValue(ctx, tenantScopeKey{}, tenantID)
}

// scopeParam возвращает значение для биндинга в scoped-предикат `($k IS NULL OR col = $k)`:
// nil для провайдера (нескоупленный ctx → предикат пропускает все строки) или tenant_id
// изолированного тенанта. Возвращаемый тип any: pgx биндит nil как SQL NULL, string как uuid.
func scopeParam(ctx context.Context) any {
	if v, ok := ctx.Value(tenantScopeKey{}).(string); ok && v != "" {
		return v
	}
	return nil
}

// TenantScoped сообщает, скоуплен ли ctx на конкретный тенант (true) или это провайдер/нескоуплено
// (false). Для тестов и редких мест, где нужно ветвление в Go, а не в SQL.
func TenantScoped(ctx context.Context) bool {
	v, ok := ctx.Value(tenantScopeKey{}).(string)
	return ok && v != ""
}
