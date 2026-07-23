//go:build enterprise

package license

// Известные enterprise-фичи для feature-gate (Claims.Features / Manager.Has). Пустой
// список фич в лицензии = вся редакция (Has возвращает true на любую из этих строк).
// Ключи совпадают с полями ответа GET /capabilities (веб гейтит по ним enterprise-UI).
const (
	// FeatureSoftwareRemoval — удаление установленного ПО с устройства из интерфейса.
	FeatureSoftwareRemoval = "software_removal"
	// FeatureSIEMExport — форвардинг событий аудита во внешний SIEM (webhook).
	FeatureSIEMExport = "siem_export"
	// FeatureAuditIntegrity — проверка целостности журнала аудита (tamper-evidence).
	FeatureAuditIntegrity = "audit_integrity"
)
