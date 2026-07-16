# Mihomo UI

单容器运行官方 [mihomo](https://github.com/MetaCubeX/mihomo) 内核 + 自研 MIUIX 风格控制面板。内置订阅管理、节点切换、连接、日志，开箱即用。

---

## 快速开始

### 1. 启动

**docker run：**

```bash
docker run -d --name mihomo-ui \
  --network host --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -e TZ=Asia/Shanghai \
  -e UI_ADDR=:8080 \
  -e UI_PASSWORD=mihomo-ui \
  -e MIHOMO_SECRET=mihomo \
  -v "$PWD/data:/data/mihomo-ui" \
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
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    environment:
      - TZ=Asia/Shanghai
      - UI_ADDR=:8080
      - UI_PASSWORD=mihomo-ui
      - MIHOMO_SECRET=mihomo
    volumes:
      - ./data:/data/mihomo-ui
```

```bash
docker compose up -d
```

> 镜像支持 `linux/amd64` + `linux/arm64`，自动匹配宿主架构。

### 2. 访问

- **面板**：http://127.0.0.1:8080 （默认密码 `mihomo-ui`）
- **代理**：`127.0.0.1:7890`（mixed-port，HTTP/SOCKS5）
- **内核 API**：仅本机 `127.0.0.1:9090`

> 默认 `bind-address: 127.0.0.1`、`allow-lan: false`，代理只对本机开放。需局域网共享时改配置。

---

## 使用

进入面板后：

**首页** — 实时上下行流量、代理模式（规则 / 全局 / 直连）、TUN 开关、运行状态。

**节点** — 切换策略组、选节点、测速。规则模式显示订阅策略组；全局模式显示 GLOBAL 及其可选项。

**配置** — 管理订阅。点击卡片切换当前；卡片右上 **⋯** 菜单：
- **更新**：重新下载该订阅（仅 URL 类型）
- **编辑**：改名称 / 地址 / 更新间隔
- **配置**：编辑该订阅的原始 YAML
- **删除**

顶部「添加」可填订阅 URL 或上传本地 YAML 文件。当前订阅支持「更新」一键刷新。

**连接** — 实时连接列表，可单条或全部关闭，支持筛选。

**日志** — 设置内核日志级别（Debug / Info / Warning / Error），实时流。

---

## 订阅与配置

- 单一**当前**订阅，切换即生效（热重载）
- 装载：`base ⊕ 订阅 ⊕ UI 开关 ⊕ secret(环境变量)`
- 订阅尽量原样交给 mihomo（含 `proxy-providers` / `rule-providers`）
- 面板开关（模式 / 日志级别 / TUN）切换订阅后仍保留

数据目录（挂载 `./data` → `/data/mihomo-ui`）：

```text
data/
  mihomo/                 # mihomo -d 工作目录
    config.yaml           # 内核运行配置（合并结果）
  ui/
    base.yaml             # UI 合并底座（首次由 UI 从内置模板生成）
    settings.yaml         # 面板开关 + 配置列表（UI 启动时生成）
    config/<id>.yaml      # 配置原始内容
```

`base.yaml` 模板内嵌在 UI 二进制中（`//go:embed`）；仅当文件不存在时写出，已有用户修改不会被覆盖。

`settings.yaml` 示例：

```yaml
mode: rule
log-level: info
tun-enable: false
configId: <uuid>
configs:
  - id: <uuid>
    name: Xin
    url: https://...
    source: url
    interval: 0
    updatedAt: "2026-07-16 16:17:28"
    createdAt: "2026-07-16 16:13:17"
```

---

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `UI_ADDR` | `:8080` | 面板监听地址（host 网络下即本机端口） |
| `UI_PASSWORD` | `mihomo-ui` | 面板登录密码 |
| `MIHOMO_SECRET` | `mihomo` | 内核 API 密钥（装载时强制覆盖） |
| `MIHOMO_BIN` | `/mihomo` | 内核二进制路径 |
| `TZ` | `Asia/Shanghai` | 时区 |

---

## TUN 模式

默认关闭。开启需容器具备 `NET_ADMIN` 和 `/dev/net/tun`（上方启动命令已含）。

> WSL 下 TUN 与 Windows 自身 TUN 可能冲突，按需开启，不建议常开。
