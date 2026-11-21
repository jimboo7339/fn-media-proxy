# fn-media-proxy 可行性评估

> 结论先行：**技术上可行，但它不是"加个代理"的量级，而是"自己把飞牛影视重新实现成一个 Emby 服务端"。**
> 真正决定成败的不是架构，而是三件工程工具：接口抓包与平台知识（已完成约 80%）、
> 多客户端协议回归矩阵、以及能不能拿到飞牛媒体库的原始索引能力。

---

## 1. 先厘清一个前提

"飞牛兼容了一套类似 emby 的接口"——这句话的方向是反的，需要纠正，因为它直接决定项目形态。

- 飞牛影视自己的客户端（`com.trim.media`，Flutter）调用的是 `/v/api/v1/*`、`/v/api/v2/*`：
  `mediadb/list`、`item/list`、`season/list`、`episode/list`、`play/info`、`stream`、`media/range/{guid}`、
  `play/record`、`item/watched`、`person/list` ……
- 所有 `/v/api/v1` 请求必须带 **`Authx` 签名头**：`sign = MD5(apiKey_path_nonce_ts_bodyMD5_secret)`，
  且必须带 `Cookie`（否则飞牛反代会把 API 请求兜底成 SPA 的 HTML）。
  登录是 `POST /v/api/v2/user/loginByPassword`，密码 **SHA256 hex**。
- 它并没有另外提供一套"Emby 兼容接口"可供转发。第三方播放器之所以"能部分用"，是因为它们
  在适配器里手搓了飞牛私有接口（参照 Entryway / Cinetry / RainPlayer / yueyin-tv 这类项目）。

因此本项目不是"透明反代"，而是 **协议翻译服务**：

```
Emby Client ──HTTP(Emby 协议)──> fn-media-proxy ──署名后的 /v/api/v1──> mediasrv(飞牛影视)
   Infuse / VidHub / SenPlayer / Emby App / Kodi     (网关 + 会话 + 缓存 + 媒体代理)
```

可行性的判断标准就变成一句话：**Emby REST 协议的全部语义能否被飞牛的能力面表达出来。**
答案分层：核心播放链路能完整表达，媒体库管理与部分精选语义不能，必须靠网关自己补。

---

## 2. 可行性评分（按 Emby API 能力分级）

| # | 能力 | 飞牛侧对应 | 实现难度 | 结论 / 做法 |
|---|---|---|---|---|
| 1 | 连通探测 `/System/Info/Public`、`POST /Users/AuthenticateByName` | v2 登录 + token | 低 | 直接做。密码双向归一（明文↔SHA256 hex） |
| 2 | 媒体库与首页 `/Users/{u}/Views` | `mediadb/list`，`category: TV/Movie/Other/Live` | 低 | 直接映射 CollectionType |
| 3 | 浏览/搜索 `/Items`、`ParentId`、`Recursive`、`IncludeItemTypes`、分页 | `item/list`（`ancestor_guid` 库级 / `parent_guid` 剧季级互斥）、`season/list`、`episode/list` | 中 | 能做，但**服务端搜索缺失**：飞牛无 `item/search`，搜索只能拉全库本地过滤 → 必须落地索引缓存 |
| 4 | 详情 `/Items/{id}`、演职员、分集 | `item/{guid}` + `person/list/{guid}` | 中 | 能做。演职员挂"季"上，剧级要逐季探 |
| 5 | **播放** `/Items/{id}/PlaybackInfo` → `/Videos/{id}/stream` | `play/info` → `stream` → `media/range/{guid}` | **中高** | 本项目的命门，见 §3 |
| 6 | 进度/已看 `/Sessions/Playing*`、`/Items/{id}/Progress` | `play/record`、`item/watched`（POST 已看 / DELETE 取消） | 低 | 能做。注意 `watched_ts` 语义混用（既像进度又像时间），继续观看排序需网关自记 |
| 7 | 收藏 `/Users/{u}/FavoriteItems/{id}` | `item/favorite`（PUT/DELETE） | 低 | 直接做 |
| 8 | 图片 `/Items/{id}/Images/{type}` | 只有海报 / Logo / 横版剧照（`sys/img{path}`） | 低 | 只能全类型回落到同一批图；Backdrop/Thumb 复用横版剧照 |
| 9 | 转码 `SupportsTranscoding`、`/Videos/{id}/master.m3u8` | **无** | — | 不做转码，作为直连型服务器（等价"Disable transcoding"）。这是兼容性最大的现实边界 |
| 10 | 字幕 `/Videos/{id}/Subtitles/{i}/{name}.{ext}`（外挂/转 ASS）、次音轨选择 | `stream/list` 有 `subtitle_streams`（含 `is_bitmap`），**但没有取字幕文件的端点**（待真机验证） | 高 | 内嵌轨可声明；外挂字幕可能需要从文件路径经挂载盘读，或干脆不声明 |
| 11 | 剧集/合集/Genres/Studios/Playlists、筛选排序（Genre/Year/任意 SortBy） | **无接口** | 高 | 只能用"全库拉取 + 本地索引"模拟，或诚实返回空（更稳，不易被客户端判为异常） |
| 12 | 管理类：扫描媒体库、刷新元数据、用户管理、播放限制 | `item/refresh` 有；其余无 | 中 | 大部分返回空实现，Infuse 等在管理页会显得"功能缺失"，但播放不受影响 |
| 13 | 事件推送 `/embywebsocket` | 飞牛没有事件流 | 中 | 需网关自建 WebSocket 并伪造会话/播放状态。不少客户端不依赖它，可后置 |

