#!/bin/sh
# Восстановление БД RoutineOps из дампа, снятого scripts/backup.sh (или update.sh —
# формат тот же, pg_dump -Fc).
#
# ⚠️ ДЕСТРУКТИВНО: pg_restore --clean --if-exists ДРОПАЕТ и пересоздаёт объекты —
# текущее содержимое БД заменяется дампом. Поэтому требует ЯВНОГО подтверждения,
# чтобы случайный запуск не затёр рабочую базу:
#   scripts/restore.sh <файл.dump> --yes
# либо BACKUP_RESTORE_CONFIRM=yes в окружении (для автоматизации).
#
# Параметры подключения — как у backup.sh: DATABASE_DSN, либо стандартные PG*.
# Пример (с хоста):
#   DATABASE_DSN=postgres://mdm:...@localhost:5432/mdm?sslmode=disable \
#     scripts/restore.sh backups/db-20260723-120000.dump --yes
set -eu
if ( set -o pipefail ) 2>/dev/null; then set -o pipefail; fi

log() { echo "[restore $(date -u +%Y-%m-%dT%H:%M:%SZ)] $*"; }
die() { echo "ОШИБКА: $*" >&2; exit 1; }

DUMP="${1:-}"
CONFIRM="${2:-}"

[ -n "$DUMP" ] || die "укажите файл дампа: scripts/restore.sh <файл.dump> --yes"
[ -f "$DUMP" ] || die "файл дампа не найден: $DUMP"

if [ "$CONFIRM" != "--yes" ] && [ "${BACKUP_RESTORE_CONFIRM:-}" != "yes" ]; then
  die "восстановление ДЕСТРУКТИВНО (перезапишет текущую БД). Повторите с флагом --yes:
  scripts/restore.sh \"$DUMP\" --yes
(или задайте BACKUP_RESTORE_CONFIRM=yes)"
fi

log "restoring from $DUMP (pg_restore --clean --if-exists)"
if [ -n "${DATABASE_DSN:-}" ]; then
  pg_restore --clean --if-exists -d "$DATABASE_DSN" "$DUMP"
else
  : "${PGDATABASE:?RESTORE: задайте DATABASE_DSN или PG*-переменные (PGDATABASE обязателен)}"
  pg_restore --clean --if-exists -d "$PGDATABASE" "$DUMP"
fi
log "restore complete."
log "перезапустите сервер поверх восстановленных данных: docker compose -f docker-compose.prod.yml restart server"
