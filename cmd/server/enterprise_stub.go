//go:build !enterprise

package main

import (
	"log/slog"
	"os"

	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/gateway"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Open-core: FileVault recovery-escrow — enterprise-фича. Escrow-сервис НЕ регистрируется
// (EscrowRecoveryKey → Unimplemented), lock-политика — дефолтная overlay-only (mode=
// filevault → 409). Все хуки — no-op. Enterprise-версии в enterprise.go (//go:build enterprise).
func registerEnterpriseFlags() {}

func runEnterpriseCLI() bool { return false }

func enterpriseSetup(_ *gateway.Gateway, _ *storage.DB, logger *slog.Logger) []api.RouterOption {
	// Оператор задал ESCROW_* на open-core-бинаре — фичи тут физически нет; молчание
	// выглядело бы как «эскроу включён». Предупредить, но стартовать (fail-closed).
	if os.Getenv("ESCROW_RECIPIENT") != "" || os.Getenv("ESCROW_RECIPIENT_FPR") != "" {
		logger.Warn("ESCROW_RECIPIENT/_FPR заданы, но это open-core сборка — FileVault escrow недоступен, переменные игнорируются (нужен enterprise-бинарь)")
	}
	return nil
}
