package emby

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/fn-media-proxy/internal/fn"
)

// 播放链路（Phase 0：纯 302 直链）
//
//	客户端 POST /Items/{id}/PlaybackInfo
//	  -> 我方 POST play/info 拿 media_guid
//	  -> 我方 POST stream 拿 direct_link_qualities[0].url（网盘/CDN 直链）
//	  -> 返回 MediaSource，Path/DirectStreamUrl 指向我方 /Videos/{id}/stream
//	客户端 GET /Videos/{id}/stream
//	  -> 我方实时重新取一次直链，返回 302 Location: <直链>
//	  -> 客户端直连网盘，NAS 零带宽
//
// 实测约束（docs/contract.md）：
//   - 光驱 CDN 不遵守 Range：能在客户端播放，但拖动进度可能受限（方案 C 的已知折价）；
//   - 拿不到真实文件大小，Size 不填（Emby 客户端对此容错）；
//   - 直链有时效，必须在播放前实时换取，不能长期缓存。

// playSessionID 生成随机播放会话 id（固定值会让部分客户端判定"会话已存在"而忽略点击）。
func playSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "fn-" + hex.EncodeToString(b)
}

// mediaSource 构造一条 MediaSourceInfo。
func (h *Handler) mediaSource(r *http.Request, id string, info *fn.PlayInfo, sr *fn.StreamResp) map[string]any {
	base := baseURL(r)

	dur := int64(0)
	if sr != nil && sr.VideoStream != nil && sr.VideoStream.Duration > 0 {
		dur = int64(sr.VideoStream.Duration)
	}
	runTicks := dur * 10_000_000

	container := "mkv"
	if sr != nil && sr.VideoStream != nil && sr.VideoStream.Wrapper != "" {
		container = strings.ToLower(sr.VideoStream.Wrapper)
	}

	directStreamURL := fmt.Sprintf("%s/Videos/%s/stream?Static=true&MediaSourceId=%s&api_key=%s",
		base, id, id, apiKey(r))

	src := map[string]any{
		"Id":                   id,
		"MediaSourceId":        id,
		"Name":                 "原画",
		"ServerId":             "fn-media-proxy",
		"Protocol":             "Http",
		"Type":                 "Default",
		"IsRemote":             true,
		"RequiresOpening":      false,
		"RequiresClosing":      false,
		"SupportsDirectPlay":   true,
		"SupportsDirectStream": true,
		"SupportsTranscoding":  false,
		"IsInfiniteStream":     false,
		"Container":            container,
		"RunTimeTicks":         runTicks,
		"ReadAtNativeFramerate": false,
		"IgnoreDts":            false,
		"IgnoreIndex":          false,
		"GenPtsInput":          false,
		"SupportsProbing":      false,
		"Path":                 directStreamURL,
		"DirectStreamUrl":      directStreamURL,
		"MediaStreams":         buildMediaStreams(sr),
		"MediaAttachments":     []any{},
		"Formats":              []string{},
		"Bitrate":              bitrateOf(sr),
		"RequiredHttpHeaders":  map[string]string{},
	}
	return src
}

