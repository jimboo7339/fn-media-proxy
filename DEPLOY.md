# 在飞牛 NAS 上部署 fn-media-proxy

## 前置条件

- 飞牛 fnOS 已开启 SSH（或能用终端）
- 已安装 Docker（fnOS「应用中心」里装 Docker 应用）
- 飞牛影视正常运行，记下它的地址，例如 `http://192.168.100.10:8005`

## 步骤

### 1. 上传并解压

把 `fn-media-proxy.zip` 传到 NAS，例如放到 `/vol1/1000/docker/`：

```bash
cd /vol1/1000/docker
unzip fn-media-proxy.zip
cd fn-media-proxy
```

### 2. 确认飞牛地址

打开 `docker-compose.yml`，把 `FN_BASE` 改成**你的飞牛影视地址**：

```yaml
environment:
  FN_BASE: "http://192.168.100.10:8005"   # ← 改成你的
```

> 网关容器和飞牛影视在同一个小主机/NAS 上时，直接用 NAS 的局域网 IP 即可；
> 如果换了网络环境，用 `host.docker.internal` 不一定可用，局域网 IP 最稳。

### 3. 构建并启动

```bash
docker compose up -d --build
```

> 如果提示 `docker compose` 不存在，试 `docker-compose up -d --build`。
> 首次构建需要下载 golang 镜像并编译，约 1~3 分钟。

### 4. 看日志确认

```bash
docker compose logs -f
```

正常应看到：

```
fn-media-proxy 启动：:8096 -> http://192.168.100.10:8005（Emby 兼容端口 :8096）
自检：飞牛返回 code=-2（通常为未登录，属正常）
```

如果看到 `签名校验失败（code=5000）`，说明飞牛版本升级改动了签名密钥，需要更新 `internal/fn/const.go` 里的值后重新构建。

### 5. 在播放器里连接

| 字段 | 值 |
|---|---|
| 服务器地址 | `http://<NAS的IP>:8096` |
| 账号 | 飞牛影视账号 |
| 密码 | 飞牛影视密码 |

支持：SenPlayer、VidHub、Hills、Capyplayer、Emby Android。

## 常见问题

**端口冲突**：如果 8096 被占用，改 `docker-compose.yml` 里的 `ports: "8096:8096"` 左侧端口，例如 `"18096:8096"`。

**容器里访问不到飞牛**：进入容器测一下连通性：

```bash
docker exec -it fn-media-proxy sh -c "wget -qO- http://192.168.100.10:8005/v/api/v1/mediadb/list | head -c 200"
```

能返回 `{"code":5000,"msg":"invalid sign"...}` 说明网络通（这个错误是预期的，因为没带签名）。

**改了代码后重新构建**：

```bash
docker compose up -d --build --force-recreate
```

## 验证部署是否成功

在任意能访问 NAS 的机器上：

```bash
curl http://<NAS的IP>:8096/System/Info/Public
```

返回带 `"ServerName":"fnOS Media"` 的 JSON 就说明服务正常。
