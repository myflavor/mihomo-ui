# Mihomo UI

单容器运行官方 [mihomo](https://github.com/MetaCubeX/mihomo) 内核 + 自研 MIUIX 风格控制面板。内置订阅管理、节点切换、连接、日志，开箱即用。

---

## 快速开始

### 1. 准备配置

```bash
mkdir -p data   # 会生成 mihomo/ 与 ui/ 子目录
cat > .env <<'EOF'
MIHOMO_SECRET=change-me-kernel
UI_PASSWORD=change-me-panel
EOF
```

- `MIHOMO_SECRET`：内核 API 密钥
- `UI_PASSWORD`：面板登录密码

### 2. 启动

**docker run：**

```bash
docker run -d --name mihomo-ui \
  --network host --pid host --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  --env-file .env \
  -e TZ=Asia/Shanghai \
  -v "$PWD/data:/data" \
  ghcr.io/myflavor/mihomo-ui:latest
```

**或 docker-compose：**

```yaml
services:
  mihomo-ui:
    image: ghcr.io/myflavor/mihomo-ui:latest
    container_name: mihomo-ui
    restart: unless-stopped
    network_mode: host
    pid: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    environment:
      - TZ=Asia/Shanghai
      - UI_ADDR=:8080
      - UI_PASSWORD=your-panel-password
      - MIHOMO_SECRET=your-kernel-secret
    volumes:
      - ./data:/data
    pull_policy: always
```

```bash
docker compose up -d
```

> 镜像支持 `linux/amd64` + `linux/arm64`，自动匹配宿主架构。仓库 `docker-compose.yml` 为本地构建用，上面这段用于拉取预构建镜像。

### 3. 访问

- **面板**：http://127.0.0.1:8080 （输入 `UI_PASSWORD` 登录）
- **代理**：`127.0.0.1:7890`（mixed-port，HTTP/SOCKS5）
- **内核 API**：仅本机 `127.0.0.1:9090`

> 默认 `bind-address: 127.0.0.1`、`allow-lan: false`，代理只对本机开放。需局域网共享时改配置。

---

## 使用

进入面板后：

**首页** — 实时上下行流量、代理模式（规则 / 全局 / 直连）、TUN 开关、运行状态。

**节点** — 切换策略组、选节点、测速。规则模式显示订阅策略组；全局模式显示 GLOBAL 及其可选项。

**配置** — 管理订阅。点击卡片切换当前；卡片右上 **⋯** 菜单：
- **更新**：重新下载该订阅并重建（仅 URL 类型）
- **编辑**：改名称 / 地址 / 更新间隔
- **配置**：编辑该订阅的原始 YAML
- **删除**

顶部「添加」可填订阅 URL 或上传本地 YAML 文件。当前订阅支持「更新」一键刷新。

**连接** — 实时连接列表，可单条或全部关闭，支持筛选。

**日志** — 设置内核日志级别（Debug / Info / Warning / Error），实时流。

---

## 订阅管理说明

- 单一**当前**订阅，切换即生效（热重载，不重启进程）
- 添加 / 更新 / 编辑时自动构建 **prepared** 处理片段；切换只读 prepared，**不联网**，秒级切换
- 运行时偏好（端口 / bind / secret / TUN / DNS 等）在切换时保留，不被订阅覆盖
- 节点名保持原文，不加订阅前缀

数据目录：

| 路径 | 含义 |
|------|------|
| `data/mihomo/config.yaml` | 内核运行时配置 |
| `data/mihomo/subs/<id>.yaml` | 订阅原始内容 |
| `data/mihomo/prepared/<id>.yaml` | 处理后的订阅片段 |
| `data/ui/subscriptions.json` | 订阅元数据 |

宿主机只需挂载 **`./data` → `/data`**，容器内自动使用 `/data/mihomo` 与 `/data/ui`。

---

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `UI_PASSWORD` | — | 面板登录密码（必填） |
| `MIHOMO_SECRET` | `change-me` | 内核 API 密钥 |
| `UI_ADDR` | `:8080` | 面板监听地址 |
| `MIHOMO_API` | `http://127.0.0.1:9090` | 内核 API 地址 |
| `MIHOMO_HOME` | `/data/mihomo` | 内核工作目录 |
| `MIHOMO_CONFIG` | `/data/mihomo/config.yaml` | 内核配置路径 |
| `DATA_DIR` | `/data/ui` | UI 数据目录 |
| `STATIC_DIR` | `/app/web` | 前端静态资源 |
| `TZ` | `Asia/Shanghai` | 时区 |

---

## TUN 模式

默认关闭。开启需容器具备 `NET_ADMIN` 和 `/dev/net/tun`（上方启动命令已含）。

> WSL 下 TUN 与 Windows 自身 TUN 可能冲突，按需开启，不建议常开。

---

## 本地构建 / 开发

```bash
# 本地构建运行
sudo docker compose up -d --build

# 开发模式
export UI_PASSWORD=dev
export MIHOMO_SECRET=change-me
./scripts/run-ui.sh
```

---

## CI

`.github/workflows/docker.yml` 在 push `main` / 标签 `v*` / 手动触发时，构建多架构镜像推送到 `ghcr.io/myflavor/mihomo-ui`，使用内置 `GITHUB_TOKEN`，无需额外配置。

可用标签：`latest`、`v1.2.3`、`1.2`、`1`、`sha-xxxxxx`。
