package emby

// 把飞牛数据结构转成 Emby BaseItemDto。
//
// 字段契约参考 Emby 4.8/4.9 官方 REST API，并针对实测过的客户端做宽容：
//   - 日期字段无值输出 null（空串会让 Flutter 客户端 DateTime.parse 崩溃）；
//   - MediaStream 的布尔字段全部给出（Hills 用强类型反序列化，缺字段即崩）；
//   - Codec 只给单个值（"h264,hevc" 这种逗号串会让客户端用 DeviceProfile 匹配时静默放弃）。

import (
	"fmt"
	"math"
	"strconv"

	"github.com/user/fn-media-proxy/internal/fn"
)

// MediaItemToBaseItem 把飞牛条目转成 Emby BaseItemDto。
// imageBase 用于生成 ImageTags（客户端凭 tag 非空才发图片请求）。
func MediaItemToBaseItem(it fn.MediaItem, imageBase string) map[string]any {
	t := embyType(it)
	dto := map[string]any{
		"Id":              it.Guid,
		"Name":            displayName(it),
		"Type":            t,
		"ServerId":        "fn-media-proxy",
		"IsFolder":        t == "Series" || t == "Season" || t == "Folder",
		"MediaType":       mediaType(t),
		"Overview":        strOrNull(it.Overview),
		"ProductionYear":  yearOrZero(it.ReleaseDate, it.AirDate),
		"DateCreated":     strOrNull(firstNonEmpty(it.ReleaseDate, it.AirDate)),
		"PremiereDate":    strOrNull(firstNonEmpty(it.ReleaseDate, it.AirDate)),
		"RunTimeTicks":    runtimeTicks(it),
		"UserData":        userData(it),
		"ImageTags":       imageTags(it, imageBase),
		"BackdropImageTags": backdropTags(it, imageBase),
		"ParentId":        it.ParentGuid,
		"GroupingKey":     nil,
		"Status":          strOrNull(it.Status),
		"EndDate":         nil,
		"Taglines":        []string{},
		"Genres":          []string{},
		"Tags":            []string{},
		"People":          []any{},
		"Studios":         []any{},
		"ProviderIds":     providerIDs(it),
		"ExternalUrls":    []any{},
		"MediaSources":    []any{},
		"MediaStreams":    []any{},
	}

	// 剧集专属字段
	if t == "Series" {
		if it.NumberOfSeasons > 0 {
			dto["ChildCount"] = it.LocalNumberOfEpisodes
			if n := it.NumberOfSeasons; n > 0 {
				dto["RecursiveItemCount"] = it.LocalNumberOfEpisodes
				dto["ProductionYear"] = yearOrZero(it.ReleaseDate, it.AirDate)
			}
		}
		if it.SeasonNumber > 0 {
			dto["IndexNumber"] = it.SeasonNumber
		}
	}
	if t == "Season" {
		dto["IndexNumber"] = it.SeasonNumber
		dto["SeriesId"] = it.ParentGuid
		dto["SeriesName"] = strOrNull(it.TvTitle)
	}
	if t == "Episode" {
		dto["IndexNumber"] = it.EpisodeNumber
		dto["ParentIndexNumber"] = it.SeasonNumber
		dto["SeriesId"] = it.AncestorGuid
		if dto["SeriesId"] == "" {
			dto["SeriesId"] = it.ParentGuid
		}
		dto["SeriesName"] = strOrNull(it.TvTitle)
		dto["SeasonId"] = it.ParentGuid
		if it.StillPath != "" {
			dto["ImageTags"] = map[string]any{"Primary": imageBase + "-still"}
			dto["BackdropImageTags"] = []string{imageBase + "-still"}
		}
	}
	if t == "Movie" || t == "Video" {
		dto["IsFolder"] = false
	}

	// 评分（飞牛是字符串，Emby 需要 double）
	if v, err := strconv.ParseFloat(it.VoteAverage, 64); err == nil && v > 0 && v != 0 {
		dto["CommunityRating"] = v
		// 简单映射到官方评分档
		if v >= 9 {
			dto["OfficialRating"] = "PG-13"
		}
	}

	return dto
}

