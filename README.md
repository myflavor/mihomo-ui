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
  -e UI_LISTEN=0.0.0.0:7080 \
  -e UI_PASSWORD=mihomo-ui \
  -v "$PWD/data:/data/mihomo-ui" \
  ghcr.io/myflavor/mihomo-ui
```

或 Compose：

```yaml
services:
  app:
    image: ghcr.io/myflavor/mihomo-ui
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    environment:
      - TZ=Asia/Shanghai
      - UI_LISTEN=0.0.0.0:7080
      - UI_PASSWORD=mihomo-ui
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
| 内核 API | `127.0.0.1:9090` | 地址可用 `MIHOMO_LISTEN` 改；密钥默认每次启动随机 |

代理端口默认只对本机开放（`allow-lan: false`）；只走 TUN 的话可以设 `PROXY_LISTEN=` 关掉它。

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
mihomo/config.yaml = base.yaml ⊕ 当前配置 ⊕ settings 开关 ⊕ 内核密钥
```

- 同一时刻只有一个**当前配置**，切换即生效
- 配置尽量原样交给内核（含 `proxy-providers` / `rule-providers` / `rules`）
- 模式 / 日志级别 / TUN 写在 `settings.yaml`，换配置后仍保留
- `base.yaml` 只放运行时底座（DNS、TUN 参数等），不放节点/规则
- 监听地址与密钥由环境变量强制写入，写进 `base.yaml` 或订阅里都不生效

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
| `UI_PASSWORD` | `mihomo-ui` | 面板登录密码，**公网可达时务必修改** |
| `UI_LISTEN` | `0.0.0.0:7080` | 面板监听（host 网络下即本机端口） |
| `PROXY_LISTEN` | `127.0.0.1:7890` | HTTP/SOCKS5 代理监听；设为空则不开端口 |
| `MIHOMO_LISTEN` | `127.0.0.1:9090` | 内核控制 API 监听 |
| `MIHOMO_SECRET` | 每次启动随机 | 内核 API 密钥；要用别的客户端连内核时才需固定 |
| `DATA_HOME` | `data`（镜像内 `/data/mihomo-ui`） | 数据目录（配置、设置、内核 home） |
| `MIHOMO_BIN` | `./mihomo`（镜像内 `/mihomo`） | 内核二进制路径 |
| `MIHOMO_PROXY` | 开了 `PROXY_LISTEN` 就用它，否则直连 | 下载订阅走的代理，`direct` 强制直连 |
| `TZ` | `Asia/Shanghai` | 时区 |

常规使用只需关心前三个，其余都有可用默认值。

- **三个 `*_LISTEN` 都是 `host:port`**，格式不对启动即报错并指明是哪个变量
- **代理端口完全由 `PROXY_LISTEN` 决定**：`base.yaml` 或订阅里写了 `mixed-port` 都不生效；设为空则不开端口；监听非回环地址还需在 `base.yaml` 开 `allow-lan`
- **`MIHOMO_LISTEN` 撞端口时改它**：9090 被占（Prometheus 等）会导致启动失败，日志会直说
- **`MIHOMO_SECRET` 不设则每次启动随机**，要用别的客户端连内核 API 时才需固定，也可从 `data/mihomo/config.yaml` 的 `secret` 读到
- **`MIHOMO_PROXY` 未设时回落** `HTTP_PROXY` / `http_proxy`
- 前端已编译进二进制，路径默认相对当前目录，直接跑 `./mihomo-ui` 即可；镜像在 ENV 里覆盖成绝对路径

---

## 鉴权

登录后拿到的是**随机会话令牌**（有效期 7 天，每次使用顺延），密码本身不会存进浏览器。令牌只存在服务端内存里，重启面板即让所有登录失效。

脚本调 API 也走同一条路——先登录换令牌：

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:7080/api/login \
  -H 'Content-Type: application/json' -d '{"password":"你的密码"}' | jq -r .token)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7080/api/overview
```

---

## TUN

默认关闭（`settings.yaml` 里 `tun-enable: false`）。开启需 `NET_ADMIN` 与 `/dev/net/tun`（上方启动命令已含）。

> WSL 下可能与 Windows 自身 TUN 冲突，按需使用，不建议常开。
