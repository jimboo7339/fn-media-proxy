package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/user/fn-media-proxy/internal/fn"
)

// ---- 基础探测 ----

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "<html><body><h1>"+h.ServerName+"</h1><p>Emby-compatible gateway for fnOS media.</p></body></html>")
}

func (h *Handler) systemInfo() map[string]any {
	return map[string]any{
		"SystemUpdateLevel":         "Release",
		"OperatingSystemDisplayName": "Linux",
		"HasPendingRestart":         false,
		"IsShuttingDown":            false,
		"SupportsLibraryMonitor":    false,
		"WebSocketPortNumber":       8096,
		"CompletedInstallations":    []any{},
		"CanSelfRestart":            false,
		"CanLaunchWebBrowser":       false,
		"ProgramDataPath":           "/config",
		"ItemsByNamePath":           "/config/metadata",
		"CachePath":                 "/config/cache",
		"LogPath":                   "/config/log",
		"InternalMetadataPath":      "/config/metadata",
		"TranscodingTempPath":       nil,
		"HasUpdateAvailable":        false,
		"EncoderLocation":           "System",
		"SystemArchitecture":        "X64",
		"Id":                        "fn-media-proxy",
		"ServerName":                h.ServerName,
		"Version":                   h.Version,
		"OperatingSystem":           "Linux",
	}
}

func (h *Handler) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.systemInfo())
}

func (h *Handler) handleSystemInfoPublic(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Id":                "fn-media-proxy",
		"ServerName":        h.ServerName,
		"Version":           h.Version,
		"OperatingSystem":   "Linux",
		"StartupWizardCompleted": true,
		"LocalAddress":      "http://" + r.Host,
		"LocalAddresses":    []string{"http://" + r.Host},
	})
}

func (h *Handler) handleSystemConfiguration(w http.ResponseWriter, r *http.Request) {
	// Hills/Yamby 强类型解析：字段必须齐全且类型正确
	writeJSON(w, http.StatusOK, map[string]any{
		"IsStartupWizardCompleted":  true,
		"ServerName":                h.ServerName,
		"UICulture":                 "zh-CN",
		"MetadataCountryCode":       "CN",
		"PreferredMetadataLanguage": "zh-CN",
		"LibraryScanFanoutConcurrency": 0,
		"LibraryMetadataRefreshConcurrency": 0,
	})
}

func (h *Handler) handleBranding(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"SplashscreenEnabled":     false,
		"LoginDisclaimer":         "",
		"CustomCss":               "",
	})
}

func (h *Handler) handleEmptyObject(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ---- 登录与用户 ----

type authRequest struct {
	Username string `json:"Username"`
	Pw       string `json:"Pw"`
	Pwd      string `json:"Pwd"`
	Password string `json:"Password"`
	PasswordMd5 string `json:"PasswordMd5"`
}

func (h *Handler) handleAuthenticate(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if ct := r.Header.Get("Content-Type"); strings.Contains(ct, "application/x-www-form-urlencoded") {
		_ = r.ParseForm()
		req.Username = r.PostForm.Get("Username")
		req.Pw = r.PostForm.Get("Pw")
		req.Pwd = r.PostForm.Get("Pwd")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"Error": "invalid body"})
			return
		}
	}
	username := strings.TrimSpace(req.Username)
	rawPw := firstNonEmpty(req.Pw, req.Pwd, req.Password)
	if username == "" || rawPw == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"Error": "missing credentials"})
		return
	}

	passHash := fn.NormalizePassword(rawPw)

	// 复用已存在的会话：避免客户端频繁重连触发飞牛登录频控
	h.mu.RLock()
	var existing *embySession
	var existingToken string
	for tok, s := range h.sessions {
		if s.UserName == username && s.PassHash == passHash {
			existing, existingToken = s, tok
			break
		}
	}
	h.mu.RUnlock()

	if existing != nil && existing.Client.GetToken() != "" {
		user := userDTO(username, stableUserID(username))
		writeJSON(w, http.StatusOK, map[string]any{
			"User":        user,
			"SessionInfo": sessionInfo(existingToken),
			"AccessToken": existingToken,
			"ServerId":    "fn-media-proxy",
		})
		return
	}

	// 新登录：用独立的飞牛客户端，互不顶号
	client := h.admin.Clone()
	token, err := client.Login(r.Context(), username, passHash)
	if err != nil {
		h.logf("login failed for %q: %v", username, err)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"Error": "invalid username or password"})
		return
	}
	_ = token
	sess := &embySession{
		Client:   client,
		FNToken:  client.GetToken(),
		UserID:   stableUserID(username),
		UserName: username,
		PassHash: passHash,
	}
	embyToken := h.registerSession(sess)
	h.logf("login ok: %s -> %s", username, embyToken)

	writeJSON(w, http.StatusOK, map[string]any{
		"User":        userDTO(username, sess.UserID),
		"SessionInfo": sessionInfo(embyToken),
		"AccessToken": embyToken,
		"ServerId":    "fn-media-proxy",
	})
}

