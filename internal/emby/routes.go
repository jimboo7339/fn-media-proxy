package emby

import (
	"net/http"
	"strings"
)

// Routes 注册全部路由并返回带中间件的 handler。
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// 探测与握手
	mux.HandleFunc("GET /{$}", h.handleRoot)
	mux.HandleFunc("GET /System/Info", h.handleSystemInfo)
	mux.HandleFunc("GET /System/Info/Public", h.handleSystemInfoPublic)
	mux.HandleFunc("GET /System/Configuration", h.handleSystemConfiguration)
	mux.HandleFunc("GET /System/Configuration/{section}", h.handleSystemConfiguration)
	mux.HandleFunc("GET /Branding/Configuration", h.handleBranding)
	mux.HandleFunc("GET /Branding/Splashscreen", h.handleEmptyObject)

	// 登录与用户
	mux.HandleFunc("POST /Users/AuthenticateByName", h.handleAuthenticate)
	mux.HandleFunc("GET /Users/Me", h.handleUsersMe)
	mux.HandleFunc("GET /Users/{uid}", h.handleUserByID)
	mux.HandleFunc("GET /Users/Public", h.handleUsersPublic)

	// 浏览
	mux.HandleFunc("GET /Users/{uid}/Views", h.handleViews)
	mux.HandleFunc("GET /Users/{uid}/Items", h.handleItems)
	mux.HandleFunc("GET /Users/{uid}/Items/Resume", h.handleResume)
	mux.HandleFunc("GET /Users/{uid}/Items/Latest", h.handleLatest)
	mux.HandleFunc("GET /Items", h.handleItems)
	mux.HandleFunc("GET /Items/{id}", h.handleItemByID)
	mux.HandleFunc("GET /Users/{uid}/Items/{id}", h.handleItemByID)
	mux.HandleFunc("GET /Items/Resume", h.handleResume)
	mux.HandleFunc("GET /Shows/{id}/Seasons", h.handleSeasons)
	mux.HandleFunc("GET /Shows/{id}/Episodes", h.handleEpisodes)
	mux.HandleFunc("GET /Shows/NextUp", h.handleNextUp)
	mux.HandleFunc("GET /Shows/{id}/NextUp", h.handleNextUp)

	// 播放
	mux.HandleFunc("POST /Items/{id}/PlaybackInfo", h.handlePlaybackInfo)
	mux.HandleFunc("GET /Items/{id}/PlaybackInfo", h.handlePlaybackInfo)
	mux.HandleFunc("POST /Videos/{id}/PlaybackInfo", h.handlePlaybackInfo)
	mux.HandleFunc("GET /Videos/{id}/stream", h.handleStream)
	mux.HandleFunc("HEAD /Videos/{id}/stream", h.handleStream)
	mux.HandleFunc("GET /Videos/{id}/{rest}", h.handleVideoSubresource)

	// 进度与会话
	mux.HandleFunc("POST /Sessions/Playing", h.handlePlaying)
	mux.HandleFunc("POST /Sessions/Playing/Progress", h.handlePlaying)
	mux.HandleFunc("POST /Sessions/Playing/Stopped", h.handlePlayingStopped)
	mux.HandleFunc("POST /Items/{id}/Progress", h.handleProgress)
	mux.HandleFunc("POST /Sessions/Capabilities/Full", h.handleCapabilities)
	mux.HandleFunc("POST /Sessions/Capabilities", h.handleCapabilities)
	mux.HandleFunc("GET /Sessions", h.handleEmptyArray)

	// 收藏 / 已看
	mux.HandleFunc("POST /Users/{uid}/FavoriteItems/{id}", h.handleFavorite)
	mux.HandleFunc("DELETE /Users/{uid}/FavoriteItems/{id}", h.handleFavorite)
	mux.HandleFunc("POST /Users/{uid}/PlayedItems/{id}", h.handlePlayed)
	mux.HandleFunc("DELETE /Users/{uid}/PlayedItems/{id}", h.handlePlayed)
	mux.HandleFunc("POST /Users/{uid}/PlayingItems/{id}", h.handlePlaying)
	mux.HandleFunc("DELETE /Users/{uid}/PlayingItems/{id}", h.handlePlayingStopped)

	// 图片
	mux.HandleFunc("GET /Items/{id}/Images/{type}", h.handleItemImage)
	mux.HandleFunc("GET /Items/{id}/Images/{type}/{index}", h.handleItemImage)
	mux.HandleFunc("HEAD /Items/{id}/Images/{type}", h.handleItemImage)

	// 空实现（客户端初始化会拉，缺了会报错）
	mux.HandleFunc("GET /Users/{uid}/DisplayPreferences/{id}", h.handleDisplayPrefs)
	mux.HandleFunc("POST /Users/{uid}/DisplayPreferences/{id}", h.handleDisplayPrefsPost)
	mux.HandleFunc("GET /DisplayPreferences/{id}", h.handleDisplayPrefs)
	mux.HandleFunc("POST /DisplayPreferences/{id}", h.handleDisplayPrefsPost)
	mux.HandleFunc("GET /Genres", h.handleEmptyQuery)
	mux.HandleFunc("GET /Persons", h.handleEmptyQuery)
	mux.HandleFunc("GET /Studios", h.handleEmptyQuery)
	mux.HandleFunc("GET /Tags", h.handleEmptyQuery)
	mux.HandleFunc("GET /Years", h.handleEmptyQuery)
	mux.HandleFunc("GET /ScheduledTasks", h.handleEmptyArray)
	mux.HandleFunc("GET /Search/Hints", h.handleEmptyQuery)
	mux.HandleFunc("GET /Activity/Log", h.handleEmptyQuery)
	mux.HandleFunc("GET /Plugins", h.handleEmptyArray)
	mux.HandleFunc("GET /ItemLookup/Search", h.handleEmptyQuery)

	// 兜底
	mux.HandleFunc("/", h.handleFallback)

	return h.middleware(mux)
}

