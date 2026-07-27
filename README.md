# Mihomo UI

单容器运行 [mihomo](https://github.com/MetaCubeX/mihomo) 内核 + 控制面板：配置管理、节点切换、连接与日志，开箱即用。

镜像：`ghcr.io/myflavor/mihomo-ui`（`linux/amd64` / `linux/arm64`）

---

## 快速开始

```bash
docker run -d --name mihomo-ui \
  --network host --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
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
| 面板 | http://127.0.0.1:7080 | 密码即 `UI_PASSWORD` |
| 代理 | `127.0.0.1:7890` | mixed-port（HTTP / SOCKS5），来自订阅 |
| 内核 API | `127.0.0.1:9090` | 面板连内核用，一般不直接访问 |

登录面板后在「配置」页添加订阅或上传 YAML，点卡片激活即生效。

> WSL 下开启 TUN 可能与 Windows 冲突，按需使用。

---

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `UI_PASSWORD` | `mihomo-ui` | 面板登录密码，**公网可达时务必修改** |
| `UI_LISTEN` | `0.0.0.0:7080` | 面板监听（host 网络下即本机端口） |
| `MIHOMO_API` | `127.0.0.1:9090` | 内核控制 API 监听（面板连内核用） |
| `MIHOMO_SECRET` | 每次启动随机 | 内核 API 密钥；要用别的客户端连内核时才需固定 |
| `DATA_HOME` | `data`（镜像内 `/data/mihomo-ui`） | 数据目录（配置、设置、内核 home） |
| `MIHOMO_BIN` | `./mihomo`（镜像内 `/mihomo`） | 内核二进制路径 |
| `TZ` | `Asia/Shanghai` | 时区 |

常规使用只需设 `UI_PASSWORD`，其余都有可用默认值。

- `UI_LISTEN` 与 `MIHOMO_API` 都是 `host:port`，格式不对启动即报错并指明是哪个变量
- `MIHOMO_API` 撞端口时改它：9090 被占（Prometheus 等）会导致启动失败，日志会直说
- `MIHOMO_SECRET` 不设则每次启动随机，可从 `data/mihomo/config.yaml` 的 `secret` 读到
- 代理端口（`mixed-port`）由订阅或 `override.yaml` 决定
- 面板下载订阅走标准 `HTTP_PROXY` / `HTTPS_PROXY`，不设则直连

---

## 配置如何生效

```text
mihomo/config.yaml = 当前配置 ⊕ override.yaml ⊕ settings 开关 ⊕ 系统强制
```

```text
data/
  mihomo/config.yaml      # 内核运行配置（合并结果）
  ui/
    override.yaml         # 运维者底线（覆盖订阅，首次从模板生成，之后不覆盖用户编辑）
    settings.yaml         # 面板开关 + 配置列表
    config/<id>.yaml      # 各配置原始内容
```