func (h *Handler) handleUsersMe(w http.ResponseWriter, r *http.Request) {
	sess := h.sessionByToken(r)
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"Error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, userDTO(sess.UserName, sess.UserID))
}

func (h *Handler) handleUserByID(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	h.mu.RLock()
	sess := h.userByID[uid]
	h.mu.RUnlock()
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"Error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, userDTO(sess.UserName, sess.UserID))
}

func (h *Handler) handleUsersPublic(w http.ResponseWriter, r *http.Request) {
	out := []any{}
	h.mu.RLock()
	for _, s := range h.userByID {
		out = append(out, publicUserDTO(s.UserName, s.UserID))
	}
	h.mu.RUnlock()
	writeJSON(w, http.StatusOK, out)
}

func userConfiguration() map[string]any {
	return map[string]any{
		"AudioLanguagePreference":    "",
		"PlayDefaultAudioTrack":      true,
		"SubtitleLanguagePreference": "",
		"DisplayMissingEpisodes":     false,
		"GroupedFolders":             []string{},
		"SubtitleMode":               "Smart",
		"DisplayCollections":         false,
		"EnableLocalPassword":        false,
		"OrderedViews":               []string{},
		"LatestItemsExcludes":        []string{},
		"MyMediaExcludes":            []string{},
		"RememberAudioSelections":    true,
		"EnableNextEpisodeAutoPlay":  true,
	}
}

func userPolicy() map[string]any {
	return map[string]any{
		"IsAdministrator":          true,
		"IsHidden":                 false,
		"IsDisabled":               false,
		"EnableRemoteControlOfOtherUsers": false,
		"EnableSharedDeviceControl":       false,
		"EnableRemoteAccess":       true,
		"EnableLiveTvManagement":   false,
		"EnableLiveTvAccess":       false,
		"EnableMediaPlayback":      true,
		"EnableAudioPlaybackTranscoding": true,
		"EnableVideoPlaybackTranscoding": true,
		"EnablePlaybackRemuxing":   true,
		"EnableContentDownloading": true,
		"EnableSyncTranscoding":    false,
		"EnableMediaConversion":    false,
		"EnabledDevices":           []string{},
		"EnableAllDevices":         true,
		"EnabledChannels":          []string{},
		"EnableAllChannels":        true,
		"EnabledFolders":           []string{},
		"EnableAllFolders":         true,
		"InvalidLoginAttemptCount": 0,
		"AuthenticationProviderId": "DefaultAuthenticationProvider",
		"PasswordResetProviderId":  "DefaultPasswordResetProvider",
		"BlockedTags":              []string{},
		"AllowedTags":              []string{},
		"BlockUnratedItems":        []string{},
	}
}

func userDTO(name, id string) map[string]any {
	return map[string]any{
		"Name":                      name,
		"ServerId":                  "fn-media-proxy",
		"ServerName":                "fn-media-proxy",
		"Prefix":                    "",
		"Id":                        id,
		"HasPassword":               true,
		"HasConfiguredPassword":     true,
		"HasConfiguredEasyPassword": false,
		"EnableAutoLogin":           false,
		"LastLoginDate":             nil,
		"LastActivityDate":          nil,
		"Configuration":             userConfiguration(),
		"Policy":                    userPolicy(),
		"PrimaryImageTag":           nil,
	}
}

