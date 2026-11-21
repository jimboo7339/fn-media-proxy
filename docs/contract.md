# fn-media-proxy · 环境实测契约（真机验证记录）

> 环境：192.168.100.10:8005 · 账号 `demo`
> 日期：2026-11-21
> 目的：把后续 Go 代理必须照抄的真实接口行为固化下来。凡标注「⚠️ 待验证」需在写代理时用真实客户端再确认。

---

## 1. 认证与签名

- 登录：`POST /v/api/v2/user/loginByPassword`
  body：`{"app_name":"trimemedia-web","username":"<u>","password":"<SHA256hex>"}`
  返 `data.token`（本环境为 32 位 hex）。
- 密码形态：`demo` 实测为**明文→SHA256 hex** 可登录；
  若已是 64 位 hex 则直接透传（不二次哈希）。
- **Authx 签名头**（所有 `/v/api/*` 必须带，否则 `code=5000 invalid sign`）：
  ```
  nonce= 6 位数字，不以 0 开头
  ts= Unix 毫秒
  bodyMD5= MD5(body字符串) ；无 body 时 = MD5("")
  sign= MD5( apiKey + "_" + path(不含query) + "_" + nonce + "_" + ts + "_" + bodyMD5 + "_" + apiSecret )
  Authx: nonce=<n>&timestamp=<ts>&sign=<sign>
  ```
- 必须带 `Cookie: mode=relay`（无 token 时也是；否则飞牛把 API 兜底成 SPA HTML）。
  带 token 后 `Cookie: Trim-MC-token=<token>; mode=relay` + `Authorization: <token>`。

⚠️ `apiKey`/`apiSecret` 逆向自旧版本 fntv-electron；本环境 2026-11 实测**仍有效**。

## 2. 核心端点（已实测成立）

| 端点 | 说明 | 实测结果 |
|---|---|---|
| `GET mediadb/list` | 全部媒体库 | ✅ 2 库，`category: TV`，posters 数组 / panel 字段 |
| `POST item/list` | 分页+tags过滤，`ancestor_guid`(库级) 或 `parent_guid`(剧/季) 互斥 | ✅ total=837；`tags.type` 支持 Movie/TV/Season/Episode/Video/Directory |
| `GET item/{guid}` | 条目详情 | ✅ 剧级 rich：genres(数字id)、logos、backdrops、original_title、production_countries、status |
| `GET season/list/{tvGuid}` | 剧下的季 | ✅ 返回 `type:"Season"`，带本地集数 |
| `GET episode/list/{seasonGuid}?guid={seasonGuid}` | 季下的集 | ✅ **必须传季 guid**；传剧 guid 返回 `null`（实测） |
| `POST play/info {item_guid}` | 拿到**可播版本** | ✅ 对剧返回该剧集下一集 Episode；`media_guid` ≠ 条目 guid ⚠️ |
| `POST stream` | 直链 + 全部码流信息 | ✅ 见 §3 |
| `GET stream/list/{guid}` | 流列表 | ⚠️ 剧集级返回空数组（单集流详情走 `stream`） |
| `GET person/list/{guid}` | 演职员 | ⚠️ 未在本环境单独验证（参照实现确认可用） |
| `POST item/favorite` PUT/DELETE，`item/watched` POST/DELETE，`play/record`，`item/refresh` | 状态类 | ⚠️ 未动（探测脚本保持只读） |

## 3. stream 接口（播放命门）— 已实测关键事实

`POST stream`
```json
{
  "media_guid": "<media_guid>",
  "ip": "<32位hex设备指纹>",
  "level": 1,
  "header": {"User-Agent": ["1.12.0 (com.trim.media; build:1120009; Android 16) Flutter/3.11.5"]}
}
```
响应顶层：`file_stream` `video_stream` `audio_streams` `qualities` `cloud_storage_info` `header` `direct_link_qualities`

- **直链位置**：`data.direct_link_qualities[]`
  ```
  [{ "bitrate": 0, "resolution": "原画", "url": "https://sh01-httpdown.guangyacdn.com/download/?fid=...&ts=...&s=...&at=...", "is_m3u8": false }]
  ```
