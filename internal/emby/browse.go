package emby

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/fn-media-proxy/internal/fn"
)

// handleItems 实现 GET /Items 与 /Users/{uid}/Items —— Emby 的浏览主入口。
//
// 飞牛没有服务端搜索/聚合查询，因此：
//   - ParentId 为空 → 返回媒体库列表（等价 Views）
//   - ParentId 有值 → 按父节点类型分派
//   - SearchTerm / 复杂筛选：Phase 0 返回空结果集，Phase 2 用本地索引实现
func (h *Handler) handleItems(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	ctx := r.Context()
	client := sess.Client

	start := queryInt(r, "StartIndex", 0)
	limit := queryInt(r, "Limit", 0)
	parentID := queryGet(r, "ParentId")
	search := queryGet(r, "SearchTerm")

	// 搜索：Phase 0 不支持（飞牛无搜索接口），返回空集而非错误
	if strings.TrimSpace(search) != "" {
		writeQueryResult(w, nil, 0, start)
		return
	}

	// 首页合集/播放列表等飞牛没有的实体，直接空集
	includeTypes := strings.ToUpper(queryGet(r, "IncludeItemTypes"))
	if strings.Contains(includeTypes, "PLAYLIST") || strings.Contains(includeTypes, "BOXSET") ||
		strings.Contains(includeTypes, "COLLECTION") || strings.Contains(includeTypes, "PERSON") ||
		strings.Contains(includeTypes, "GENRE") || strings.Contains(includeTypes, "MUSIC") {
		writeQueryResult(w, nil, 0, start)
		return
	}

	// 收藏筛选
	if strings.Contains(strings.ToUpper(queryGet(r, "Filters")), "ISFAVORITE") {
		h.handleFavoriteList(w, r, client, start, limit)
		return
	}

	// 无 ParentId：返回库视图
	if parentID == "" {
		dbs, err := client.MediaDBList(ctx)
		if err != nil {
			h.fail(w, "mediadb/list", err)
			return
		}
		items := make([]fn.MediaItem, 0, len(dbs))
		for _, db := range dbs {
			items = append(items, fn.MediaItem{
				Guid: db.Guid, Title: db.Title, Type: "MediaDB",
				Poster: db.Poster, Posters: firstOf(db.Posters),
			})
		}
		h.emitItems(w, r, items, start, limit)
		return
	}

	// 有 ParentId：先判断父节点类型
	parent, err := client.ItemDetail(ctx, parentID)
	if err != nil {
		// 可能是库 guid（item/{guid} 对库无效），退回 ancestor 模式
		parent = nil
	}

	recursive := queryBool(r, "Recursive")
	var items []fn.MediaItem

	if parent != nil {
		switch parent.Type {
		case "TV":
			// 剧：默认列季；Recursive 或显式要 Episode 时列全剧集
			if recursive || strings.Contains(includeTypes, "EPISODE") {
				items, _ = client.SeriesEpisodes(ctx, parentID)
			} else {
				items, _ = client.SeasonList(ctx, parentID)
				if len(items) == 0 {
					items, _ = client.SeriesEpisodes(ctx, parentID)
				}
			}
		case "Season":
			items, _ = client.EpisodeList(ctx, parentID)
		default:
			// 库或目录
			items = h.listByAncestor(ctx, client, parentID)
		}
	} else {
		items = h.listByAncestor(ctx, client, parentID)
	}

	h.emitItems(w, r, items, start, limit)
}

// listByAncestor 用 ancestor_guid 模式拉库下条目（先 ancestor 再回退 parent）。
func (h *Handler) listByAncestor(ctx context.Context, client *fn.Client, guid string) []fn.MediaItem {
	var all []fn.MediaItem
	for page := 1; page <= 50; page++ {
		p, err := client.ItemListPage(ctx, "ancestor_guid", guid, page, 100)
		if err != nil || p == nil {
			break
		}
		all = append(all, p.List...)
		if len(all) >= p.Total || len(p.List) == 0 {
			break
		}
	}
	if len(all) > 0 {
		return all
	}
	for page := 1; page <= 20; page++ {
		p, err := client.ItemListPage(ctx, "parent_guid", guid, page, 100)
		if err != nil || p == nil {
			break
		}
		all = append(all, p.List...)
		if len(all) >= p.Total || len(p.List) == 0 {
			break
		}
	}
	return all
}

