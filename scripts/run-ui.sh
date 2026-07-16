#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export PATH="${HOME}/.vfox/sdks/golang/bin:${HOME}/.vfox/sdks/nodejs/bin:${PATH}"

mkdir -p "$ROOT/data/mihomo" "$ROOT/data/ui"
if [[ ! -f "$ROOT/data/mihomo/config.yaml" ]]; then
  cp "$ROOT/config/config.yaml" "$ROOT/data/mihomo/config.yaml"
fi

export MIHOMO_API="${MIHOMO_API:-http://127.0.0.1:9090}"
export MIHOMO_SECRET="${MIHOMO_SECRET:-mihomo}"
export UI_PASSWORD="${UI_PASSWORD:-mihomo-ui}"
export MIHOMO_CONFIG="${MIHOMO_CONFIG:-$ROOT/data/mihomo/config.yaml}"
export DATA_DIR="${DATA_DIR:-$ROOT/data/ui}"
export UI_ADDR="${UI_ADDR:-:8080}"
export STATIC_DIR="${STATIC_DIR:-$ROOT/frontend/dist}"

if [[ ! -d "$ROOT/frontend/dist" ]]; then
  (cd "$ROOT/frontend" && npm install && npm run build)
fi

cd "$ROOT/backend"
exec go run ./cmd/server
