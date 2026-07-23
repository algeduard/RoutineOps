//go:build enterprise

package api

import (
	"net/http"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/go-chi/chi/v5"
)

// CapabilitiesRoutes монтирует GET /capabilities — какие enterprise-функции РЕАЛЬНО
// активны при текущей лицензии (для веба: показывать ли enterprise-кнопки). Все роли
// (WithRoutes, не только it_admin — viewer тоже видит доступность). В open-core роута нет
// (404) → веб трактует все enterprise-фичи как выключенные. Ключи совпадают с константами
// фич в internal/license (features.go).
func CapabilitiesRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(_ *Handler, r chi.Router) {
		r.Get("/capabilities", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]bool{
				license.FeatureSoftwareRemoval: mgr.Has(license.FeatureSoftwareRemoval),
				license.FeatureSIEMExport:      mgr.Has(license.FeatureSIEMExport),
				license.FeatureAuditIntegrity:  mgr.Has(license.FeatureAuditIntegrity),
				license.FeatureSSO:             mgr.Has(license.FeatureSSO),
				license.FeatureCompliance:      mgr.Has(license.FeatureCompliance),
			})
		})
	}
}