// buildMediaStreams 把飞牛码流声明成 Emby MediaStream 列表。
//
// 关键（实测教训）：
//   - Codec 必须是单个值，逗号串会让客户端 DeviceProfile 匹配静默失败；
//   - 布尔字段全部给出，Hills 这类强类型客户端缺字段即崩。
func buildMediaStreams(sr *fn.StreamResp) []any {
	if sr == nil {
		return []any{}
	}
	streams := make([]any, 0, 4)
	idx := 0

	if v := sr.VideoStream; v != nil {
		streams = append(streams, map[string]any{
			"Type":                 "Video",
			"Index":                idx,
			"Codec":                strings.ToLower(v.CodecName),
			"Profile":              strOrNull(v.Profile),
			"Width":                v.Width,
			"Height":               v.Height,
			"BitRate":              v.Bps,
			"BitDepth":             v.BitDepth,
			"IsDefault":            true,
			"IsForced":             false,
			"IsInterlaced":         false,
			"AverageFrameRate":     parseFPS(v.FrameRate),
			"RealFrameRate":        parseFPS(v.FrameRate),
			"AspectRatio":          strOrNull(v.DisplayAspect),
			"VideoRange":           videoRange(v.ColorRangeType),
			"ColorSpace":           strOrNull(v.PixFmt),
			"ColorTransfer":        nil,
			"ColorPrimaries":       nil,
			"PixelFormat":          strOrNull(v.PixFmt),
			"DisplayTitle":         "4K HEVC" + hl(v.ColorRangeType),
			"Language":             "",
			"IsExternal":           false,
			"DeliveryMethod":       "Encode",
			"DeliveryUrl":          nil,
			"AttachmentSize":       0,
			"CodecTag":             nil,
			"Comment":              nil,
			"Flags":                nil,
			"Level":                parseLevel(v.Profile),
			"Channels":             0,
			"SampleRate":           0,
			"ChannelLayout":        nil,
		})
		idx++
	}

	for _, a := range sr.AudioStreams {
		title := a.Title
		streams = append(streams, map[string]any{
			"Type":           "Audio",
			"Index":          idx,
			"Codec":          strings.ToLower(a.CodecName),
			"Profile":        strOrNull(a.Profile),
			"Language":       a.Language,
			"Channels":       a.Channels,
			"ChannelLayout":  strOrNull(a.ChannelLayout),
			"SampleRate":     parseRate(a.SampleRate),
			"BitRate":        a.Bps,
			"BitDepth":       0,
			"IsDefault":      a.IsDefault != 0,
			"IsForced":       false,
			"IsExternal":     false,
			"DisplayTitle":   title,
			"Title":          title,
			"DeliveryMethod": "Encode",
			"DeliveryUrl":    nil,
			"AspectRatio":    nil,
			"AverageFrameRate": 0,
			"RealFrameRate":  0,
			"IsInterlaced":   false,
			"VideoRange":     nil,
			"ColorSpace":     nil,
		})
		idx++
	}

	for _, s := range sr.SubtitleStreams {
		streams = append(streams, map[string]any{
			"Type":           "Subtitle",
			"Index":          idx,
			"Codec":          subtitleCodec(s.CodecName),
			"Language":       s.Language,
			"IsDefault":      s.IsDefault != 0,
			"IsForced":       false,
			"IsExternal":     s.IsExternal != 0,
			"DisplayTitle":   s.Title,
			"Title":          s.Title,
			"DeliveryMethod": "Embed",
			"DeliveryUrl":    nil,
			"IsHearingImpaired": false,
			"IsTextSubtitleStream": s.IsBitmap == 0,
			"SupportsExternalStream": false,
			"Width": 0, "Height": 0, "Channels": 0, "SampleRate": 0,
			"BitRate": 0, "BitDepth": 0, "ChannelLayout": nil,
			"AspectRatio": nil, "AverageFrameRate": 0, "RealFrameRate": 0,
			"IsInterlaced": false, "VideoRange": nil, "ColorSpace": nil,
		})
		idx++
	}
	return streams
}

func bitrateOf(sr *fn.StreamResp) int64 {
	if sr == nil || sr.VideoStream == nil {
		return 0
	}
	return sr.VideoStream.Bps
}

func videoRange(t string) string {
	if strings.EqualFold(t, "HDR") {
		return "HDR"
	}
	return "SDR"
}

func hl(t string) string {
	if strings.EqualFold(t, "HDR") {
		return " HDR"
	}
	return ""
}

// parseFPS 解析 "25 fps" / "23.98 fps" / "30/1" 这类帧率串。
func parseFPS(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "fps"))
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "/"); i > 0 {
		a, err1 := strconv.ParseFloat(s[:i], 64)
		b, err2 := strconv.ParseFloat(s[i+1:], 64)
		if err1 == nil && err2 == nil && b != 0 {
			return round2(a / b)
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return round2(v)
	}
	return 0
}

func parseRate(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return v
}