func filterByParent(items []fn.MediaItem, parent string) []fn.MediaItem {
	out := make([]fn.MediaItem, 0, len(items))
	for _, it := range items {
		if it.ParentGuid == parent {
			out = append(out, it)
		}
	}
	return out
}

// emitItems 统一输出 QueryResult，处理类型过滤与分页。
func (h *Handler) emitItems(w http.ResponseWriter, r *http.Request, items []fn.MediaItem, start, limit int) {
	includeTypes := strings.ToUpper(queryGet(r, "IncludeItemTypes"))
	if includeTypes != "" {
		want := map[string]bool{}
		mapped := false
		for _, t := range strings.Split(includeTypes, ",") {
			switch strings.TrimSpace(t) {
			case "SERIES", "TVSHOW":
				want["Series"], mapped = true, true
			case "SEASON":
				want["Season"], mapped = true, true
			case "EPISODE":
				want["Episode"], mapped = true, true
			case "MOVIE":
				want["Movie"], mapped = true, true
			case "VIDEO":
				want["Video"], mapped = true, true
			case "FOLDER", "COLLECTIONFOLDER":
				want["Folder"], want["CollectionFolder"], want["Series"] = true, true, true
				mapped = true
			}
		}
		if !mapped {
			items = nil
		} else {
			kept := make([]fn.MediaItem, 0, len(items))
			for _, it := range items {
				if want[embyType(it)] {
					kept = append(kept, it)
				}
			}
			items = kept
		}
	}

	total := len(items)
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	page := items[start:]
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}

	dtos := make([]any, 0, len(page))
	for _, it := range page {
		dtos = append(dtos, MediaItemToBaseItem(it, it.Guid))
	}
	writeQueryResult(w, dtos, total, start)
}

func writeQueryResult(w http.ResponseWriter, items []any, total, start int) {
	if items == nil {
		items = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            items,
		"TotalRecordCount": total,
		"StartIndex":       start,
	})
}

