//go:build enterprise

package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/gateway"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Enterprise-сборка (-tags enterprise): включает лицензионное ядро. Парная реализация к
// cmd/server/enterprise_stub.go (//go:build !enterprise). FileVault-escrow — отдельная
// enterprise-фича, в этом форке ещё не собрана (её ESCROW_* игнорируются с предупреждением).

// registerEnterpriseFlags — escrow-флаги (напр. -escrow-fpr) появятся вместе с escrow-фичей.
func registerEnterpriseFlags() {}

// runEnterpriseCLI — выпуск лицензий делает отдельный бинарь routineops-license, серверу
// enterprise-CLI не нужен.
func runEnterpriseCLI() bool { return false }

// enterpriseSetup поднимает лицензионное ядро и монтирует /license (через WithAdminRoutes).
// Роут ставится ВСЕГДА в enterprise-сборке (даже без корня доверия): GET /license тогда
// вернёт «не задана», а не 404 — так UI отличает enterprise-без-лицензии от open-core.
func enterpriseSetup(_ *gateway.Gateway, _ *storage.DB, logger *slog.Logger) []api.RouterOption {
	pub, err := license.PubKey()
	if err != nil {
		logger.Error("публичный ключ лицензий не разобран — лицензирование выключено", "err", err)
		pub = nil
	}
	if pub == nil {
		logger.Warn("корень доверия лицензий не задан (ROUTINEOPS_LICENSE_PUBKEY или вшитый на сборке defaultPubKeyB64) — enterprise-функции выключены, /license покажет «не задана»")
	}

	grace := time.Duration(0)
	if g := os.Getenv("ROUTINEOPS_LICENSE_GRACE"); g != "" {
		if d, perr := time.ParseDuration(g); perr == nil {
			grace = d
		} else {
			logger.Warn("ROUTINEOPS_LICENSE_GRACE не разобран как duration — отсрочка 0", "value", g, "err", perr)
		}
	}

	mgr := license.NewManager(pub, grace, os.Getenv("ROUTINEOPS_LICENSE_FILE"))
	mgr.LoadInitial(os.Getenv("ROUTINEOPS_LICENSE"), os.Getenv("ROUTINEOPS_LICENSE_PASSWORD"), logger)

	// FileVault recovery-escrow (ESCROW_*) — отдельная enterprise-фича, в этом форке ещё
	// не реализована; молчание выглядело бы как «эскроу включён».
	if os.Getenv("ESCROW_RECIPIENT") != "" || os.Getenv("ESCROW_RECIPIENT_FPR") != "" {
		logger.Warn("ESCROW_* заданы, но FileVault-escrow в этой сборке не реализован — игнорируются")
	}

	return []api.RouterOption{
		api.WithAdminRoutes(api.LicenseRoutes(mgr)),
		// Удаление ПО из интерфейса — enterprise-фича за лицензией (mgr.Has внутри хендлера).
		api.WithAdminRoutes(api.SoftwareRemovalRoutes(mgr)),
		// /capabilities — какие enterprise-фичи активны (веб гейтит по ним UI). Все роли.
		api.WithRoutes(api.CapabilitiesRoutes(mgr)),
	}
}
