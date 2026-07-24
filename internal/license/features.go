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
	// FeatureSSO — вход через внешний OIDC-провайдер (SSO).
	FeatureSSO = "sso"
	// FeatureCompliance — compliance-дашборды и отчёты (CIS/SOC2-скоринг по существующим данным).
	FeatureCompliance = "compliance"
	// FeatureCVEScan — сканирование инвентаря ПО на известные уязвимости (CVE).
	FeatureCVEScan = "cve_scan"
	// FeatureMultitenancy — модель тенантов (арендаторов) и привязка устройств/пользователей
	// к ним. MVP: управление + назначение; полная per-query изоляция данных — follow-up.
	FeatureMultitenancy = "multitenancy"
	// FeatureSCIM — provisioning юзеров из IdP по SCIM 2.0 (Okta/Azure AD/OneLogin).
	FeatureSCIM = "scim"
	// FeatureAlertRouting — маршрутизация алертов по уровню критичности (эскалация,
	// доставка в telegram/webhook по правилам).
	FeatureAlertRouting = "alert_routing"
	// FeatureReports — экспортируемые отчёты (CSV / печатный HTML→PDF) по существующим
	// данным: устройства, инвентарь ПО, журнал аудита, алерты.
	FeatureReports = "reports"
	// FeaturePolicyAsCode — декларативные software-политики (GitOps): админ хранит желаемый
	// набор глобальных software-правил как JSON source-of-truth, сервер реконсилит живые
	// правила и показывает дрейф (расхождение живого состояния с декларацией).
	FeaturePolicyAsCode = "policy_as_code"
)
