#!/bin/sh
# Авто-бэкап PostgreSQL для self-hosted RoutineOps (HA/DR). Делает pg_dump в custom
# format (-Fc → восстанавливается pg_restore --clean --if-exists) в каталог с
# таймстампом и ротирует старые дампы. Идемпотентный и безопасный: БД на запись не
# трогает, ротация удаляет ТОЛЬКО свои файлы (db-*.dump) старше N дней.
#
# Где запускается:
#   - как compose-сервис `backup` (docker-compose.prod.yml) в контейнере
#     postgres:16-alpine по расписанию (там есть pg_dump и сеть до сервиса postgres);
#   - или вручную с хоста, где установлен клиент PostgreSQL:
#       DATABASE_DSN=postgres://mdm:...@localhost:5432/mdm?sslmode=disable \
#         BACKUP_DIR=./backups scripts/backup.sh
#
# Параметры (env):
#   DATABASE_DSN            строка подключения (как у сервера). Задан → используется.
#   PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE  стандартные libpq — альтернатива DSN.
#   BACKUP_DIR             каталог дампов (по умолчанию ./backups).
#   BACKUP_RETENTION_DAYS  сколько дней хранить (по умолчанию 14; 0 = не удалять).
set -eu
# pipefail ловит ошибку pg_dump в конвейере; поддержан bash и busybox-ash (образ
# postgres:16-alpine), но НЕ dash (/bin/sh на Debian) — включаем опционально, чтобы
# скрипт оставался переносимым и при ручном запуске с Debian-хоста.
if ( set -o pipefail ) 2>/dev/null; then set -o pipefail; fi

# umask 077 ДО создания файлов: pg_dump пишет во временный db-*.dump.partial, и без этого
# он создавался бы с umask по умолчанию (0644 под root в postgres:16-alpine) — весь дамп
# (хеши паролей, TOTP-секреты, все данные) висел бы world-readable на время дампа (минуты),
# а осиротевший после сбоя .partial — навсегда. С umask 077 и .partial, и итог = 0600.
umask 077

log() { echo "[backup $(date -u +%Y-%m-%dT%H:%M:%SZ)] $*"; }

BACKUP_DIR="${BACKUP_DIR:-./backups}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"

# Каталог создаём только если его нет, и лишь тогда ужимаем права: дампы содержат ВСЕ
# данные (хеши паролей, TOTP-секреты) — не world-readable. НЕ chmod'им существующий
# каталог, чтобы не отобрать доступ у деплойера, если ./backups завёл update.sh.
if [ ! -d "$BACKUP_DIR" ]; then
  mkdir -p "$BACKUP_DIR"
  chmod 700 "$BACKUP_DIR" 2>/dev/null || true
fi

ts=$(date -u +%Y%m%d-%H%M%S)
out="$BACKUP_DIR/db-$ts.dump"
tmp="$out.partial"

# Пишем во временный файл и атомарно переименовываем: прерванный дамп не оставит
# «готовый» на вид, но битый db-*.dump (его подхватили бы ротация и restore).
log "starting pg_dump -> $out"
if [ -n "${DATABASE_DSN:-}" ]; then
  pg_dump -Fc -d "$DATABASE_DSN" -f "$tmp"
else
  : "${PGDATABASE:?BACKUP: задайте DATABASE_DSN или PG*-переменные (PGDATABASE обязателен)}"
  pg_dump -Fc -f "$tmp"
fi
mv "$tmp" "$out"
chmod 600 "$out" 2>/dev/null || true
log "backup done: $out ($(du -h "$out" 2>/dev/null | cut -f1))"

# Ротация: удаляем СВОИ дампы старше N дней (mtime +N = строго старше N*24ч). 0 = выкл.
if [ "$BACKUP_RETENTION_DAYS" -gt 0 ] 2>/dev/null; then
  removed=$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'db-*.dump*' -mtime "+$BACKUP_RETENTION_DAYS" -print -delete | wc -l)
  kept=$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'db-*.dump' | wc -l)
  log "retention: removed $(echo "$removed" | tr -d ' ') dump(s) older than ${BACKUP_RETENTION_DAYS}d, kept $(echo "$kept" | tr -d ' ')"
else
  log "retention disabled (BACKUP_RETENTION_DAYS=$BACKUP_RETENTION_DAYS)"
fi
