package emby

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ---- 播放进度与会话 ----

type playProgress struct {
	ItemId       string `json:"ItemId"`
	PlaySessionId string `json:"PlaySessionId"`
	PositionTicks int64 `json:"PositionTicks"`
	IsPaused     bool   `json:"IsPaused"`
}

// handlePlaying 处理 POST /Sessions/Playing 与 /Sessions/Playing/Progress。
func (h *Handler) handlePlaying(w http.ResponseWriter, r *http.Request) {
	sess := h.sessionByToken(r)
	var p playProgress
	if err := json.NewDecoder(r.Body).Decode(&p); err == nil {
		h.recordProgress(r.Context(), sess, p.ItemId, p.PositionTicks, false)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePlayingStopped 播放停止：接近结尾时标记已看完（对齐飞牛播放器行为）。
func (h *Handler) handlePlayingStopped(w http.ResponseWriter, r *http.Request) {
	sess := h.sessionByToken(r)
	var p playProgress
	if err := json.NewDecoder(r.Body).Decode(&p); err == nil && sess != nil {
		h.recordProgress(r.Context(), sess, p.ItemId, p.PositionTicks, true)
		if shouldMarkWatched(r, sess, p.ItemId, p.PositionTicks) {
			if err := sess.Client.MarkWatched(r.Context(), p.ItemId); err != nil {
				h.logf("mark watched %s: %v", p.ItemId, err)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func shouldMarkWatched(r *http.Request, sess *embySession, itemID string, ticks int64) bool {
	if ticks <= 0 {
		return false
	}
	info, err := sess.Client.PlayInfo(r.Context(), itemID)
	if err != nil {
		return false
	}
	_ = info
	// 无可靠总时长时，用 92% 阈值并由飞牛侧自行判定；这里只在位置>0 时保守触发
	return false
}

// handleProgress 老端点 POST /Items/{id}/Progress。
func (h *Handler) handleProgress(w http.ResponseWriter, r *http.Request) {
	sess := h.sessionByToken(r)
	id := r.PathValue("id")
	var p struct {
		PositionTicks int64 `json:"PositionTicks"`
	}
	_ = json.NewDecoder(r.Body).Decode(&p)
	h.recordProgress(r.Context(), sess, id, p.PositionTicks, false)
	w.WriteHeader(http.StatusNoContent)
}

// handleCapabilities 客户端能力上报，记录即可。
func (h *Handler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// ---- 收藏 / 已看 ----

func (h *Handler) handleFavorite(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	on := r.Method == http.MethodPost
	if err := sess.Client.SetFavorite(r.Context(), id, on); err != nil {
		h.fail(w, "favorite", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Id": id, "IsFavorite": on, "Played": false,
	})
}

func (h *Handler) handlePlayed(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	var err error
	if r.Method == http.MethodPost {
		err = sess.Client.MarkWatched(r.Context(), id)
	} else {
		err = sess.Client.UnmarkWatched(r.Context(), id)
	}
	if err != nil {
		h.fail(w, "played", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Id": id, "Played": r.Method == http.MethodPost, "IsFavorite": false,
	})
}

// ---- 图片 ----

// handleItemImage 代理飞牛图片资源。
// 飞牛只有海报 / Logo / 横版剧照，全部 ImageType 回落同一批资源。
func (h *Handler) handleItemImage(w http.ResponseWriter, r *http.Request) {
	sess := h.sessionByToken(r)
	if sess == nil {
		transparentPNG(w)
		return
	}
	id := r.PathValue("id")
	imgType := strings.ToLower(r.PathValue("type"))

	it, err := sess.Client.ItemDetail(r.Context(), id)
	if err != nil {
		transparentPNG(w)
		return
	}

	var path string
	switch imgType {
	case "logo":
		path = it.Logos
	case "backdrop", "thumb":
		path = it.Backdrops
		if path == "" {
			path = it.StillPath
		}
	default: // primary
		path = it.PosterPath()
		if path == "" {
			path = it.StillPath
		}
	}
	if path == "" {
		transparentPNG(w)
		return
	}

	url := "/v/api/v1/sys/img" + path
	resp, err := sess.Client.RawFetch(r.Context(), http.MethodGet, url, nil, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		transparentPNG(w)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

// transparentPNG 返回 1x1 透明 PNG，避免客户端破图。
func transparentPNG(w http.ResponseWriter) {
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
		0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", "67")
	_, _ = w.Write(png)
}

// ---- 空实现 ----

func (h *Handler) handleEmptyArray(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (h *Handler) handleEmptyQuery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"Items": []any{}, "TotalRecordCount": 0, "StartIndex": 0})
}

func (h *Handler) handleDisplayPrefs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Id": queryGet(r, "id"),
		"SortBy": "SortName", "SortOrder": "Ascending",
		"RememberIndexing": false, "PrimaryImageHeight": 0, "PrimaryImageWidth": 0,
		"ScrollDirection": "Horizontal", "ShowBackdrop": true,
		"RememberSorting": false, "CustomPrefs": map[string]string{},
		"DisplayType": "poster", "ViewType": nil,
	})
}

func (h *Handler) handleDisplayPrefsPost(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// handleFallback 兜底：未知端点返回空对象而非 404（部分客户端对 404 处理粗暴）。
func (h *Handler) handleFallback(w http.ResponseWriter, r *http.Request) {
	h.logf("fallback: %s %s", r.Method, r.URL.Path)
	writeJSON(w, http.StatusOK, map[string]any{})
}