- **云盘信息**：`cloud_storage_info` = `{valid:true, disabled:false, cloud_storage_type:9001, cloud_nick_name:"strm文件", is_vip:false}`
  （`cloud_storage_type:9001` 表示 STRM 直链；1百度/2阿里/3=115/4夸克/5=123 是本地云盘类型，STRM 单独是这个值）
- **多清晰度** `qualities[]`：4k/1080/720/540/480/360… 每档 `{bitrate, resolution, progressive, is_m3u8:false}`
  但 `direct_link_qualities` 只给一个「原画」。**换清晰度可能需要按 resolution 再调 stream，⚠️ 待验证**。
- **直链 UA**：`data.header["User-Agent"]` 返回官方播放器 UA，**带它请求直链**（部分网盘限定速）。

### ⚠️ 关键：光驱(guangya) CDN 直链的 Range 行为异常（实测结论）

用 curl/HttpClient 对 `direct_link_qualities[0].url` 实测：
- `HEAD` → `200`，`Content-Type: video/x-matroska`，`Content-Disposition: attachment; filename="两京十五日.S01E01.mkv"`，`Accept-Ranges: bytes`，但没有 `Content-Length`。
- **`Range: bytes=0-0` → 仍返回 `200`，不是 `206`，`Content-Length: 1`（body 只发 1 字节），无 `Content-Range`。**
- 即：**服务端声明支持 Range 但实际不遵守 Range 头**，既不给 206 也不给全量，只发你请求字节数的前 1 字节。

### 对架构的影响（重要）

1. **纯 302 直连下，客户端拖动进度条可能失败**——因为标准 Range/206 无法工作，且拿不到总大小。
2. `PlaybackInfo` 的 `Size` 无法从直链 HEAD 精确拿到（应放向量总大小或留空/估计）。
3. `media/range/{guid}` 对 STRM 剧集返回 **`400`**（该端点可能只对本地 NAS 文件有效，对云盘/STRM 无效）。

> 这意味着「纯 302 直连」对这个**光驱(guangya)片源**是有折价的：
> 它只能作为「打开播放、顺序播放」的直链；一旦需要声道切换、进度精确跳转、断点续传，就需要网关注到播放器认可的**可寻址流**。
> 飞牛官方播放器能拖进度，大概率是因为它用「端到端下载缓存」或走了另一条播放路径，而不是靠 HTTP Range。

## 4. 待确认/实测决策点（已定）

- [x] **目标客户端**：SenPlayer、Emby Android、VidHub、Hills、Capyplayer
- [x] **片源**：主要走光鸭(guangya) STRM 直链
- [x] **传输方案**：方案 C —— 先做纯 302 直链，遇到进度问题再局部升级为网关中转
- [x] **部署**：fnOS 上 Docker

## 5. 端到端冒烟测试结果（2026-11-21）

对真实飞牛 192.168.100.10:8005，模拟 Emby 客户端跑完整链路，**13/13 通过**：

| 检查项 | 结果 |
|---|---|
| `System/Info/Public` | ✅ |
| `Users/AuthenticateByName`（demo） | ✅ |
| `Users/Me` | ✅ |
| `Users/{uid}/Views` | ✅ 7 个媒体库 |
| `Items?ParentId={lib}` | ✅ 837 条 |
| `Items/{id}` | ✅ 含 ImageTags(Logo,Primary) |
| `Shows/{id}/Episodes` | ✅ 自动逐季展开，S1E1… |
| `Items/{id}/PlaybackInfo` | ✅ MKV / hevc 3840x2160 / 2 音轨 |
| `Videos/{id}/stream` → 302 | ✅ Location 指向光鸭 CDN |
| `Items/{id}/Images/Primary` | ✅ 1.48 MB webp |
| `Sessions/Playing/Progress` | ✅ 204 |
| `/emby` 前缀 | ✅ |
| 小写路径 | ✅ |