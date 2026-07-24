package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Политика unattended-доступа удалённого рабочего стола на устройство (миграция 039,
// devices.rd_unattended). Opt-in, DEFAULT false: пока it_admin явно не включит,
// сеанс идёт ATTENDED (с запросом согласия на устройстве). Когда включено — сервер
// сообщает агенту в RemoteDesktopCommand.unattended, что для сессии можно пропустить
// запрос согласия; плашка «идёт сеанс» и аудит при этом СОХРАНЯЮТСЯ.

// GetRDUnattended отдаёт флаг unattended-доступа по deviceID (для WS-хендлера при
// старте сессии и для REST). found=false, если устройства нет. Неизвестное
// устройство → (false, false, nil): fail-safe — без opt-in согласие не пропускается.
func (db *DB) GetRDUnattended(ctx context.Context, deviceID string) (enabled, found bool, err error) {
	err = db.pool.QueryRow(ctx,
		`SELECT rd_unattended FROM devices WHERE id = $1 AND ($2::uuid IS NULL OR tenant_id = $2::uuid)`,
		deviceID, scopeParam(ctx)).Scan(&enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, nil
		}
		return false, false, err
	}
	return enabled, true, nil
}

// SetRDUnattended включает/выключает unattended-доступ для устройства. found=false,
// если устройства нет (0 обновлённых строк).
func (db *DB) SetRDUnattended(ctx context.Context, deviceID string, enabled bool) (found bool, err error) {
	tag, err := db.pool.Exec(ctx,
		`UPDATE devices SET rd_unattended = $2 WHERE id = $1 AND ($3::uuid IS NULL OR tenant_id = $3::uuid)`,
		deviceID, enabled, scopeParam(ctx))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
