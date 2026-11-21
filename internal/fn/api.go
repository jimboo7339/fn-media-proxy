package fn

// 本文件是飞牛接口的响应类型定义与调用方法。
// 字段来源：真机实测，见 docs/contract.md。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ---- 类型定义 ----

// MediaDB 对应 mediadb/list 的库条目。
type MediaDB struct {
	Guid     string   `json:"guid"`
	Title    string   `json:"title"`
	Category string   `json:"category"` // TV / Movie / Other / Live
	ViewType int      `json:"view_type"`
	Poster   string   `json:"poster"`
	Posters  []string `json:"posters"` // 库级是数组
}

// MediaItem 对应 item/list、item/{guid}、season/list、episode/list 的条目。
type MediaItem struct {
	Guid      string `json:"guid"`
	ParentGuid string `json:"parent_guid"`
	AncestorGuid string `json:"ancestor_guid"`
	Title     string `json:"title"`
	OriginalTitle string `json:"original_title"`
	TvTitle   string `json:"tv_title"`
	ParentTitle string `json:"parent_title"`

	Type string `json:"type"` // TV / Season / Episode / Movie / Video / Directory

	// 图片资源（item/{guid} 是字符串，item/list 的 poster 是字符串）
	Poster   string   `json:"poster"`
	Posters  string   `json:"posters"`
	StillPath string  `json:"still_path"` // 集级剧照
	Logos    string   `json:"logos"`
	Backdrops string  `json:"backdrops"`

	PosterWidth  int `json:"poster_width"`
	PosterHeight int `json:"poster_height"`

	// 时长：单集用 duration（秒），条目常用 runtime（分钟）
	Runtime  int `json:"runtime"`
	Duration int `json:"duration"`

	IsFavorite int   `json:"is_favorite"`
	Watched    int   `json:"watched"`
	IsWatched  int   `json:"is_watched"`
	WatchedTs  int64 `json:"watched_ts"`
	Ts         int64 `json:"ts"` // 续播位置（秒）

	VoteAverage string `json:"vote_average"`
	Status      string `json:"status"` // "Returning Series" / "Ended" / "1"

	SeasonNumber     int `json:"season_number"`
	EpisodeNumber    int `json:"episode_number"`
	NumberOfSeasons  int `json:"number_of_seasons"`
	NumberOfEpisodes int `json:"number_of_episodes"`

	LocalNumberOfEpisodes int `json:"local_number_of_episodes"`

	AirDate     string `json:"air_date"`
	ReleaseDate string `json:"release_date"`
	Overview    string `json:"overview"`

	CanPlay  int    `json:"can_play"`
	PlayError string `json:"play_error"`

	Genres              []int    `json:"genres"` // 数字 id
	ProductionCountries []string `json:"production_countries"`

	TrimID string `json:"trim_id"` // TMDB id
	ImdbID string `json:"imdb_id"`
}

// PosterPath 取海报路径（poster 优先，回退 posters）。
func (m MediaItem) PosterPath() string {
	if m.Poster != "" {
		return m.Poster
	}
	return m.Posters
}

// PlayInfo 对应 POST play/info 的响应：把 item 解析为可播放的版本。
type PlayInfo struct {
	Guid         string `json:"guid"`
	ParentGuid   string `json:"parent_guid"`
	GrandGuid    string `json:"grand_guid"`
	Type         string `json:"type"` // Episode / Movie / Video
	MediaGuid    string `json:"media_guid"` // ★ 真正的可播版本 id
	VideoGuid    string `json:"video_guid"`
	AudioGuid    string `json:"audio_guid"`
	SubtitleGuid string `json:"subtitle_guid"`
	Ts           int64  `json:"ts"`
	Item         struct {
		Guid        string `json:"guid"`
		Title       string `json:"title"`
		TvTitle     string `json:"tv_title"`
		ParentTitle string `json:"parent_title"`
		ParentGuid  string `json:"parent_guid"`
		AncestorGuid string `json:"ancestor_guid"`
		TrimID      string `json:"trim_id"`
		VoteAverage string `json:"vote_average"`
		ReleaseDate string `json:"release_date"`
		StillPath   string `json:"still_path"`
		AirDate     string `json:"air_date"`
		SeasonNumber int `json:"season_number"`
		EpisodeNumber int `json:"episode_number"`
		NumberOfEpisodes int `json:"number_of_episodes"`
		CanPlay     int    `json:"can_play"`
		PlayError   string `json:"play_error"`
		Type        string `json:"type"`
	} `json:"item"`
}

