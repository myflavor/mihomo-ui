# build frontend
FROM node:22-alpine AS web
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install
COPY frontend/ .
RUN npm run build

# build control plane
FROM golang:1.22-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -o /out/mihomo-ui ./cmd/server

# final: official mihomo + UI
FROM metacubex/mihomo:latest

RUN apk add --no-cache ca-certificates tzdata wget || true

COPY --from=api /out/mihomo-ui /usr/local/bin/mihomo-ui
COPY --from=web /src/dist /app/web
COPY config/config.yaml /defaults/config.yaml
COPY scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /usr/local/bin/mihomo-ui

ENV TZ=Asia/Shanghai \
    STATIC_DIR=/app/web \
    DATA_DIR=/data \
    UI_ADDR=:8080 \
    MIHOMO_API=http://127.0.0.1:9090 \
    MIHOMO_CONFIG=/root/.config/mihomo/config.yaml \
    MIHOMO_HOME=/root/.config/mihomo \
    DEFAULT_CONFIG=/defaults/config.yaml

VOLUME ["/root/.config/mihomo", "/data"]
EXPOSE 8080 7890 9090

ENTRYPOINT ["/entrypoint.sh"]
