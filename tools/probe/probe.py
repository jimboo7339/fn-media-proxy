#!/usr/bin/env python3
"""飞牛影视接口探测脚手架（Phase 0 用）。

目的：在你自己的环境里，用最小依赖把飞牛私有接口逐项验证一遍，
产出后续写 Go 代理时必须照抄的"真机契约"。

用法：
    python tools/probe/probe.py login       # 仅验证登录 + 签名
    python tools/probe/probe.py full        # 全链路：库/条目/播放信息/直链

凭据从环境变量读，避免落盘：
    FN_BASE=http://192.168.100.10:8005
    FN_USER=admin
    FN_PASS=你的密码

注意：本脚本只读。所有 POST 都是查询性的（item/list、play/info、stream），
不写任何进度、收藏、已看状态。
"""

from __future__ import annotations

import hashlib
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ.get("FN_BASE", "http://192.168.100.10:8005").rstrip("/")
USER = os.environ.get("FN_USER", "")
PASS = os.environ.get("FN_PASS", "")

# 逆向自 fntv-electron / trimemedia-web 的固定密钥（飞牛版本变化后可能失效）
API_KEY = "NDzZTVxnRKP8Z0jXg1VAMonaG8akvh"
API_SECRET = "16CCEB3D-AB42-077D-36A1-F355324E4237"
APP_NAME = "trimemedia-web"
# 32 位十六进制设备指纹；真机格式，直链未绑定请求 IP
DEVICE_FP = "16a9d43e38a5f992b3f1c44d68940523"
# 飞牛官方播放器 UA：网盘直链（尤其夸克）用这个 UA 才不限速
FN_PLAYER_UA = "1.12.0 (com.trim.media; build:1120009; Android 16) Flutter/3.11.5"


def md5_hex(s: str) -> str:
    return hashlib.md5(s.encode()).hexdigest()


def sha256_hex(s: str) -> str:
    return hashlib.sha256(s.encode()).hexdigest()


def authx(path: str, body: str = "") -> str:
    """构造 Authx 头。path 不含 query；body 为空时 hash(MD5(""))。

    nonce 必须 6 位且不以 0 开头——前导零会返回 code=5000 invalid sign。
    """
    nonce = f"{random.randint(100000, 999999)}"
    ts = int(time.time() * 1000)
    sign = md5_hex(f"{API_KEY}_{path}_{nonce}_{ts}_{md5_hex(body)}_{API_SECRET}")
    return f"nonce={nonce}&timestamp={ts}&sign={sign}"


class FNClient:
    def __init__(self) -> None:
        self.token = ""

    def call(self, method: str, full_path: str, body: object | None = None) -> dict:
        path = full_path.split("?")[0]
        raw = json.dumps(body, separators=(",", ":")) if body is not None else ""
        req = urllib.request.Request(
            BASE + full_path,
            data=raw.encode() if raw else None,
            method=method,
        )
        if raw:
            req.add_header("Content-Type", "application/json")
        req.add_header("User-Agent", APP_NAME)
        req.add_header("Authx", authx(path, raw))
        # 无 token 时也必须带 Cookie（值任意），否则飞牛反代把 API 兜底成 SPA HTML
        req.add_header(
            "Cookie",
            f"{{Trim-MC-token=}}{self.token}; mode=relay" if self.token else "mode=relay",
        )
        if self.token:
            req.add_header("Authorization", self.token)
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read().decode())

    def v1(self, method: str, path: str, body: object | None = None) -> dict:
        return self.call(method, "/v/api/v1/" + path, body)

    def range_probe(self, media_guid: str) -> dict:
        """用签名代理端点 media/range 请求 0-0，验证服务端是否返回 206 及总大小。"""
        path = f"/v/api/v1/media/range/{media_guid}"
        qs = "?direct_link_quality_index=1"
        req = urllib.request.Request(BASE + path + qs, method="GET")
        req.add_header("User-Agent", APP_NAME)
        req.add_header("Authx", authx(path, ""))
        req.add_header("Cookie", f"{{Trim-MC-token=}}{self.token}; mode=relay" if self.token else "mode=relay")
        req.add_header("Authorization", self.token)
        req.add_header("Range", "bytes=0-0")
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                status = resp.status
                headers = {k: v for k, v in resp.getheaders()}
                body = resp.read(200)
                return {
                    "status": status,
                    "content_range": headers.get("Content-Range", ""),
                    "content_length_header": headers.get("Content-Length", ""),
                    "content_type": headers.get("Content-Type", ""),
                    "first_bytes_len": len(body),
                }
        except urllib.error.HTTPError as e:
            return {"status": e.code, "reason": str(e.reason)}

    def login(self, username: str, password: str) -> str:
        # 密码必须是 SHA256 hex；已哈希过的直接透传
        pwd = password if len(password) == 64 and all(c in "0123456789abcdefABCDEF" for c in password) else sha256_hex(password)
        res = self.call(
            "POST",
            "/v/api/v2/user/loginByPassword",
            {"app_name": APP_NAME, "username": username, "password": pwd},
        )
        if res.get("code") != 0:
            raise SystemExit(f"登录失败: {res}")
        self.token = (res.get("data") or {}).get("token", "")
        return self.token


