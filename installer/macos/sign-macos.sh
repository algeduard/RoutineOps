#!/usr/bin/env bash
# Подпись macOS-бинаря агента БЕЗ Apple Developer ID (pre-release путь).
#
# Зачем: на Apple Silicon исполняемый файл обязан быть подписан, иначе ОС его
# убивает. Developer ID + нотаризация — это «zero-friction запуск из браузера»,
# для enterprise-агента (ставится админом/деплоем, дальше self-update по ed25519)
# это апгрейд на будущее, а не блокер. Здесь — ad-hoc (по умолчанию) или
# self-signed идентичность (стабильный code identity), оба бесплатны.
#
# Использование:
#   installer/macos/sign-macos.sh [путь-к-бинарю]      # ad-hoc (identity "-")
#   MACOS_SIGN_IDENTITY="Mac Dev: …" installer/macos/sign-macos.sh bin/agent
#
# Будущий Developer ID — codesign --options runtime +
# notarytool).
set -euo pipefail

BIN="${1:-bin/agent}"
IDENTITY="${MACOS_SIGN_IDENTITY:--}" # "-" = ad-hoc

if [[ ! -f "$BIN" ]]; then
	echo "ошибка: бинарь не найден: $BIN" >&2
	exit 1
fi

if [[ "$IDENTITY" == "-" ]]; then
	echo "→ ad-hoc подпись $BIN"
else
	echo "→ подпись $BIN идентичностью: $IDENTITY"
fi

# --force перезатирает ad-hoc подпись, которую Go-линкер ставит сам.
codesign --force --sign "$IDENTITY" "$BIN"

echo "→ проверка целостности подписи"
codesign --verify --strict --verbose=2 "$BIN"

echo "→ сводка подписи"
codesign -dvv "$BIN" 2>&1 | grep -E 'Identifier|Signature|TeamIdentifier|flags' || true

cat <<'NOTE'

ГОТОВО. Без Developer ID нотаризации нет — Gatekeeper блокирует ТОЛЬКО файлы с
карантином (скачаны через браузер/почту). Установщик/деплой агента должен
ставить бинарь без карантина (через pkg/скрипт под админом) либо снимать его:
  xattr -dr com.apple.quarantine <путь>
Self-update не затрагивается: замена бинаря на месте карантин не ставит.
NOTE
