#!/bin/sh
# CI-guard от утечки enterprise-кода в open-core (free) сборку. Падает, если:
#  1) free-граф зависимостей содержит filippo.io/age (leak-canary: age — единственный
#     импортёр escrow-крипты; его наличие во free = escrow затянут в open-core);
#  2) free-сборка сервера/агента импортирует enterprise-пакеты (crypto/escrow/filevault/shamir).
#
# Запуск (open-core CI, без -tags enterprise):
#   sh scripts/check-oss-no-enterprise.sh
set -e

fail=0

# Транзитивное замыкание зависимостей ШИПАЕМЫХ free-бинарей (сервер + агент). НЕ
# `./internal/...` — там enterprise-пакеты существуют как пустые free-заглушки
# (doc_free.go) и попали бы в листинг, хотя ничем free не ИМПОРТИРУЮТСЯ.
BINDEPS=$(go list -deps ./cmd/server ./cmd/agent 2>/dev/null)

echo "== 1. filippo.io/age не в free-графе =="
if printf '%s\n' "$BINDEPS" | grep -q '^filippo.io/age'; then
  echo "  ОШИБКА: filippo.io/age в графе open-core-бинарей — escrow затянут в free!" >&2
  printf '%s\n' "$BINDEPS" | grep '^filippo.io/age' >&2
  fail=1
else
  echo "  OK: age отсутствует в open-core"
fi

echo "== 2. enterprise-пакеты не импортируются free-бинарями =="
ent='internal/server/crypto|internal/server/escrow|internal/agent/filevault|internal/offline/shamir'
if printf '%s\n' "$BINDEPS" | grep -Eq "$ent"; then
  echo "  ОШИБКА: enterprise-пакет в графе open-core-бинарей:" >&2
  printf '%s\n' "$BINDEPS" | grep -E "$ent" >&2
  fail=1
else
  echo "  OK: enterprise-пакеты не в графе open-core"
fi

if [ "$fail" -ne 0 ]; then
  echo "LEAK-GUARD: enterprise-код просочился в open-core-сборку." >&2
  exit 1
fi
echo "LEAK-GUARD: open-core чист от enterprise ✅"