// StreamResp 对应 POST stream 的响应。字段按真机实测命名。
type StreamResp struct {
	FileStream struct {
		Guid       string `json:"guid"`
		Path       string `json:"path"` // 真实文件路径（STRM 时是 .strm 文件）
		FileName   string `json:"file_name"`
		Size       int64  `json:"size"`
		CanPlay    int    `json:"can_play"`
		CreateTime int64  `json:"create_time"`
	} `json:"file_stream"`

	VideoStream *VideoStream `json:"video_stream"`
	AudioStreams []AudioStream `json:"audio_streams"`
	SubtitleStreams []SubtitleStream `json:"subtitle_streams"`

	// 云盘 / STRM 信息
	CloudStorageInfo *struct {
		CloudStorageType int    `json:"cloud_storage_type"` // 9001 = STRM
		CloudNickName    string `json:"cloud_nick_name"`
		Valid            bool   `json:"valid"`
		Disabled         bool   `json:"disabled"`
		IsVip            bool   `json:"is_vip"`
	} `json:"cloud_storage_info"`

	// Header 请求直链时要带的头（User-Agent 必须沿用）
	Header struct {
		Cookie    []string `json:"Cookie"`
		UserAgent []string `json:"User-Agent"`
	} `json:"header"`

	// DirectLinkQualities 直链（真机实测：STRM 片源给出「原画」一条）
	DirectLinkQualities []DirectLinkQuality `json:"direct_link_qualities"`

	// Qualities 可选清晰度列表（含码率）
	Qualities []Quality `json:"qualities"`
}

// VideoStream 视频流信息。
type VideoStream struct {
	MediaGuid      string `json:"media_guid"`
	Guid           string `json:"guid"`
	Title          string `json:"title"`
	ResolutionType string `json:"resolution_type"` // 4k / 1080 / ...
	CodecName      string `json:"codec_name"`       // hevc / h264 ...
	CodecType      string `json:"codec_type"`
	Profile        string `json:"profile"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	Duration       int    `json:"duration"`
	Bps            int64  `json:"bps"`
	BitDepth       int    `json:"bit_depth"`
	PixFmt         string `json:"pix_fmt"`
	FrameRate      string `json:"r_frame_rate"` // "25 fps"
	DisplayAspect  string `json:"display_aspect_ratio"`
	ColorRangeType string `json:"color_range_type"` // SDR / HDR
	Index          int    `json:"index"`
	Wrapper        string `json:"wrapper"` // MKV
}

// AudioStream 音频流信息。
type AudioStream struct {
	MediaGuid     string `json:"media_guid"`
	Guid          string `json:"guid"`
	Title         string `json:"title"`
	CodecName     string `json:"codec_name"`
	CodecType     string `json:"codec_type"`
	Language      string `json:"language"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout"`
	Profile       string `json:"profile"`
	SampleRate    string `json:"sample_rate"`
	Bps           int64  `json:"bps"`
	Index         int    `json:"index"`
	IsDefault     int    `json:"is_default"`
	Duration      int    `json:"duration"`
	AudioType     string `json:"audio_type"` // Stereo / DolbyAtmos ...
}

// SubtitleStream 字幕轨。
type SubtitleStream struct {
	MediaGuid  string `json:"media_guid"`
	Guid       string `json:"guid"`
	Title      string `json:"title"`
	CodecName  string `json:"codec_name"`
	Language   string `json:"language"`
	Index      int    `json:"index"`
	IsDefault  int    `json:"is_default"`
	IsExternal int    `json:"is_external"`
	IsBitmap   int    `json:"is_bitmap"`
}

// DirectLinkQuality 直链条目。
type DirectLinkQuality struct {
	Bitrate    int    `json:"bitrate"`
	Resolution string `json:"resolution"`
	Progressive bool   `json:"progressive"`
	URL        string `json:"url"`
	IsM3u8     bool   `json:"is_m3u8"`
}