---

## 3. 命门：播放链路

这是整个项目 80% 的价值和 80% 的坑所在。飞牛给客户端的是：

1. `POST /v/api/v1/play/info {item_guid}` → `media_guid`（**真正的可播放版本 id 与条目 id 不是同一个**，这是最容易踩的坑）
2. `POST /v/api/v1/stream {media_guid, ip(32位hex设备指纹), level:1, header:{User-Agent}}` → `file_stream`（真实路径、大小）、
   `video_stream`/`audio_streams`/`subtitle_streams`（codec/宽高/码率/位深/profile/HDR）、
   `cloud_storage_info`，以及 **`direct_link_qualities`（网盘直链）+ 需要的 Cookie/UA**
3. 本地 NAS 文件走 `GET /v/api/v1/media/range/{media_guid}?direct_link_quality_index=1`（要 authx，支持 Range）
4. 网盘文件（115/夸克/阿里/百度/123）拿到的是带时效的直链，且 115 要限速（1 req/s）、夸克要 10MiB 串行分块防风控、
   且**用飞牛官方播放器 UA 请求才不限速**

网关要做的事：

- **MediaSource 必须如实声明**：`Codec` 只能是单个值（`"h264,hevc"` 这种逗号串会让客户端在 DeviceProfile 匹配阶段静默放弃，
  表现为"点了没反应"）；`SupportsDirectPlay/SupportsDirectStream` 视情况给，`SupportsTranscoding=false`。
- **302 直连（Killer Feature）**：网盘场景把客户端 302 到真实直链，**NAS 的带宽完全不消耗**——
  这是自建影音最重要的体验，也是本项目必须做对的点。代价：直链会过期，要在播放前实时换链 + 短 TTL 缓存。
  兜底：客户端所在网络直连不到网盘时，回落到网关分块中转。
- **Range / 断点续传**：上游不是所有版本都正确返回 206，需要网关自己裁剪并回 206。
- **STRM**：飞牛支持 STRM 入库，网关解析 `.strm` 内容后 302 到目标 URL（可参考 MediaWarp 的 HTTPStrm / AlistStrm 两套模式）。
- **本地文件零拷贝加速（本项目独有的上限）**：同机部署时，根本不必经 `media/range` 中转——
  网关可以直接按 `file_stream.path`（`/vol0x/...`）打开文件，用 `http.ServeContent` 提供零拷贝的 Range 响应。
  **前提是网关以 root 或同组权限运行，且能挂载同样的存储路径。** 这一条能把本地库的性能做到接近原生文件服务器。
- **ABR/HLS**：飞牛没有转码，客户端若坚持要 HLS 会直接失败。要接受"部分客户端/部分低端设备不合规码流放不了"。

---

## 4. 真正的风险（按杀伤力排序）

1. **私有协议的漂移（最高风险）**。`authx` 的 apiKey/apiSecret、登录方式、字段名都随飞牛影视版本变过
   （现有逆向记录就明确标注了"真机 0.9.8 / 1.12.0 契约"）。飞牛每次更新都可能让网关整体失效。
   - 缓解：把 `fn/` 层做成**可版本化的适配器 + 契约测试**；网关侧维护"探测自检"页；对上游 5000 invalid sign 做显式告警；
     发布规则写明"跟随飞牛影视版本发布"。
