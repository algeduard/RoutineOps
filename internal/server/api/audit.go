package api

import (
	"context"
	"log/slog"
)

// audit пишет запись аудита best-effort: ошибка логируется, но не валит операцию.
func (h *Handler) audit(ctx context.Context, userID, userEmail, action, targetType, targetID string, details any) {
	// context.WithoutCancel: аудит — побочная запись, которая должна пережить отмену
	// запроса (клиент отключился / таймаут уже ПОСЛЕ успешной основной операции),
	// иначе запись о свершившемся действии молча теряется.
	if err := h.db.WriteAuditLog(context.WithoutCancel(ctx), userID, userEmail, action, targetType, targetID, details); err != nil {
		slog.Error("audit log write failed", "action", action, "target_type", targetType, "target_id", targetID, "err", err)
	}
}