// Quality 可选清晰度条目。
type Quality struct {
	Bitrate     int    `json:"bitrate"`
	Resolution  string `json:"resolution"`
	Progressive bool   `json:"progressive"`
	IsM3u8      bool   `json:"is_m3u8"`
}

// Person 演职员。
type Person struct {
	PersonGuid   string `json:"person_guid"`
	Role         string `json:"role"`
	Job          string `json:"job"`
	Order        int    `json:"order"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	Biography    string `json:"biography"`
	ProfilePath  string `json:"profile_path"`
	Gender       int    `json:"gender"`
}

// ---- 调用方法 ----

// Login 登录并设置 token。username/password 中 password 由调用方归一（见 NormalizePassword）。
func (c *Client) Login(ctx context.Context, username, passwordHash string) (string, error) {
	res, err := c.do(ctx, "POST", LoginPath, map[string]any{
		"app_name": AppName,
		"username": username,
		"password": passwordHash,
	})
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return "", fmt.Errorf("login decode: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("login: empty token (check credentials)")
	}
	c.SetToken(out.Token)
	return out.Token, nil
}

// MediaDBList 列出全部媒体库。
func (c *Client) MediaDBList(ctx context.Context) ([]MediaDB, error) {
	res, err := c.v1(ctx, "GET", "mediadb/list", nil)
	if err != nil {
		return nil, err
	}
	var out []MediaDB
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("mediadb/list decode: %w", err)
	}
	return out, nil
}

// ItemPage 是 item/list 的分页响应。
type ItemPage struct {
	List  []MediaItem `json:"list"`
	Total int         `json:"total"`
}

// ItemListPage 分页列出某父节点下的条目。
// mode: "ancestor_guid"（库/剧/季 guid）或 "parent_guid"（剧/季子级）。
// 实测：库 guid 只认 ancestor，剧/季 guid 只认 parent，两者互斥。
func (c *Client) ItemListPage(ctx context.Context, mode, guid string, page, pageSize int) (*ItemPage, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	body := map[string]any{
		"tags":                  map[string]any{"type": []string{"Movie", "TV", "Directory", "Video", "Season", "Episode"}},
		"sort_type":             "DESC",
		"sort_column":           "create_time",
		"exclude_grouped_video": 1,
		"page":                  page,
		"page_size":             pageSize,
		mode:                    guid,
	}
	res, err := c.v1(ctx, "POST", "item/list", body)
	if err != nil {
		return nil, err
	}
	var out ItemPage
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("item/list decode: %w", err)
	}
	return &out, nil
}

// ItemDetail 取单条目详情。
func (c *Client) ItemDetail(ctx context.Context, guid string) (*MediaItem, error) {
	res, err := c.v1(ctx, "GET", "item/"+guid, nil)
	if err != nil {
		return nil, err
	}
	var out MediaItem
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("item decode: %w", err)
	}
	return &out, nil
}

// SeasonList 列剧的季。
func (c *Client) SeasonList(ctx context.Context, tvGuid string) ([]MediaItem, error) {
	res, err := c.v1(ctx, "GET", "season/list/"+tvGuid, nil)
	if err != nil {
		return nil, err
	}
	var out []MediaItem
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("season/list decode: %w", err)
	}
	return out, nil
}

// EpisodeList 取某季下全部集。
// 实测：episode/list/{季guid} 有效；对剧 guid 直接调用返回 null，必须先走 SeasonList。
func (c *Client) EpisodeList(ctx context.Context, seasonGuid string) ([]MediaItem, error) {
	res, err := c.v1(ctx, "GET", "episode/list/"+seasonGuid+"?guid="+seasonGuid, nil)
	if err != nil {
		return nil, err
	}
	var out []MediaItem
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("episode/list decode: %w", err)
	}
	return out, nil
}

// SeriesEpisodes 取剧下全部集（自动逐季展开并按季号/集号排序）。
func (c *Client) SeriesEpisodes(ctx context.Context, tvGuid string) ([]MediaItem, error) {
	seasons, err := c.SeasonList(ctx, tvGuid)
	if err != nil {
		return nil, err
	}
	var all []MediaItem
	for _, s := range seasons {
		if s.Type != "Season" {
			continue
		}
		eps, err := c.EpisodeList(ctx, s.Guid)
		if err != nil {
			continue
		}
		for i := range eps {
			// 集条目可能缺 parent_guid，补齐以便客户端正确归属
			if eps[i].ParentGuid == "" {
				eps[i].ParentGuid = s.Guid
			}
			if eps[i].SeasonNumber == 0 {
				eps[i].SeasonNumber = s.SeasonNumber
			}
			all = append(all, eps[i])
		}
	}
	sortEpisodes(all)
	return all, nil
}

// sortEpisodes 按季号、集号升序。
func sortEpisodes(items []MediaItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			a, b := items[j-1], items[j]
			if a.SeasonNumber > b.SeasonNumber ||
				(a.SeasonNumber == b.SeasonNumber && a.EpisodeNumber > b.EpisodeNumber) {
				items[j-1], items[j] = items[j], items[j-1]
				continue
			}
			break
		}
	}
}

// PlayInfo 把条目（Movie/Episode/TV）解析为可播放版本。
// 注意：对 TV guid 调用返回该剧的下一集。
func (c *Client) PlayInfo(ctx context.Context, itemGuid string) (*PlayInfo, error) {
	res, err := c.v1(ctx, "POST", "play/info", map[string]any{"item_guid": itemGuid})
	if err != nil {
		return nil, fmt.Errorf("play/info: %w", err)
	}
	var out PlayInfo
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("play/info decode: %w", err)
	}
	return &out, nil
}

// Stream 获取流信息（码流 + 直链）。
func (c *Client) Stream(ctx context.Context, mediaGuid, ua string) (*StreamResp, error) {
	if ua == "" {
		ua = PlayerUA
	}
	res, err := c.v1(ctx, "POST", "stream", map[string]any{
		"media_guid": mediaGuid,
		"ip":         DeviceFingerprint,
		"level":      1,
		"header":     map[string]any{"User-Agent": []string{ua}},
	})
	if err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}
	var out StreamResp
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("stream decode: %w", err)
	}
	return &out, nil
}

// PersonList 取条目演职员。
func (c *Client) PersonList(ctx context.Context, guid string) ([]Person, error) {
	res, err := c.v1(ctx, "POST", "person/list/"+guid, map[string]any{
		"guid": guid, "page": 1, "page_size": 1000, "lan": "zh-CN",
	})
	if err != nil {
		return nil, fmt.Errorf("person/list: %w", err)
	}
	var out struct {
		Total int      `json:"total"`
		List  []Person `json:"list"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		return nil, fmt.Errorf("person/list decode: %w", err)
	}
	return out.List, nil
}

