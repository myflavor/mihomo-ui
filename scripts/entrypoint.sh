#!/bin/sh
set -eu

# Unified layout under /data (host mounts ./data -> /data):
#   /data/mihomo  kernel home (config.yaml, subs/, prepared/, providers/)
#   /data/ui      panel store (subscriptions.json)
DATA_ROOT="${DATA_ROOT:-/data}"
CONFIG_DIR="${MIHOMO_HOME:-$DATA_ROOT/mihomo}"
CONFIG_FILE="${MIHOMO_CONFIG:-$CONFIG_DIR/config.yaml}"
UI_DATA_DIR="${DATA_DIR:-$DATA_ROOT/ui}"
DEFAULT_CONFIG="${DEFAULT_CONFIG:-/defaults/config.yaml}"
MIHOMO_BIN="${MIHOMO_BIN:-/mihomo}"
UI_BIN="${UI_BIN:-/usr/local/bin/mihomo-ui}"
SECRET="${MIHOMO_SECRET:-change-me}"

mkdir -p "$CONFIG_DIR" "$UI_DATA_DIR" \
  "$CONFIG_DIR/subs" "$CONFIG_DIR/providers" "$CONFIG_DIR/prepared"

if [ ! -f "$CONFIG_FILE" ]; then
  if [ -f "$DEFAULT_CONFIG" ]; then
    cp "$DEFAULT_CONFIG" "$CONFIG_FILE"
    echo "[entrypoint] seeded config from $DEFAULT_CONFIG"
  else
    echo "[entrypoint] missing $CONFIG_FILE and no default template" >&2
    exit 1
  fi
fi

# Harden / sync secret + bind addresses for local-only control plane.
sync_config() {
  # secret
  if grep -qE '^[[:space:]]*secret:' "$CONFIG_FILE"; then
    sed -i -E "s|^([[:space:]]*secret:).*|\1 \"$SECRET\"|" "$CONFIG_FILE"
  else
    printf '\nsecret: "%s"\n' "$SECRET" >>"$CONFIG_FILE"
  fi
  # external-controller -> loopback
  if grep -qE '^[[:space:]]*external-controller:' "$CONFIG_FILE"; then
    sed -i -E 's|^([[:space:]]*external-controller:).*|\1 127.0.0.1:9090|' "$CONFIG_FILE"
  else
    printf '\nexternal-controller: 127.0.0.1:9090\n' >>"$CONFIG_FILE"
  fi
  # bind-address loopback
  if grep -qE '^[[:space:]]*bind-address:' "$CONFIG_FILE"; then
    sed -i -E 's|^([[:space:]]*bind-address:).*|\1 127.0.0.1|' "$CONFIG_FILE"
  else
    printf '\nbind-address: 127.0.0.1\n' >>"$CONFIG_FILE"
  fi
  # allow-lan false
  if grep -qE '^[[:space:]]*allow-lan:' "$CONFIG_FILE"; then
    sed -i -E 's|^([[:space:]]*allow-lan:).*|\1 false|' "$CONFIG_FILE"
  else
    printf '\nallow-lan: false\n' >>"$CONFIG_FILE"
  fi
}

sync_config

if [ ! -x "$MIHOMO_BIN" ]; then
  echo "[entrypoint] mihomo binary not found at $MIHOMO_BIN" >&2
  exit 1
fi
if [ ! -x "$UI_BIN" ]; then
  echo "[entrypoint] ui binary not found at $UI_BIN" >&2
  exit 1
fi

echo "[entrypoint] starting mihomo..."
"$MIHOMO_BIN" -d "$CONFIG_DIR" &
MIHOMO_PID=$!

cleanup() {
  echo "[entrypoint] shutting down..."
  kill "$MIHOMO_PID" 2>/dev/null || true
  wait "$MIHOMO_PID" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

# wait briefly for API
i=0
while [ "$i" -lt 30 ]; do
  if wget -qO- --header="Authorization: Bearer $SECRET" "http://127.0.0.1:9090/version" >/dev/null 2>&1 \
    || busybox wget -qO- --header="Authorization: Bearer $SECRET" "http://127.0.0.1:9090/version" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$MIHOMO_PID" 2>/dev/null; then
    echo "[entrypoint] mihomo exited early" >&2
    wait "$MIHOMO_PID" || true
    exit 1
  fi
  i=$((i + 1))
  sleep 0.3
done

export MIHOMO_API="${MIHOMO_API:-http://127.0.0.1:9090}"
export MIHOMO_SECRET="$SECRET"
export MIHOMO_CONFIG="$CONFIG_FILE"
export MIHOMO_HOME="$CONFIG_DIR"
export UI_ADDR="${UI_ADDR:-:8080}"
export DATA_DIR="$UI_DATA_DIR"
export STATIC_DIR="${STATIC_DIR:-/app/web}"

echo "[entrypoint] data: kernel=$CONFIG_DIR ui=$UI_DATA_DIR"
echo "[entrypoint] starting ui on $UI_ADDR..."
trap - INT TERM EXIT
"$UI_BIN" &
UI_PID=$!

term() {
  kill "$UI_PID" 2>/dev/null || true
  kill "$MIHOMO_PID" 2>/dev/null || true
  wait "$UI_PID" 2>/dev/null || true
  wait "$MIHOMO_PID" 2>/dev/null || true
  exit 0
}
trap term INT TERM

while kill -0 "$MIHOMO_PID" 2>/dev/null && kill -0 "$UI_PID" 2>/dev/null; do
  sleep 2
done

echo "[entrypoint] a process exited; shutting down companion"
term