// handleItemByID 单条目详情。
func (h *Handler) handleItemByID(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	it, err := sess.Client.ItemDetail(r.Context(), id)
	if err != nil {
		// 可能是库 guid
		writeJSON(w, http.StatusNotFound, map[string]any{"Error": "item not found"})
		return
	}
	dto := MediaItemToBaseItem(*it, id)

	// 演职员（剧级挂在季上，做一次兜底探测）
	if embyType(*it) == "Series" || embyType(*it) == "Movie" {
		if people := h.peopleFor(r, sess, id); len(people) > 0 {
			dto["People"] = people
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

// handleSeasons 剧下的季列表。
func (h *Handler) handleSeasons(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	seasons, err := sess.Client.SeasonList(r.Context(), id)
	if err != nil {
		h.fail(w, "season/list", err)
		return
	}
	// 季在飞牛里可能为空，用集聚合伪造
	dtos := make([]any, 0, len(seasons))
	for _, s := range seasons {
		if s.Type != "Season" {
			continue
		}
		dtos = append(dtos, MediaItemToBaseItem(s, s.Guid))
	}
	writeQueryResult(w, dtos, len(dtos), 0)
}

// handleEpisodes 剧/季下的集列表。
func (h *Handler) handleEpisodes(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	eps, err := sess.Client.SeriesEpisodes(r.Context(), id)
	if err != nil {
		h.fail(w, "episode/list", err)
		return
	}
	// 若 id 本身是季，SeriesEpisodes 会查不到季；回退直接按季取
	if len(eps) == 0 {
		if direct, derr := sess.Client.EpisodeList(r.Context(), id); derr == nil {
			eps = direct
		}
	}
	start := queryInt(r, "StartIndex", 0)
	limit := queryInt(r, "Limit", 0)
	h.emitItems(w, r, eps, start, limit)
}

// handleResume 继续观看（按 ts>0 的条目聚合）。
func (h *Handler) handleResume(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	ctx := r.Context()
	var list []fn.MediaItem
	dbs, err := sess.Client.MediaDBList(ctx)
	if err == nil {
		for _, db := range dbs {
			items := h.listByAncestor(ctx, sess.Client, db.Guid)
			for _, it := range items {
				if it.Ts > 0 && it.Type == "Episode" || (it.Ts > 0 && it.Type == "Movie") {
					list = append(list, it)
				}
			}
		}
	}
	limit := queryInt(r, "Limit", 25)
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	h.emitItems(w, r, list, 0, 0)
}

// handleLatest 最近添加。
func (h *Handler) handleLatest(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	ctx := r.Context()
	var list []fn.MediaItem
	dbs, err := sess.Client.MediaDBList(ctx)
	if err == nil {
		for _, db := range dbs {
			p, err := sess.Client.ItemListPage(ctx, "ancestor_guid", db.Guid, 1, 30)
			if err != nil || p == nil {
				continue
			}
			list = append(list, p.List...)
		}
	}
	limit := queryInt(r, "Limit", 20)
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	h.emitItems(w, r, list, 0, 0)
}

// handleNextUp 下一集（对剧返回 play/info 解析出的下一集）。
func (h *Handler) handleNextUp(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		id = queryGet(r, "SeriesId")
	}
	if id == "" {
		writeQueryResult(w, nil, 0, 0)
		return
	}
	info, err := sess.Client.PlayInfo(r.Context(), id)
	if err != nil || info == nil {
		writeQueryResult(w, nil, 0, 0)
		return
	}
	it := fn.MediaItem{
		Guid: info.Item.Guid, Title: info.Item.Title, Type: info.Item.Type,
		TvTitle: info.Item.TvTitle, ParentGuid: info.Item.ParentGuid,
		AncestorGuid: info.Item.AncestorGuid, StillPath: info.Item.StillPath,
		SeasonNumber: info.Item.SeasonNumber, EpisodeNumber: info.Item.EpisodeNumber,
		AirDate: info.Item.AirDate, CanPlay: info.Item.CanPlay,
	}
	h.emitItems(w, r, []fn.MediaItem{it}, 0, 0)
}

// handleFavoriteList 收藏列表。
func (h *Handler) handleFavoriteList(w http.ResponseWriter, r *http.Request, client *fn.Client, start, limit int) {
	ctx := r.Context()
	var list []fn.MediaItem
	dbs, err := client.MediaDBList(ctx)
	if err == nil {
		for _, db := range dbs {
			for _, it := range h.listByAncestor(ctx, client, db.Guid) {
				if it.IsFavorite != 0 {
					list = append(list, it)
				}
			}
		}
	}
	h.emitItems(w, r, list, start, limit)
}

// ---- 工具 ----

func queryInt(r *http.Request, key string, def int) int {
	v := queryGet(r, key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func firstOf(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func (h *Handler) peopleFor(r *http.Request, sess *embySession, guid string) []any {
	people, err := sess.Client.PersonList(r.Context(), guid)
	if err != nil || len(people) == 0 {
		return nil
	}
	out := make([]any, 0, len(people))
	for _, p := range people {
		out = append(out, map[string]any{
			"Name": p.Name, "Id": p.PersonGuid, "Role": p.Role,
			"Type": personType(p.Job), "PrimaryImageTag": nil,
		})
	}
	return out
}

func personType(job string) string {
	switch job {
	case "Actor":
		return "Actor"
	case "Director":
		return "Director"
	case "Writer":
		return "Writer"
	}
	return "Actor"
}