// RecordProgress 回传播放进度（秒）到飞牛 play/record。
func (c *Client) RecordProgress(ctx context.Context, itemGuid, mediaGuid string, ts, duration int64) error {
	_, err := c.v1(ctx, "POST", "play/record", map[string]any{
		"item_guid":  itemGuid,
		"media_guid": mediaGuid,
		"ts":         ts,
		"duration":   duration,
	})
	return err
}

// MarkWatched 标记条目已看完。
func (c *Client) MarkWatched(ctx context.Context, itemGuid string) error {
	_, err := c.v1(ctx, "POST", "item/watched", map[string]any{"item_guid": itemGuid, "lan": "zh-CN"})
	return err
}

// UnmarkWatched 取消已看（飞牛要求 DELETE + body）。
func (c *Client) UnmarkWatched(ctx context.Context, itemGuid string) error {
	_, err := c.v1(ctx, "DELETE", "item/watched", map[string]any{"item_guid": itemGuid, "lan": "zh-CN"})
	return err
}

// SetFavorite 收藏 / 取消收藏。
func (c *Client) SetFavorite(ctx context.Context, itemGuid string, on bool) error {
	method := "PUT"
	if !on {
		method = "DELETE"
	}
	_, err := c.v1(ctx, method, "item/favorite", map[string]any{"item_guid": itemGuid, "lan": "zh-CN"})
	return err
}

// RawFetch 返回原始 HTTP 响应（供 Range 中转与图片代理）。
func (c *Client) RawFetch(ctx context.Context, method, fullPath string, body []byte, extraHeaders map[string]string) (*http.Response, error) {
	if c.http == nil {
		return nil, fmt.Errorf("client closed")
	}
	return c.doRaw(ctx, method, fullPath, body, extraHeaders)
}
