#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export PATH="${HOME}/.vfox/sdks/golang/bin:${HOME}/.vfox/sdks/nodejs/bin:${PATH}"

export DATA_HOME="${DATA_HOME:-$ROOT/data}"
mkdir -p "$DATA_HOME/subs" "$DATA_HOME/prepared"
if [[ ! -f "$DATA_HOME/base.yaml" ]]; then
  cp "$ROOT/config/base.yaml" "$DATA_HOME/base.yaml"
fi
if [[ ! -f "$DATA_HOME/config.yaml" ]]; then
  cp "$DATA_HOME/base.yaml" "$DATA_HOME/config.yaml"
fi

export MIHOMO_API="${MIHOMO_API:-http://127.0.0.1:9090}"
export MIHOMO_SECRET="${MIHOMO_SECRET:-mihomo}"
export UI_PASSWORD="${UI_PASSWORD:-mihomo-ui}"
export UI_ADDR="${UI_ADDR:-:8080}"
export STATIC_DIR="${STATIC_DIR:-$ROOT/frontend/dist}"

if [[ ! -d "$ROOT/frontend/dist" ]]; then
  (cd "$ROOT/frontend" && npm install && npm run build)
fi

cd "$ROOT/backend"
exec go run ./cmd/server