// middleware 组合：/emby 前缀剥离 + 路径大小写归一 + 访问日志。
func (h *Handler) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1) 剥掉 /emby 前缀（官方客户端带前缀，且可能重复 /emby/emby）
		p := r.URL.Path
		for p == "/emby" || strings.HasPrefix(p, "/emby/") {
			p = strings.TrimPrefix(p, "/emby")
			if p == "" {
				p = "/"
			}
		}
		// 2) 折叠重复斜杠：okhttp 遇到 301 会把 POST 降级成 GET
		for strings.Contains(p, "//") {
			p = strings.ReplaceAll(p, "//", "/")
		}
		// 3) 已知段大小写归一（Go mux 大小写敏感，Emby 服务端不敏感）
		p = normalizePathCase(p)
		if p != r.URL.Path {
			r2 := r.Clone(r.Context())
			r2.URL.Path = p
			r = r2
		}
		h.logf("%s %s %s", r.Method, r.URL.Path, clientIP(r))
		next.ServeHTTP(w, r)
	})
}

// canonicalSegments 已知路由静态段：小写 -> 规范大小写。
var canonicalSegments = map[string]string{
	"system": "System", "info": "Info", "public": "Public", "configuration": "Configuration",
	"branding": "Branding", "splashscreen": "Splashscreen",
	"users": "Users", "me": "Me", "authenticatebyname": "AuthenticateByName",
	"views": "Views", "items": "Items", "resume": "Resume", "latest": "Latest",
	"shows": "Shows", "seasons": "Seasons", "episodes": "Episodes", "nextup": "NextUp",
	"playbackinfo": "PlaybackInfo", "videos": "Videos", "stream": "Stream",
	"sessions": "Sessions", "playing": "Playing", "progress": "Progress", "stopped": "Stopped",
	"capabilities": "Capabilities", "full": "Full",
	"favoriteitems": "FavoriteItems", "playeditems": "PlayedItems", "playingitems": "PlayingItems",
	"images": "Images", "displaypreferences": "DisplayPreferences",
	"genres": "Genres", "persons": "Persons", "studios": "Studios", "tags": "Tags", "years": "Years",
	"scheduledtasks": "ScheduledTasks", "search": "Search", "hints": "Hints",
	"activity": "Activity", "log": "Log", "plugins": "Plugins",
}

func normalizePathCase(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if seg == "" {
			continue
		}
		if canon, ok := canonicalSegments[strings.ToLower(seg)]; ok {
			parts[i] = canon
		}
	}
	return strings.Join(parts, "/")
}
