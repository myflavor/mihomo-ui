# Mihomo UI

单容器运行 [mihomo](https://github.com/MetaCubeX/mihomo) 内核 + 控制面板：配置管理、节点切换、连接与日志，开箱即用。

镜像：`ghcr.io/myflavor/mihomo-ui`（`linux/amd64` / `linux/arm64`）

---

## 快速开始

```bash
docker run -d --name mihomo-ui \
  --network host --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -e TZ=Asia/Shanghai \
  -e UI_ADDR=:7080 \
  -e UI_PASSWORD=mihomo-ui \
  -e MIHOMO_SECRET=mihomo \
  -v "$PWD/data:/data/mihomo-ui" \
  ghcr.io/myflavor/mihomo-ui:latest
```

或 Compose：

```yaml
services:
  mihomo-ui:
    image: ghcr.io/myflavor/mihomo-ui:latest
    container_name: mihomo-ui
    restart: unless-stopped
    network_mode: host
    cap_add: [NET_ADMIN]
    devices: [/dev/net/tun:/dev/net/tun]
    environment:
      - TZ=Asia/Shanghai
      - UI_ADDR=:7080
      - UI_PASSWORD=mihomo-ui
      - MIHOMO_SECRET=mihomo
    volumes:
      - ./data:/data/mihomo-ui
```

```bash
docker compose up -d
```

| 入口 | 地址 | 说明 |
|------|------|------|
| 面板 | http://127.0.0.1:7080 | 密码默认 `mihomo-ui` |
| 代理 | `127.0.0.1:7890` | mixed-port（HTTP / SOCKS5） |
| 内核 API | `127.0.0.1:9090` | 仅本机；密钥默认 `mihomo` |

默认 `bind-address: 127.0.0.1`、`allow-lan: false`，代理只对本机开放。

---

## 面板

| 页 | 功能 |
|----|------|
| **首页** | 流量、模式（规则 / 全局 / 直连）、TUN、运行状态 |
| **节点** | 策略组切换、选节点、测速 |
| **配置** | 添加 URL / 上传 YAML；点卡片切换当前；菜单：更新 / 编辑 / 原始配置 / 删除 |
| **连接** | 实时连接，单条或全部关闭 |
| **日志** | 级别（Debug / Info / Warning / Error）与实时流 |

---

## 配置如何生效

装载公式：

```text
mihomo/config.yaml = base.yaml ⊕ 当前配置 ⊕ settings 开关 ⊕ MIHOMO_SECRET
```

- 同一时刻只有一个**当前配置**，切换即热重载
- 配置尽量原样交给内核（含 `proxy-providers` / `rule-providers`）
- 模式 / 日志级别 / TUN 写在 `settings.yaml`，换配置后仍保留
- `secret`、`external-controller` 由运行时强制写入，不必写进 base

数据目录（`./data` → `/data/mihomo-ui`）：

```text
data/
  mihomo/
    config.yaml          # 内核运行配置（合并结果）
  ui/
    base.yaml            # 合并底座（首次从内置模板生成，之后不覆盖）
    settings.yaml        # 面板开关 + 配置列表
    config/<id>.yaml     # 各配置原始内容
```

`settings.yaml` 示例：

```yaml
mode: rule
log-level: info
tun-enable: false
configId: <uuid>
configs:
  - id: <uuid>
    name: example
    url: https://...
    source: url
    interval: 0
    updatedAt: "2026-07-16 16:17:28"
    createdAt: "2026-07-16 16:13:17"
```

进程：容器入口是 `mihomo-ui`，由它拉起内核 `mihomo -d …/mihomo`。

---

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `UI_ADDR` | `:7080` | 面板监听（host 网络下即本机端口） |
| `UI_PASSWORD` | `mihomo-ui` | 面板登录密码 |
| `MIHOMO_SECRET` | `mihomo` | 内核 API 密钥（装载时强制覆盖） |
| `MIHOMO_BIN` | `/mihomo` | 内核二进制路径 |
| `TZ` | `Asia/Shanghai` | 时区 |

代理端口改 `data/ui/base.yaml` 的 `mixed-port` 后，在面板重新装载当前配置即可。

---

## TUN

默认关闭（`settings.yaml` 里 `tun-enable: false`）。开启需 `NET_ADMIN` 与 `/dev/net/tun`（上方启动命令已含）。

> WSL 下可能与 Windows 自身 TUN 冲突，按需使用，不建议常开。
