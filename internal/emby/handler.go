// Package emby 实现一个"冒充 Emby 服务端"的 HTTP 层，内部把请求翻译成飞牛影视私有接口。
//
// Phase 0 目标：让 SenPlayer / VidHub / Hills / Capyplayer / Emby Android
// 能连接、浏览、点开播放。播放走"纯 302 直链"（飞牛直接给出网盘/CDN 直链）。
package emby

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/user/fn-media-proxy/internal/fn"
)

// embySession 一个客户端登录会话，持有该用户自己的飞牛 token。
type embySession struct {
	Client   *fn.Client
	FNToken  string
	UserID   string
	UserName string
	PassHash string // SHA256 hex，token 失效时静默重登用
}

// Handler 是 Emby 协议层的总入口。
type Handler struct {
	// SessionTTL 登录会话缓存窗口（秒）。窗口内重复登录不复用飞牛登录接口，避免频控。
	admin *fn.Client

	mu       sync.RWMutex
	sessions map[string]*embySession // embyToken -> session
	userByID map[string]*embySession

	ServerName string
	Version    string

	// OnLog 可注入日志函数（默认 log.Printf）。
	OnLog func(format string, args ...any)
}

// NewHandler 构造处理器。
func NewHandler(fnClient *fn.Client, serverName string) *Handler {
	if serverName == "" {
		serverName = "fnOS Media"
	}
	return &Handler{
		admin:      fnClient,
		sessions:   make(map[string]*embySession),
		userByID:   make(map[string]*embySession),
		ServerName: serverName,
		Version:    "4.8.0.0",
	}
}

func (h *Handler) logf(format string, args ...any) {
	if h.OnLog != nil {
		h.OnLog(format, args...)
		return
	}
	log.Printf(format, args...)
}

// staticUserID 由于飞牛账号与 Emby 用户是同一套，我们为每个用户名派生一个稳定的 GUID。
// Emby 客户端要求 UserId 是 GUID 形态。
func stableUserID(username string) string {
	sum := fn.SHA256Hex("fn-media-proxy:" + username)
	// 取 32 位 hex 并格式化为 GUID
	s := sum[:32]
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// newToken 生成客户端访问令牌（32 位 hex）。
func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sessionByToken 按 api_key / X-Emby-Token / Authorization 找会话。
func (h *Handler) sessionByToken(r *http.Request) *embySession {
	tok := apiKey(r)
	if tok == "" {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[tok]
}

// registerSession 注册会话（登录成功后调用）。
func (h *Handler) registerSession(sess *embySession) string {
	tok := newToken()
	h.mu.Lock()
	h.sessions[tok] = sess
	h.userByID[sess.UserID] = sess
	h.mu.Unlock()
	return tok
}

// apiKey 从各种位置取客户端令牌（Emby 客户端位置五花八门）。
func apiKey(r *http.Request) string {
	if v := r.URL.Query().Get("api_key"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Emby-Token"); v != "" {
		return v
	}
	if v := r.Header.Get("X-MediaBrowser-Token"); v != "" {
		return v
	}
	// Authorization: Emby Client="x", Device="y", DeviceId="z", Version="1", Token="..."
	for _, hk := range []string{"Authorization", "X-Emby-Authorization"} {
		raw := r.Header.Get(hk)
		if raw == "" {
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) != 2 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(kv[0]), "Token") {
				return strings.Trim(strings.TrimSpace(kv[1]), `"`)
			}
		}
	}
	return ""
}

// ---- JSON 工具 ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		_, _ = w.Write([]byte("null"))
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// queryGet 大小写不敏感地取 query 参数（Emby 客户端传参大小写不统一）。
func queryGet(r *http.Request, key string) string {
	q := r.URL.Query()
	if v := q.Get(key); v != "" {
		return v
	}
	for k, vs := range q {
		if strings.EqualFold(k, key) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

// queryBool 解析粗粒度布尔参数。
func queryBool(r *http.Request, key string) bool {
	v := strings.ToLower(queryGet(r, key))
	return v == "true" || v == "1" || v == "yes"
}

// clientIP 取客户端 IP（用于日志与直链透传）。
func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return strings.TrimSpace(strings.Split(xf, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		return host[:i]
	}
	return host
}
