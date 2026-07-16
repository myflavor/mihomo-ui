#!/bin/sh
set -eu

# Everything under one home. Host mounts e.g. ./data -> /data/mihomo-ui
# mihomo -d $DATA_HOME
DATA_HOME="${DATA_HOME:-/data/mihomo-ui}"
CONFIG_FILE="$DATA_HOME/config.yaml"
BASE_FILE="$DATA_HOME/base.yaml"
DEFAULT_BASE="/defaults/base.yaml"
MIHOMO_BIN="${MIHOMO_BIN:-/mihomo}"
UI_BIN="${UI_BIN:-/usr/local/bin/mihomo-ui}"
SECRET="${MIHOMO_SECRET:-mihomo}"

mkdir -p "$DATA_HOME" "$DATA_HOME/subs" "$DATA_HOME/prepared"

# Seed base.yaml once (user can edit it later under the data mount).
if [ ! -f "$BASE_FILE" ]; then
  if [ -f "$DEFAULT_BASE" ]; then
    cp "$DEFAULT_BASE" "$BASE_FILE"
    echo "[entrypoint] seeded base.yaml"
  else
    echo "[entrypoint] missing default base template at $DEFAULT_BASE" >&2
    exit 1
  fi
fi

# Seed runtime config from base on first boot.
if [ ! -f "$CONFIG_FILE" ]; then
  cp "$BASE_FILE" "$CONFIG_FILE"
  echo "[entrypoint] seeded config.yaml from base.yaml"
fi

# Keep secret / bind in sync for first boot (full merge is done by UI on install).
if grep -qE '^[[:space:]]*secret:' "$CONFIG_FILE"; then
  sed -i -E "s|^([[:space:]]*secret:).*|\1 \"$SECRET\"|" "$CONFIG_FILE"
else
  printf '\nsecret: "%s"\n' "$SECRET" >>"$CONFIG_FILE"
fi
if grep -qE '^[[:space:]]*external-controller:' "$CONFIG_FILE"; then
  sed -i -E 's|^([[:space:]]*external-controller:).*|\1 127.0.0.1:9090|' "$CONFIG_FILE"
else
  printf '\nexternal-controller: 127.0.0.1:9090\n' >>"$CONFIG_FILE"
fi
if grep -qE '^[[:space:]]*bind-address:' "$CONFIG_FILE"; then
  sed -i -E 's|^([[:space:]]*bind-address:).*|\1 127.0.0.1|' "$CONFIG_FILE"
else
  printf '\nbind-address: 127.0.0.1\n' >>"$CONFIG_FILE"
fi
if grep -qE '^[[:space:]]*allow-lan:' "$CONFIG_FILE"; then
  sed -i -E 's|^([[:space:]]*allow-lan:).*|\1 false|' "$CONFIG_FILE"
else
  printf '\nallow-lan: false\n' >>"$CONFIG_FILE"
fi

if [ ! -x "$MIHOMO_BIN" ]; then
  echo "[entrypoint] mihomo binary not found at $MIHOMO_BIN" >&2
  exit 1
fi
if [ ! -x "$UI_BIN" ]; then
  echo "[entrypoint] ui binary not found at $UI_BIN" >&2
  exit 1
fi

echo "[entrypoint] starting mihomo -d $DATA_HOME ..."
"$MIHOMO_BIN" -d "$DATA_HOME" &
MIHOMO_PID=$!

cleanup() {
  echo "[entrypoint] shutting down..."
  kill "$MIHOMO_PID" 2>/dev/null || true
  wait "$MIHOMO_PID" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

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
export DATA_HOME
export UI_ADDR="${UI_ADDR:-:8080}"
export STATIC_DIR="${STATIC_DIR:-/app/web}"

echo "[entrypoint] data=$DATA_HOME"
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
