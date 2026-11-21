// Package fn 封装飞牛影视（fnOS media）私有接口。
//
// 契约来源：真机实测（192.168.100.10:8005，2026-11），详见 docs/contract.md。
// 关键点：
//   - 所有 /v/api/v1 请求必须带 Authx 签名头；
//   - 必须带 Cookie（无 token 时任意值，否则反代把 API 兜底成 SPA 的 HTML）；
//   - 登录是 v2/loginByPassword，密码必须是 SHA256 hex。
package fn

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Result 是飞牛 /v/api/v1 所有接口的统一响应包：{"code":0,"msg":"","data":...}
type Result struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// ErrInvalidSign 飞牛返回的签名失效错误码。
const codeInvalidSign = 5000

// APIError 是飞牛接口返回的业务错误。
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("fn api error: code=%d msg=%s", e.Code, e.Msg)
}

// IsAuthFailure 判断错误是否为 token 失效（各版本码值不统一，做宽匹配）。
func IsAuthFailure(err error) bool {
	ae, ok := err.(*APIError)
	if !ok {
		return false
	}
	if ae.Code == 401 {
		return true
	}
	m := strings.ToLower(ae.Msg)
	return strings.Contains(m, "token") || strings.Contains(m, "登录") ||
		strings.Contains(m, "权限") || strings.Contains(m, "auth")
}

// Client 是飞牛接口的薄客户端，持有 base URL、token 与 HTTP 传输。
type Client struct {
	BaseURL string
	Token   string
	UserAgent string
	http    *http.Client

	mu sync.RWMutex
}

// NewClient 构造客户端。baseURL 形如 http://192.168.100.10:8005。
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		UserAgent: AppName,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// 不跟随重定向：stream/direct link 需要客户端自己看到 302
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// SetToken 设置或刷新 token。
func (c *Client) SetToken(t string) {
	c.mu.Lock()
	c.Token = t
	c.mu.Unlock()
}

// GetToken 读取当前 token。
func (c *Client) GetToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Token
}

// Clone 派生一个共享传输、独立 token 的客户端（多用户会话用）。
func (c *Client) Clone() *Client {
	return &Client{BaseURL: c.BaseURL, UserAgent: c.UserAgent, http: c.http}
}

// ---- 哈希与签名 ----

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// SHA256Hex 求 SHA256 hex（登录密码哈希、明文/已哈希判定都用它）。
func SHA256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// IsSHA256Hex 判断是否 64 位十六进制。
func IsSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return false
		}
	}
	return true
}

// NormalizePassword 把客户端传来的密码归一为 SHA256 hex：
// 已是 64 位 hex 则原样返回，否则哈希。
func NormalizePassword(p string) string {
	if IsSHA256Hex(p) {
		return p
	}
	return SHA256Hex(p)
}

// authx 构造 Authx 头。path 不含 query；body 为空串时 bodyHash = MD5("")。
// nonce 必须 6 位且不能以 0 开头（实测前导零返回 5000 invalid sign）。
func authx(path, body string) string {
	nonce := fmt.Sprintf("%06d", rand.Intn(900000)+100000)
	ts := time.Now().UnixMilli()
	bodyHash := md5Hex(body)
	sign := md5Hex(APIKey + "_" + path + "_" + nonce + "_" + fmt.Sprint(ts) + "_" + bodyHash + "_" + APISecret)
	return fmt.Sprintf("nonce=%s&timestamp=%d&sign=%s", nonce, ts, sign)
}

// ---- 底层请求 ----

// do 发起一次带签名的请求并解析 Result。
func (c *Client) do(ctx context.Context, method, fullPath string, body any) (*Result, error) {
	signPath := fullPath
	if i := strings.IndexByte(fullPath, '?'); i >= 0 {
		signPath = fullPath[:i]
	}

	var raw string
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		raw = string(b)
	}

	var reader io.Reader
	if raw != "" {
		reader = strings.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+fullPath, reader)
	if err != nil {
		return nil, err
	}
	if raw != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Authx", authx(signPath, raw))

	token := c.GetToken()
	if token != "" {
		req.Header.Set("Authorization", token)
		// 图片/媒体等端点认 Cookie
		req.Header.Set("Cookie", "mode=relay; "+"token="+token)
	} else {
		// 无 token 也必须带 Cookie，否则反代兜底成 SPA 的 HTML
		req.Header.Set("Cookie", "mode=relay")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, fullPath, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode result (status=%d): %s", resp.StatusCode, truncate(string(data), 200))
	}
	if res.Code != 0 {
		return &res, &APIError{Code: res.Code, Msg: res.Msg}
	}
	return &res, nil
}

// v1 调 /v/api/v1/{path}。
func (c *Client) v1(ctx context.Context, method, path string, body any) (*Result, error) {
	return c.do(ctx, method, PathPrefix+path, body)
}

// doRaw 返回原始响应（媒体字节流、图片用）。
func (c *Client) doRaw(ctx context.Context, method, fullPath string, body []byte, extraHeaders map[string]string) (*http.Response, error) {
	signPath := fullPath
	if i := strings.IndexByte(fullPath, '?'); i >= 0 {
		signPath = fullPath[:i]
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+fullPath, reader)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Authx", authx(signPath, string(body)))
	token := c.GetToken()
	if token != "" {
		req.Header.Set("Authorization", token)
		req.Header.Set("Cookie", "mode=relay; token="+token)
	} else {
		req.Header.Set("Cookie", "mode=relay")
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	return c.http.Do(req)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
