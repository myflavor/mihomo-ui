# Mihomo UI（MIUIX 风格）

自研控制面板 + 订阅管理，**单容器**运行官方 mihomo 内核与 UI 控制面。

## 架构

一个 Docker 镜像：`FROM metacubex/mihomo`，entrypoint 同时启动：

| 进程 | 作用 | 默认 |
|------|------|------|
| `mihomo` | Meta 内核 | 代理 `127.0.0.1:7890`，API `127.0.0.1:9090` + secret |
| `mihomo-ui` | Go 控制面 + Vue 面板 | `:8080`，用 **UI_PASSWORD** 登录 |

### 配置数据流

```
订阅 URL / 本地上传
        ↓
data/mihomo/subs/<id>.yaml      原始 YAML（可编辑）
        ↓  添加 / 更新 / 编辑
构建 prepared（含 provider 物化）
        ↓
data/mihomo/prepared/<id>.yaml  处理后的订阅片段
        ↓  切换当前（仅读 prepared，不联网）
preserveKeys ⊕ prepared
        ↓
data/mihomo/config.yaml         内核运行时
        ↓
PUT /configs?force=true         热重载（不重启进程）
```

| 路径 | 含义 |
|------|------|
| `config/config.yaml` | 模板 |
| `data/mihomo/config.yaml` | 内核运行时配置 |
| `data/mihomo/subs/<id>.yaml` | 每份订阅的**原始**内容 |
| `data/mihomo/prepared/<id>.yaml` | 处理后的订阅片段（切换时直接装载） |
| `data/ui/subscriptions.json` | 订阅元数据 |
| `data/mihomo/providers/*.yaml` | 嵌套 provider 缓存 |

> `preserveKeys`（端口 / bind / secret / TUN / DNS 等）在切换订阅时从当前运行配置保留，不会被订阅覆盖。

## 安全

| 项 | 默认 |
|----|------|
| 内核 API | `127.0.0.1:9090` + `MIHOMO_SECRET` |
| 代理端口 | `bind-address: 127.0.0.1`，`allow-lan: false` |
| 面板 | `UI_ADDR=:8080`（可从局域网访问）+ **`UI_PASSWORD` 登录** |
| TUN | 默认关闭 |

`.env` 示例：

```bash
MIHOMO_SECRET=your-kernel-secret
UI_PASSWORD=your-panel-password
```

## 快速开始

### 方式一：拉取预构建镜像（推荐）

CI 在 push 到 `main` 或打 `v*` 标签时自动构建多架构镜像并发布到 GHCR：

```bash
ghcr.io/myflavor/mihomo-ui:latest        # main 分支
ghcr.io/myflavor/mihomo-ui:v1.2.3        # 版本标签
```

准备 `.env`（改两个密码）后：

```bash
mkdir -p data/mihomo data/ui

docker run -d --name mihomo-ui \
  --network host --pid host --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -e TZ=Asia/Shanghai \
  -e UI_PASSWORD=your-panel-password \
  -e MIHOMO_SECRET=your-kernel-secret \
  -v "$PWD/data/mihomo:/root/.config/mihomo" \
  -v "$PWD/data/ui:/data" \
  ghcr.io/myflavor/mihomo-ui:latest
```

或用 compose（见下）。

### 方式二：本地构建

```bash
cp .env.example .env        # 改掉两个密码
mkdir -p data/mihomo data/ui

# 去掉旧双容器（若有）
docker rm -f mihomo mihomo-ui 2>/dev/null || true

sudo docker compose up -d --build
```

### docker-compose（使用预构建镜像）

```yaml
services:
  mihomo-ui:
    image: ghcr.io/myflavor/mihomo-ui:latest
    container_name: mihomo-ui
    restart: unless-stopped
    network_mode: host
    pid: host
    cap_add: [NET_ADMIN]
    devices: [/dev/net/tun:/dev/net/tun]
    environment:
      - TZ=Asia/Shanghai
      - UI_ADDR=:8080
      - UI_PASSWORD=${UI_PASSWORD:-}
      - MIHOMO_SECRET=${MIHOMO_SECRET:-change-me}
      - MIHOMO_API=http://127.0.0.1:9090
      - MIHOMO_CONFIG=/root/.config/mihomo/config.yaml
      - DATA_DIR=/data
      - STATIC_DIR=/app/web
    volumes:
      - ./data/mihomo:/root/.config/mihomo
      - ./data/ui:/data
    pull_policy: always
```

启动后：

- 面板：http://127.0.0.1:8080 （需 UI_PASSWORD）
- 代理：`127.0.0.1:7890`
- 内核 API：仅本机 `127.0.0.1:9090`

> 首次拉取私有/公开镜像：仓库默认公开，匿名可拉；若设为私有需 `docker login ghcr.io`。

## 功能

- **首页**：实时流量、代理模式、TUN 开关、运行状态（当前配置 / 日志级别 / 内核版本 / 混合端口）
- **节点**：策略组、测速；规则模式显示订阅组，全局模式显示 GLOBAL 及其子组
- **配置**：单选当前订阅；更多菜单 = 更新 / 编辑 / 配置（原始 YAML） / 删除；编辑保存自动全量重建 prepared
- **连接**：实时连接列表，单条 / 全部关闭
- **日志**：设置内核日志级别，实时流

切换订阅只读已构建的 prepared，不联网，秒级生效。

## 开发

```bash
export UI_PASSWORD=dev
export MIHOMO_SECRET=change-me
./scripts/run-ui.sh
```

## CI

`.github/workflows/docker.yml` 在 push `main` / 标签 `v*` / 手动触发时，用 buildx 构建 `linux/amd64` + `linux/arm64`，推送到 `ghcr.io/myflavor/mihomo-ui`。无需额外 secret（使用内置 `GITHUB_TOKEN`）。
