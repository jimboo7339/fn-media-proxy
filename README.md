# fn-media-proxy

[![release](https://github.com/jimboo7339/fn-media-proxy/actions/workflows/release.yml/badge.svg)](https://github.com/jimboo7339/fn-media-proxy/actions/workflows/release.yml)
[![license](https://img.shields.io/github/license/jimboo7339/fn-media-proxy)](LICENSE)
[![ghcr](https://img.shields.io/badge/ghcr.io-fn--media--proxy-blue)](https://github.com/jimboo7339/fn-media-proxy/pkgs/container/fn-media-proxy)

把 **飞牛影视（fnOS media）** 包装成一个 **Emby 兼容服务端**，让 SenPlayer、VidHub、Hills、Capyplayer、Emby Android 等第三方播放器直接连飞牛影视库。

播放走**纯 302 直链**：网关把客户端重定向到飞牛解析出的网盘/CDN 直链，**NAS 不消耗转发带宽**。

> 状态：Phase 0 已完成，13 项协议冒烟测试全通过（对真实飞牛环境验证）。

---

## 为什么需要它

飞牛影视没有开放 API。它的客户端调用的是一套私有接口（`/v/api/v1/*`、`/v/api/v2/*`，所有 v1 请求需 `Authx` 签名），
第三方播放器想接飞牛只能各自硬编码适配，能力参差不齐。

本项目把这些私有接口翻译成标准 Emby REST 协议，于是：

- 任何支持 Emby 的播放器都能连飞牛影视库
- 网盘片源走 302 直连，**不占用 NAS 上传带宽**
- 播放进度、收藏、已看状态**自动同步回飞牛**

```
Emby 客户端 ──Emby REST──> fn-media-proxy ──带 Authx 签名的 /v/api/v1──> mediasrv（飞牛影视）
     │                                                                        │
     └──────────── 302 Location: 网盘/CDN 直链（NAS 零带宽）◄──────────────────┘
```

## 快速开始

### Docker（推荐）

```bash
docker run -d --name fn-media-proxy --restart unless-stopped \
  -p 8096:8096 \
  -e FN_BASE=http://192.168.100.10:8005 \
  ghcr.io/jimboo7339/fn-media-proxy:latest
```

或用 `docker compose`（见 [docker-compose.yml](docker-compose.yml)）：

```bash
docker compose up -d
```

### 从源码构建

```bash
git clone https://github.com/jimboo7339/fn-media-proxy.git
cd fn-media-proxy
docker compose up -d --build
```

### 二进制

从 [Releases](https://github.com/jimboo7339/fn-media-proxy/releases) 下载对应平台：

```bash
FN_BASE=http://192.168.100.10:8005 ./fn-media-proxy-linux-amd64 -listen :8096
```

## 客户端接入

在播放器里添加 Emby 服务器：

| 字段 | 值 |
|---|---|
| 服务器地址 | `http://<运行网关的机器>:8096` |
| 账号 | 飞牛影视账号 |
| 密码 | 飞牛影视密码 |

已验证客户端：SenPlayer、VidHub、Hills、Capyplayer、Emby Android。
官方 Emby 客户端带 `/emby` 前缀也能正常访问；路径大小写不敏感。

## 配置项

| 环境变量 | 说明 | 默认 |
|---|---|---|
| `FN_BASE` | 飞牛影视地址（**必填**），如 `http://192.168.100.10:8005` | — |
| `SERVER_NAME` | 客户端里显示的服务器名 | `fnOS Media` |
| `LISTEN` | 监听地址 | `:8096` |
| `VERBOSE` | 非空则打印访问日志 | 打印 |

启动时会自检飞牛连通性。若日志出现 `签名校验失败（code=5000）`，说明飞牛升级改动了签名密钥，
需要更新 [internal/fn/const.go](internal/fn/const.go) 里的 `APIKey` / `APISecret`。

## 已实现的能力

| Emby 端点 | 状态 |
|---|---|
| `System/Info`、`System/Info/Public`、`System/Configuration` | ✅ |
| `Users/AuthenticateByName`、`Users/Me`、`Users/{id}`、`Users/Public` | ✅ |
| `Users/{uid}/Views` | ✅ 映射飞牛媒体库 |
| `Items`、`Users/{uid}/Items`（ParentId / 分页 / IncludeItemTypes） | ✅ |
| `Items/{id}`（含演职员、ImageTags） | ✅ |
| `Shows/{id}/Episodes`（自动逐季展开） | ✅ |
| `Items/{id}/PlaybackInfo`（真实码流声明：容器/codec/分辨率/音轨） | ✅ |
| `Videos/{id}/stream` → **302 直链** | ✅ |
| `Items/{id}/Images/{type}` | ✅ 代理飞牛图片 |
| `Sessions/Playing*`、`Items/{id}/Progress` | ✅ 回写飞牛 |
| `FavoriteItems`、`PlayedItems` | ✅ |
| `/emby` 前缀、小写路径、重复斜杠 | ✅ |

## 已知限制

- **搜索不支持**：飞牛无搜索接口，`SearchTerm` 返回空集（Phase 2 用本地索引实现）。
- **不转码**：飞牛没有转码能力，`SupportsTranscoding=false`；客户端必须能直解 HEVC/MKV。
- **拖动进度可能受限**：光鸭等 CDN 直链声明 `Accept-Ranges: bytes` 但**不遵守 Range 头**
  （实测返回 `200` + 1 字节，不给 `206` 也不给总大小）。顺序播放正常，但精确跳转/断点续传可能失败。
  这是"纯 302 零带宽"的已知代价，详见 [docs/contract.md](docs/contract.md)。
- **外挂字幕**：飞牛无字幕文件端点，字幕轨仅在 `stream` 返回元数据时才声明。
- **合集 / Genres / 播放列表**：返回空集。
- **协议漂移**：飞牛升级可能改动签名或字段，需跟随适配。

## 开发

```bash
# 探测飞牛接口（只读，不写进度/收藏）
FN_BASE=http://192.168.100.10:8005 FN_USER=your_user FN_PASS=your_pass \
  python tools/probe/probe.py full

# 对运行中的网关跑端到端冒烟测试
powershell -ExecutionPolicy Bypass -File tools/smoke.ps1 -Base http://127.0.0.1:18096

# 本地构建
go build -o fn-media-proxy ./cmd/fn-media-proxy
```

文档：

- [docs/feasibility.md](docs/feasibility.md) — 可行性与路线图
- [docs/contract.md](docs/contract.md) — 飞牛接口实测契约（签名算法、端点行为、Range 异常证据）
- [DEPLOY.md](DEPLOY.md) — NAS 部署分步指南

## 目录结构

```
cmd/fn-media-proxy/   入口（配置、启动、自检）
internal/fn/          飞牛私有接口客户端（Authx 签名、登录、接口封装）
internal/emby/        Emby 协议层（路由、模型转换、播放、会话）
tools/probe/          飞牛接口探测脚本（Python，只读）
tools/smoke.ps1       端到端冒烟测试
```

## 发版

推送 tag 会自动构建多架构镜像并发布 Release：

```bash
git tag v0.1.0
git push origin v0.1.0
```

产物：`linux/amd64`、`linux/arm64`、`linux/arm/v7`、`windows/amd64`、`darwin/arm64` 二进制 + `ghcr.io` 多架构镜像。

## 免责声明

本项目通过逆向飞牛影视私有接口实现兼容层，仅供个人自用。飞牛官方未提供公开 API，
升级可能导致不可用。请勿用于商业用途，自行承担使用风险。

## 许可

[MIT](LICENSE)