def show(title: str, res: object, limit: int = 1200) -> None:
    txt = json.dumps(res, ensure_ascii=False, indent=2)
    print(f"\n=== {title} ===")
    print(txt[:limit] + ("\n... (截断)" if len(txt) > limit else ""))


def main() -> None:
    mode = sys.argv[1] if len(sys.argv) > 1 else "login"
    if not USER or not PASS:
        raise SystemExit("请先设置 FN_USER / FN_PASS 环境变量")

    c = FNClient()
    print(f"BASE = {BASE}")
    print(f"登录 {USER} ...")
    c.login(USER, PASS)
    print("token 获取成功，长度 =", len(c.token))

    dbs = c.v1("GET", "mediadb/list")
    show("mediadb/list", dbs)
    items = ((dbs.get("data") or []) or [])
    if not isinstance(items, list) or not items:
        return
    if mode == "login":
        return

    lib = items[0]
    print("\n选中媒体库:", lib.get("title"), lib.get("guid"), "category=", lib.get("category"))

    lst = c.v1(
        "POST",
        "item/list",
        {
            "tags": {"type": ["Movie", "TV", "Directory", "Video", "Season", "Episode"]},
            "sort_type": "DESC",
            "sort_column": "create_time",
            "exclude_grouped_video": 1,
            "page": 1,
            "page_size": 5,
            "ancestor_guid": lib["guid"],
        },
    )
    show("item/list (库级)", lst)

    rows = ((lst.get("data") or {}).get("list") or [])
    if not rows:
        return
    item = rows[0]
    guid = item["guid"]
    print("\n选中条目:", item.get("title"), "type=", item.get("type"), "guid=", guid)

    show(f"item/{guid}", c.v1("GET", f"item/{guid}"))
    show(f"stream/list/{guid}", c.v1("GET", f"stream/list/{guid}"), 2500)

    info = c.v1("POST", "play/info", {"item_guid": guid})
    show("play/info", info)

    media_guid = (info.get("data") or {}).get("media_guid") or guid
    print("\nmedia_guid =", media_guid, "(与本条目 guid 是否相同:", media_guid == guid, ")")
    stream = c.v1("POST", "stream", {
        "media_guid": media_guid,
        "ip": DEVICE_FP,
        "level": 1,
        "header": {"User-Agent": [FN_PLAYER_UA]},
    })
    show("stream（含网盘直链）", stream, 3000)

    # 精查：可能在顶层或 data 顶层有直链/云盘字段，完整落盘
    with open(os.path.join(os.path.dirname(__file__), "stream_full.json"), "w", encoding="utf-8") as f:
        json.dump(stream, f, ensure_ascii=False, indent=2)
    print("\n完整 stream 响应已写 tools/probe/stream_full.json")
    data = stream.get("data") or {}
    fst = data.get("file_stream") or {}
    print("\nfile_stream.path =", fst.get("path"))
    print("cloud_storage_type =", (data.get("cloud_storage_info") or {}).get("cloud_storage_type"))
    print("media_guid =", media_guid)

    # 验证飞牛签名代理端点 media/range 是否支持 Range（决定纯直连 vs 网关兜底）
    # 该端点需 authx 签名，GET 到 media/range/{media_guid}?direct_link_quality_index=1
    print("\n=== 验证 media/range Range 行为（只请求 0-0，判断是否 206/返回首字节） ===")
    try:
        cfg = c.range_probe(media_guid)
        print("range_probe =", cfg)
    except Exception as e:  # noqa: BLE001
        print("media/range 探测失败:", e)


if __name__ == "__main__":
    main()
