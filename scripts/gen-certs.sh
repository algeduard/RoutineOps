#!/usr/bin/env bash
#
# Генерация сертификатов для mTLS в dev-окружении.
#
# Usage:
#   ./scripts/gen-certs.sh                # создаст CA + server, без агента
#   ./scripts/gen-certs.sh <device_id>    # дополнительно создаст агентский cert
#
# Структура вывода:
#   certs/
#     ca.crt, ca.key              — корневой CA (подписывает всё)
#     server.crt, server.key      — серверный cert
#     agents/<device_id>/
#       agent.crt, agent.key      — агентский cert (CN = device_id)
#       ca.crt                    — копия CA для агента
#
# CN сертификата агента = device_id. Сервер извлекает его из подключения
# (см. ADR-1 в proto/agent.proto). Это единственный источник истины.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERTS_DIR="$SCRIPT_DIR/../certs"

mkdir -p "$CERTS_DIR"
cd "$CERTS_DIR"

# ---------- Root CA ----------
if [[ ! -f ca.key ]]; then
  echo "[+] Generating Root CA (10 years)"
  openssl genrsa -out ca.key 4096 2>/dev/null
  openssl req -new -x509 -days 3650 -key ca.key -out ca.crt \
    -subj "/CN=RoutineOps Root CA/O=RoutineOps" 2>/dev/null
  echo "    ca.crt, ca.key"
else
  echo "[=] Root CA already exists, skipping"
fi

# ---------- Server cert ----------
if [[ ! -f server.key ]]; then
  echo "[+] Generating server cert (1 year)"
  openssl genrsa -out server.key 4096 2>/dev/null
  openssl req -new -key server.key -out server.csr \
    -subj "/CN=routineops-server/O=RoutineOps" 2>/dev/null

  # Базовые SAN + дополнительные для удалённого доступа агентов (публичный IP/домен):
  #   SERVER_SANS="IP:203.0.113.10,DNS:mdm.example.com" bash scripts/gen-certs.sh
  # install.sh проставляет SERVER_SANS автоматически из публичного IP хоста.
  SAN="DNS:localhost,DNS:routineops-server,IP:127.0.0.1"
  [ -n "${SERVER_SANS:-}" ] && SAN="${SAN},${SERVER_SANS}"
  cat > server.ext <<EOF
subjectAltName = ${SAN}
extendedKeyUsage = serverAuth
EOF

  openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out server.crt -days 365 -extfile server.ext 2>/dev/null
  rm server.csr server.ext
  echo "    server.crt, server.key"
else
  echo "[=] Server cert already exists, skipping"
fi

# ---------- Agent cert (опционально) ----------
DEVICE_ID="${1:-}"
if [[ -n "$DEVICE_ID" ]]; then
  AGENT_DIR="agents/$DEVICE_ID"
  mkdir -p "$AGENT_DIR"

  if [[ ! -f "$AGENT_DIR/agent.key" ]]; then
    echo "[+] Generating agent cert for device_id=$DEVICE_ID (1 year)"
    openssl genrsa -out "$AGENT_DIR/agent.key" 4096 2>/dev/null
    openssl req -new -key "$AGENT_DIR/agent.key" -out "$AGENT_DIR/agent.csr" \
      -subj "/CN=$DEVICE_ID/O=RoutineOps Agent" 2>/dev/null

    cat > "$AGENT_DIR/agent.ext" <<EOF
extendedKeyUsage = clientAuth
EOF

    openssl x509 -req -in "$AGENT_DIR/agent.csr" -CA ca.crt -CAkey ca.key \
      -CAcreateserial -out "$AGENT_DIR/agent.crt" -days 365 \
      -extfile "$AGENT_DIR/agent.ext" 2>/dev/null

    rm "$AGENT_DIR/agent.csr" "$AGENT_DIR/agent.ext"
    cp ca.crt "$AGENT_DIR/ca.crt"

    echo "    $AGENT_DIR/agent.crt, agent.key, ca.crt"
  else
    echo "[=] Agent cert for $DEVICE_ID already exists, skipping"
  fi
fi

echo "[✓] Done. Certs in: $CERTS_DIR"
