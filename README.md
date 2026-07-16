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
data/mihomo/subs/<id>.yaml     原始 YAML（可编辑）
        ↓  激活 / 更新 / 保存且为当前
合并 + preserveKeys
        ↓
data/mihomo/config.yaml        内核运行时
        ↓
PUT /configs?force=true        热重载（不重启进程）
```

| 路径 | 含义 |
|------|------|
| `config/config.yaml` | 模板 |
| `data/mihomo/config.yaml` | 内核运行时配置 |
| `data/mihomo/subs/<id>.yaml` | 每份订阅的**原始**内容 |
| `data/ui/subscriptions.json` | 订阅元数据 |
| `data/mihomo/providers/*.yaml` | 嵌套 provider 缓存 |

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

```bash
cd /home/xin/mihomo-ui
cp .env.example .env   # 改掉两个密码
mkdir -p data/mihomo data/ui

# 去掉旧双容器（若有）
docker rm -f mihomo mihomo-ui 2>/dev/null || true

sudo docker compose up -d --build
```

- 面板：http://127.0.0.1:8080 （需 UI_PASSWORD）
- 代理：`127.0.0.1:7890`
- 内核 API：仅本机 `127.0.0.1:9090`

## 功能

- 首页：模式 / TUN
- 节点：策略组、测速
- 配置：单选当前；「编辑配置」= 原始订阅文件；更新 = 重下并热重载
- 日志：实时流

## 开发

```bash
export UI_PASSWORD=dev
export MIHOMO_SECRET=change-me
./scripts/run-ui.sh
```