2. **没有服务端搜索 / 聚合查询（架构级风险）**。`item/list` 只支持按 `ancestor_guid`/`parent_guid` + `tags.type` + 简单排序分页。
   客户端喜欢 `SearchTerm`、`Genres`、`Filters=IsPlayed`、`SortBy=DatePlayed` 这类查询——每个都意味着"拉全库再过滤"。
   - 缓解：**必须自建索引**（SQLite/FTS5 或内存），冷启动全量同步，之后增量；大库（10 万集）首次同步要算进设计。
3. **客户端兼容性是乘法而非加法**。Infuse / VidHub / SenPlayer / Fileball / Emby Android / Emby TV / Kodi(Emby 插件) /
   Emby Theater 各自对字段非空性、大小写路径、`/emby` 前缀、`PlaySessionId`、日期格式（空串 → `DateTime.parse` 崩溃）的要求不同。
   已知的坑随手就能列出一串：路径大小写敏感、`//` 双斜杠触发 301 导致 POST 降级 GET、必填字段缺失导致 `SerializationException`、
   MediaSourceId 与上游版本 id 不一致导致 302 失效。
   - 缓解：**以客户端为单位的回归矩阵**（见 §6 Phase 4），宁可为某个客户端做 UA 分支。
4. **多用户/多账号语义**。飞牛按账号存进度与收藏；Emby 缺省的"多用户"在此没有意义。但客户端频繁重连会重复登录，
   飞牛登录接口有频控 → 必须做 token 复用 + 静默刷新，否则会出现"用一会儿让重新登录"。
5. **反代/WAF/远程访问**。远程访问（FN Connect、雷池/nginx 反代、端口映射）下，302 的直链、绝对 URL、Host 推断都容易出错。
6. **法律与维护**。这是对闭源私有接口的逆向适配——按现状可做个人/小圈子工具；对外分发要注意不内置密钥、不承诺稳定性。
   已有实践者（见 §7）说明这个生态是默认接受的，但项目定位应当是"社区维护的适配层"，而不是"官方替代品"。

---

## 5. 已经被验证过的部分（说明这不是空想，以及为什么还要自己做）

竞品/先例的存在同时给出了"下限已证明"和"上限在哪"：

- `AkimioJR/MediaWarp`（~790★，活跃）：前置反代，对 Emby/Jellyfin/飞牛影视做 STRM 302、客户端过滤、Web 美化。
  **它是"改响应"路线，不是"协议翻译"路线**，功能集中在外链加速。
- `ssabv/fnos-emby-bridge`（~0★，2026-09 起停更）：**正是你说的架构**——把飞牛伪装成 Emby 服务端，
  已打通登录/浏览/详情/PlaybackInfo/Range 流/进度/收藏/图片/云盘直链，代码里沉淀了大量真机结论
  （如 `media/guid` 与版本 id 分离、Yamby 必填字段崩溃、PlaySessionId 必须每播随机、非 root 时间戳等）。
  但它的弱点是：搜索/分类/Person/合集的语义补偿几乎靠"拉全库过滤"、无转码、无字幕端点、无索引层、已停更。
- `xiaotian89/fnos-nfo-bridge`、`liangpangzhi/metabridge`：走"读写飞牛刮削 SQLite（`trim.media`）"的路线导出 NFO/写回。
  说明**飞牛媒体库落库在本地 SQLite**——这是一条潜在的高价值捷径（见 §8 的"大胆选项"）。
- 客户端侧：`Entryway`（聚合飞牛影视的播放器）、`Cinetry`（飞牛数据导入）、`RainPlayer`、`yueyin-tv`
  都在各自实现飞牛适配，反过来说明接口是稳定可用的。

结论：**技术风险主要在"广度与维护"，不在"能不能跑通"。**

---

## 6. 建议路线（4 个阶段，每阶段都有可演示产出）

**技术选型**：Go（单二进制、交叉编译到 x86/arm、`httputil.ReverseProxy`+`http.ServeContent` 天然适合流式，
Docker 与 fnOS `.fpk` 双形态好打包）。不建议 Node/Python 承接大流量流式场景。

### Phase 0 —— 打通（1~2 周）
`System/Info`、鉴权、`Views`、`Items` 浏览、`Items/{id}` 详情、`PlaybackInfo` + `/Videos/{id}/stream`（Range 透传）、
`Sessions/Playing` 进度、图片。**验收：Infuse / Emby Web 能登录、浏览、点开播放、拖进度条、进度回写飞牛。**

