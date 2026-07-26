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
# The frontend is embedded into the binary, so it must be in place first.
COPY --from=web /src/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -o /out/mihomo-ui ./cmd/server

# final: official mihomo + UI (single process: mihomo-ui starts mihomo)
FROM metacubex/mihomo:latest

RUN apk add --no-cache ca-certificates tzdata || true

COPY --from=api /out/mihomo-ui /usr/local/bin/mihomo-ui

# Single home: mount host dir -> /data/mihomo-ui
ENV TZ=Asia/Shanghai \
    UI_LISTEN=0.0.0.0:7080 \
    MIHOMO_BIN=/mihomo \
    DATA_HOME=/data/mihomo-ui

VOLUME ["/data/mihomo-ui"]
EXPOSE 7080

ENTRYPOINT ["/usr/local/bin/mihomo-ui"]