// displayName 取显示名：集优先用 parent_title（"第1集"语义），否则 title。
func displayName(it fn.MediaItem) string {
	if it.Title != "" {
		return it.Title
	}
	return it.TvTitle
}

// embyType 飞牛类型 → Emby 类型。
func embyType(it fn.MediaItem) string {
	switch it.Type {
	case "TV":
		return "Series"
	case "Season":
		return "Season"
	case "Episode":
		return "Episode"
	case "Movie":
		return "Movie"
	case "Video":
		return "Video"
	case "Directory":
		return "Folder"
	case "MediaDB":
		return "CollectionFolder"
	}
	return "Video"
}

func mediaType(t string) any {
	switch t {
	case "Series", "Season", "Episode":
		return "Video"
	case "Movie", "Video":
		return "Video"
	case "Folder", "CollectionFolder":
		return nil
	}
	return nil
}

// runtimeTicks 飞牛 duration(秒) 或 runtime(分钟) → Emby ticks(100ns)。
func runtimeTicks(it fn.MediaItem) int64 {
	sec := int64(0)
	if it.Duration > 0 {
		sec = int64(it.Duration)
	} else if it.Runtime > 0 {
		sec = int64(it.Runtime) * 60
	}
	return sec * 10_000_000
}

// userData 生成 Emby UserItemDataDto。
func userData(it fn.MediaItem) map[string]any {
	played := it.Watched != 0 || it.IsWatched != 0
	pos := it.Ts * 10_000_000
	total := runtimeTicks(it)
	pct := 0.0
	if total > 0 {
		pct = math.Round(float64(pos) / float64(total) * 100)
	}
	return map[string]any{
		"PlayCount":                 boolToCount(played),
		"IsFavorite":                it.IsFavorite != 0,
		"Played":                    played,
		"PlaybackPositionTicks":     pos,
		"PlayedPercentage":          pct,
		"UnplayedItemCount":         0,
		"LastPlayedDate":            strOrNullTime(it.WatchedTs),
		"Likes":                     nil,
		"Ratings":                   nil,
		"ItemId":                    it.Guid,
		"Key":                       it.Guid,
	}
}

func boolToCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

// imageTags 生成 ImageTags。Primary 用海报，Logo 用 logos，Thumb/Backdrop 用 backdrops。
func imageTags(it fn.MediaItem, imageBase string) map[string]any {
	tags := map[string]any{}
	p := it.PosterPath()
	if p != "" {
		tags["Primary"] = imageBase + "-primary"
	}
	if it.Logos != "" {
		tags["Logo"] = imageBase + "-logo"
	}
	// 集没有独立海报时用 still
	if it.Type == "Episode" && it.StillPath != "" {
		tags["Primary"] = imageBase + "-still"
	}
	return tags
}

// backdropTags 生成 BackdropImageTags（横版剧照）。
func backdropTags(it fn.MediaItem, imageBase string) []string {
	b := it.Backdrops
	if b == "" {
		b = it.StillPath
	}
	if b == "" {
		return []string{}
	}
	return []string{imageBase + "-backdrop"}
}

// providerIDs 生成 ProviderIds（IMDB/TMDB）。
func providerIDs(it fn.MediaItem) map[string]string {
	m := map[string]string{}
	if it.ImdbID != "" {
		m["Imdb"] = it.ImdbID
	}
	if it.TrimID != "" {
		m["Tmdb"] = it.TrimID
	}
	return m
}

// ---- 工具 ----

func strOrNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// strOrNullTime 毫秒时间戳 → ISO 时间字符串，0 值返回 null。
func strOrNullTime(ms int64) any {
	if ms <= 0 {
		return nil
	}
	return fmt.Sprintf("/Date(%d)/", ms)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// yearOrZero 从日期串取年份。
func yearOrZero(vals ...string) any {
	for _, v := range vals {
		if len(v) >= 4 {
			if y, err := strconv.Atoi(v[:4]); err == nil && y > 1900 {
				return y
			}
		}
	}
	return 0
}