### Phase 1 —— 播放体验（2~3 周）
真实 MediaStreams 声明、多版本（`stream/list` 的 files）、网盘直链 302 + 过期续链 + 中转兜底、
本地文件零拷贝（路径直读优先，`media/range` 兜底）、STRM 解析、字幕轨声明。**验收：网盘片源 302 播放、NAS 零中转带宽。**

### Phase 2 —— 满足 Emby 语义（2~3 周）
`item/list` 全量落地的本地索引（SQLite FTS5）+ 增量同步；搜索、筛选、排序、`Latest`、`Resume`、`NextUp`、
`FavoriteItems`、`IsPlayed`、People/PersonIds、`/emby` 前缀与大小写归一、WebSocket 最小实现（`Sessions`、`Playing`）。
**验收：首页"最近添加/继续观看/收藏"三个模块在 Infuse、SenPlayer、Emby TV 上都正确。**

### Phase 3 —— 客户端矩阵与运维（持续）
客户端回归矩阵（至少 6 个）：字段非空补齐、UA 分支、日期 null 语义、错误码映射；admin 后台（配置/自检/日志/索引状态）；
缓存（图片、列表、直链）；Docker + `.fpk`；版本探测。

### Phase 4 —— 加分项
合集/Genres 从索引推导、同人图/Logo 补齐、播放列表（网关本地存储）、多 NAS 聚合、Emby/Jellyfin 双协议同一后端。

---

## 7. 必须现在回答的 4 个问题

1. **主要目标客户端是谁？**（Infuse / Apple TV、Emby 官方 Android/TV、SenPlayer/VidHub、Kodi+Emby 插件、Emby Web）
   这决定了 Phase 2/3 的优先级——先打透 2 个客户端，比"所有客户端都能连上但都不好用"价值高得多。
2. **片源分布？**（本地直连盘 / 网盘 STRM 各占多少）——决定"零拷贝直读"和"302 + 续链"哪个先做。
3. **网关跑在哪？**（fnOS Docker / 旁路机器 / 以 root 挂载存储）——决定能否走本地文件直读、能否用 `.fpk`。
4. **是否接受"跟随飞牛版本维护"**？飞牛升级后可能需要 1~3 天修复。不接受就只能做只读/静态方案。

---

## 8. 两个"更激进"的选项（如果想让项目真正难被超越）

- **A. 面向前端直改（最小改动路径）**：与其翻译飞牛→Emby，不如给飞牛 Web 前端注入 Emby 兼容层，
  或直接做"飞牛账号统一入口"。适合快速满足自家需求，不适合做通用产品。
- **B. 双数据源（上限最高）**：网关同时对接"飞牛 HTTP 接口 + 飞牛媒体库 SQLite（只读）"。
  索引、元数据、演员、合集从库直接读，播放与进度走 HTTP。这样搜索与聚合筛选不再需要"拉全库",
  且能绕开大部分 `item/list` 的表达力限制。
  **代价**：DB schema 会变、需要只读挂载与版本探测（schema 校验失败即回落到纯 HTTP 模式）。
  这是把项目从"能用的桥"变成"能打的产品"的关键一步，建议在 Phase 2 评估。

---

## 9. 最终判断

- **可行性：可以做到。** 核心播放链路（登录 → 浏览 → 详情 → 开始播放 → Range 拖动 → 进度回写）已被实践验证；
  上游接口与签名机制清楚；生态里已有客户端在有意适配飞牛，说明行为稳定。
- **工作量：中大型项目。** 保守估计，单人做到"Infuse + Emby TV 稳定可用"约 **4~6 周**的业余投入；
  做到"多客户端 + 索引 + 网盘 302 + 后台"约 **2~3 个月**。
- **最大不确定性：不是代码，而是三件事**——多客户端字段契约、私有接口版本漂移、以及无转码这一硬边界。
  这三件事都可以通过"收缩目标客户端 + 明确不支持清单 + 契约测试"来控制。
- **建议**：先做 Phase 0 的最小可跑通版本（一个二进制 + 一个 Infuse 能播就行），用真实体验确认收益，
  再决定是否投入索引层和 SQLite 双数据源。**如果目标是自用，Phase 0+1 就足够；如果目标是开源项目，Phase 2 是分水岭。**