// parseLevel 从飞牛 profile 猜一个 H.264 level 值（客户端只用来展示）。
func parseLevel(profile string) int {
	if strings.Contains(strings.ToLower(profile), "main 10") {
		return 150
	}
	return 0
}

func subtitleCodec(c string) string {
	switch strings.ToLower(c) {
	case "subrip", "srt":
		return "srt"
	case "ass", "ssa":
		return "ass"
	case "pgssub", "pgs":
		return "pgssub"
	case "vobsub":
		return "dvdsub"
	}
	return strings.ToLower(c)
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// baseURL 推断客户端可达的本服务地址（尊重反代头）。
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.Split(p, ",")[0]
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = strings.Split(fh, ",")[0]
	}
	return scheme + "://" + strings.TrimSpace(host)
}

// handlePlaybackInfo 实现 POST/GET /Items/{id}/PlaybackInfo —— 播放入口。
func (h *Handler) handlePlaybackInfo(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()

	info, err := sess.Client.PlayInfo(ctx, id)
	if err != nil {
		h.fail(w, "play/info", err)
		return
	}
	mediaGuid := info.MediaGuid
	if mediaGuid == "" {
		mediaGuid = info.Guid
	}

	sr, err := sess.Client.Stream(ctx, mediaGuid, "")
	if err != nil {
		// 流信息拿不到也要给出可播入口（客户端仍可走 stream 端点重试）
		h.logf("playbackinfo: stream %s failed: %v", mediaGuid, err)
		sr = nil
	}

	src := h.mediaSource(r, id, info, sr)

	writeJSON(w, http.StatusOK, map[string]any{
		"PlaySessionId": playSessionID(),
		"MediaSources":  []any{src},
		"ErrorCode":     nil,
	})
}

// handleStream 把客户端重定向到飞牛给出的网盘/CDN 直链（纯 302，NAS 零带宽）。
func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()

	info, err := sess.Client.PlayInfo(ctx, id)
	if err != nil {
		h.fail(w, "play/info", err)
		return
	}
	mediaGuid := info.MediaGuid
	if mediaGuid == "" {
		mediaGuid = info.Guid
	}
	// 客户端可能用 MediaSourceId 指定版本
	if v := queryGet(r, "MediaSourceId"); v != "" && v != id {
		mediaGuid = v
	}

	sr, err := sess.Client.Stream(ctx, mediaGuid, "")
	if err != nil {
		h.fail(w, "stream", err)
		return
	}

	direct := pickDirectLink(sr)
	if direct == "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"Error": "no direct link available"})
		return
	}

	h.logf("302 stream: %s -> %s", id, shortURL(direct))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, direct, http.StatusFound)
}

// pickDirectLink 从 stream 响应挑直链（优先原画）。
func pickDirectLink(sr *fn.StreamResp) string {
	if sr == nil {
		return ""
	}
	for _, q := range sr.DirectLinkQualities {
		if q.URL != "" {
			return q.URL
		}
	}
	return ""
}

// handleVideoSubresource 处理 /Videos/{id}/{other}：
// master.m3u8 / Subtitles / live 等我们不支持的子资源，统一 302 到直链或 404。
func (h *Handler) handleVideoSubresource(w http.ResponseWriter, r *http.Request) {
	rest := r.PathValue("rest")
	switch {
	case strings.HasSuffix(rest, ".m3u8") || rest == "master.m3u8":
		// 不做转码，客户端若请求 HLS 直接拒绝（明确报错比静默失败好排查）
		writeJSON(w, http.StatusNotImplemented, map[string]any{"Error": "HLS/transcoding not supported"})
	case strings.Contains(rest, "Subtitles"):
		writeJSON(w, http.StatusNotFound, map[string]any{"Error": "subtitle not available"})
	case strings.HasPrefix(rest, "stream"):
		// /Videos/{id}/stream.mp4、stream.mkv 等：当做直链请求处理
		h.handleStream(w, r)
	default:
		h.handleStream(w, r)
	}
}

func shortURL(u string) string {
	if len(u) > 80 {
		return u[:80] + "..."
	}
	return u
}