func publicUserDTO(name, id string) map[string]any {
	return map[string]any{
		"Name":                  name,
		"Id":                    id,
		"HasPassword":           true,
		"HasConfiguredPassword": true,
		"PrimaryImageTag":       nil,
	}
}

func sessionInfo(token string) map[string]any {
	return map[string]any{
		"Id":                 "",
		"UserId":             "",
		"UserName":           "",
		"Client":             "",
		"DeviceName":         "",
		"DeviceId":           "",
		"ApplicationVersion": "",
		"PlayableMediaTypes": []string{"Video", "Audio", "Photo"},
		"SupportsRemoteControl": false,
	}
}

// ---- 媒体库视图 ----

func (h *Handler) handleViews(w http.ResponseWriter, r *http.Request) {
	sess := h.requireSession(w, r)
	if sess == nil {
		return
	}
	dbs, err := sess.Client.MediaDBList(r.Context())
	if err != nil {
		h.fail(w, "mediadb/list", err)
		return
	}
	items := make([]any, 0, len(dbs))
	for _, db := range dbs {
		items = append(items, map[string]any{
			"Name":             db.Title,
			"Id":               db.Guid,
			"ServerId":         "fn-media-proxy",
			"Type":             "CollectionFolder",
			"CollectionType":   collectionType(db.Category),
			"IsFolder":         true,
			"BaseItemKind":     "CollectionFolder",
			"ImageTags":        primaryImageTag(db.Poster, db.Posters),
			"BackdropImageTags": []string{},
			"ChildCount":       0,
			"UserData": map[string]any{
				"PlayCount": 0, "IsFavorite": false, "Played": false,
				"PlaybackPositionTicks": 0, "UnplayedItemCount": 0,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            items,
		"TotalRecordCount": len(items),
		"StartIndex":       0,
	})
}

func primaryImageTag(poster string, posters []string) map[string]any {
	if poster == "" && len(posters) == 0 {
		return map[string]any{}
	}
	return map[string]any{"Primary": "v1"}
}

func collectionType(category string) string {
	switch category {
	case "TV":
		return "tvshows"
	case "Movie":
		return "movies"
	case "Live":
		return ""
	default:
		return ""
	}
}

// requireSession 取会话，失败时已写响应并返回 nil。
func (h *Handler) requireSession(w http.ResponseWriter, r *http.Request) *embySession {
	sess := h.sessionByToken(r)
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"Error": "unauthorized"})
		return nil
	}
	if sess.Client.GetToken() == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"Error": "fn token lost"})
		return nil
	}
	return sess
}

func (h *Handler) fail(w http.ResponseWriter, what string, err error) {
	h.logf("%s failed: %v", what, err)
	writeJSON(w, http.StatusBadGateway, map[string]any{"Error": what + ": " + err.Error()})
}

// ---- 进度 ----

func (h *Handler) recordProgress(ctx context.Context, sess *embySession, itemID string, positionTicks int64, stopped bool) {
	if sess == nil || itemID == "" {
		return
	}
	sec := positionTicks / 10_000_000
	// 需要 media_guid 才能回传；这里用 play/info 解析
	info, err := sess.Client.PlayInfo(ctx, itemID)
	if err != nil {
		h.logf("progress: play/info %s failed: %v", itemID, err)
		return
	}
	mediaGuid := info.MediaGuid
	if mediaGuid == "" {
		mediaGuid = info.Guid
	}
	dur := int64(0)
	if info.Item.SeasonNumber > 0 {
		_ = dur
	}
	if err := sess.Client.RecordProgress(ctx, itemID, mediaGuid, sec, dur); err != nil {
		h.logf("progress: record %s failed: %v", itemID, err)
	}
}

var _ = fmt.Sprintf
