package jellyfin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/image"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/userdata"
)

func unescapeParam(r *http.Request, key string) string {
	val := chi.URLParam(r, key)
	if unescaped, err := url.PathUnescape(val); err == nil {
		return unescaped
	}
	return val
}

const serverID = "jmlab"

var mediaBrowserAttribute = regexp.MustCompile(`(?i)([a-z0-9_-]+)\s*=\s*"([^"]*)"`)

type Service struct {
	db                   *repository.Database
	cfg                  config.Config
	authService          *auth.Service
	catalog              *catalog.Service
	userData             *userdata.Service
	imageService         *image.Service
	mediaSourcesProvider func(context.Context, catalog.Item, auth.User) ([]map[string]any, error)
}

type BaseItemDto struct {
	SeriesId                string            `json:"SeriesId,omitempty"`
	SeriesName              string            `json:"SeriesName,omitempty"`
	SeasonId                string            `json:"SeasonId,omitempty"`
	SeasonName              string            `json:"SeasonName,omitempty"`
	ParentIndexNumber       *int              `json:"ParentIndexNumber,omitempty"`
	ParentId                string            `json:"ParentId,omitempty"`
	IndexNumber             *int              `json:"IndexNumber,omitempty"`
	Name                    string            `json:"Name"`
	OriginalTitle           string            `json:"OriginalTitle,omitempty"`
	ServerId                string            `json:"ServerId"`
	Id                      string            `json:"Id"`
	Etag                    string            `json:"Etag,omitempty"`
	DateCreated             string            `json:"DateCreated,omitempty"`
	Container               string            `json:"Container,omitempty"`
	SortName                string            `json:"SortName,omitempty"`
	PremiereDate            string            `json:"PremiereDate,omitempty"`
	CommunityRating         *float64          `json:"CommunityRating,omitempty"`
	ProductionYear          *int              `json:"ProductionYear,omitempty"`
	Overview                string            `json:"Overview,omitempty"`
	Genres                  []string          `json:"Genres,omitempty"`
	Tags                    []string          `json:"Tags,omitempty"`
	People                  []BaseItemPerson  `json:"People,omitempty"`
	Studios                 []NameIdPair      `json:"Studios,omitempty"`
	ProviderIds             map[string]string `json:"ProviderIds,omitempty"`
	ImageTags               map[string]string `json:"ImageTags,omitempty"`
	BackdropImageTags       []string          `json:"BackdropImageTags,omitempty"`
	Type                    string            `json:"Type"`
	MediaType               string            `json:"MediaType,omitempty"`
	CollectionType          string            `json:"CollectionType,omitempty"`
	ChildCount              int               `json:"ChildCount,omitempty"`
	RunTimeTicks            *int64            `json:"RunTimeTicks,omitempty"`
	UserData                *JellyfinUserData `json:"UserData,omitempty"`
	Path                    string            `json:"Path,omitempty"`
	IsFolder                bool              `json:"IsFolder,omitempty"`
	CanDownload             bool              `json:"CanDownload,omitempty"`
	SupportsSync            bool              `json:"SupportsSync,omitempty"`
	RecursiveItemCount      int               `json:"RecursiveItemCount,omitempty"`
	LocationType            string            `json:"LocationType,omitempty"`
	PrimaryImageAspectRatio *float64          `json:"PrimaryImageAspectRatio,omitempty"`
	MediaSources            []map[string]any  `json:"MediaSources,omitempty"`
	MediaSourceCount        int               `json:"MediaSourceCount,omitempty"`
	PlayAccess              string            `json:"PlayAccess,omitempty"`
	CanResume               bool              `json:"CanResume,omitempty"`
}

type BaseItemPerson struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
	Role string `json:"Role,omitempty"`
	Type string `json:"Type"`
}

type NameIdPair struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

type JellyfinUserData struct {
	PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
	PlayCount             int      `json:"PlayCount"`
	IsFavorite            bool     `json:"IsFavorite"`
	Played                bool     `json:"Played"`
	Key                   string   `json:"Key"`
	ItemId                string   `json:"ItemId"`
	Rating                *float64 `json:"Rating,omitempty"`
}

func NewService(db *repository.Database, cfg config.Config, authService *auth.Service, catalogService *catalog.Service, userDataService *userdata.Service) *Service {
	return &Service{db: db, cfg: cfg, authService: authService, catalog: catalogService, userData: userDataService}
}

func (s *Service) SetImageService(imageService *image.Service) {
	s.imageService = imageService
}

func (s *Service) SetMediaSourcesProvider(provider func(context.Context, catalog.Item, auth.User) ([]map[string]any, error)) {
	s.mediaSourcesProvider = provider
}

// RegisterRoutes intentionally adds no duplicate routes below /api/v1. The
// Jellyfin protocol uses root-level paths and is registered separately.
func (s *Service) RegisterRoutes(chi.Router) {}

func (s *Service) RegisterProtocolRoutes(router chi.Router) {
	router.Get("/System/Info", s.systemInfo)
	router.Get("/System/Info/Public", s.systemInfo)
	router.Get("/System/Configuration", s.systemConfiguration)
	router.Get("/System/Configuration/Branding", s.brandingConfig)
	router.Get("/Users/Public", s.publicUsers)
	router.Get("/Branding/Configuration", s.brandingConfig)
	router.Get("/Branding/Css", s.brandingCss)
	router.Get("/QuickConnect/Enabled", s.quickConnectEnabled)
	router.Get("/Startup/Status", s.startupStatus)
	router.Get("/Startup/Configuration", s.startupConfiguration)
	router.Get("/Startup/User", s.startupUser)
	router.Post("/Startup/Complete", s.startupComplete)
	router.Get("/Auth/Providers", s.authProviders)
	router.Get("/Auth/Keys", s.authKeys)
	router.Get("/Localization/Options", s.localizationOptions)
	router.Get("/Localization/Cultures", s.localizationCultures)
	router.Get("/Localization/ParentalRatings", s.localizationParentalRatings)
	router.Get("/Environment/DefaultDirectoryBrowser", s.defaultDirectoryBrowser)
	router.Post("/Users/AuthenticateByName", s.authenticateByName)

	router.With(s.authService.RequireAuth).Get("/Items/{itemID}/Images/{imageType}", s.image)
	router.With(s.authService.RequireAuth).Head("/Items/{itemID}/Images/{imageType}", s.image)
	router.With(s.authService.RequireAuth).Get("/Items/{itemID}/Images/{imageType}/{imageIndex}", s.image)
	router.With(s.authService.RequireAuth).Head("/Items/{itemID}/Images/{imageType}/{imageIndex}", s.image)

	protected := router.With(s.authService.RequireAuth)
	protected.Get("/Users", s.listUsers)
	protected.Get("/Users/Me", s.getUser)
	protected.Get("/Users/{userID}", s.getUser)
	protected.Get("/Users/{userID}/Views", s.views)
	protected.Get("/DisplayPreferences/{displayPreferencesId}", s.getDisplayPreferences)
	protected.Post("/DisplayPreferences/{displayPreferencesId}", s.saveDisplayPreferences)
	protected.Post("/Sessions/Capabilities", s.sessionCapabilities)
	protected.Post("/Sessions/Capabilities/Full", s.sessionCapabilities)
	protected.Get("/Sessions", s.sessions)
	protected.Post("/Sessions/Logout", s.logout)
	protected.Get("/Items/Counts", s.itemCounts)
	protected.Get("/Items", s.items)
	protected.Get("/Shows/{itemID}/Seasons", s.showChildren)
	protected.Get("/Shows/{itemID}/Episodes", s.showChildren)
	protected.Get("/Users/{userID}/Items", s.items)
	protected.Get("/Users/{userID}/Items/Resume", s.items)
	protected.Get("/Users/{userID}/Items/Latest", s.latestItems)
	protected.Get("/Items/Latest", s.latestItems)
	protected.Get("/Items/{itemID}", s.item)
	protected.Get("/Users/{userID}/Items/{itemID}", s.item)
	protected.Get("/Search/Hints", s.searchHints)
	protected.Get("/Genres", s.genres)
	protected.Get("/Studios", s.studios)
	protected.Get("/Persons", s.persons)
	protected.Get("/Users/{userID}/Items/{itemID}/UserData", s.getUserData)
	protected.Post("/Users/{userID}/Items/{itemID}/UserData", s.postUserData)
	protected.Post("/Sessions/Playing", s.playing)
	protected.Post("/Sessions/Playing/Progress", s.playingProgress)
	protected.Post("/Sessions/Playing/Stopped", s.playingStopped)
	protected.Get("/Users/{userID}/PlayedItems/{itemID}", s.getPlayedItem)
	protected.Post("/Users/{userID}/PlayedItems/{itemID}", s.markPlayed)
	protected.Delete("/Users/{userID}/PlayedItems/{itemID}", s.markUnplayed)
	protected.Post("/Users/{userID}/FavoriteItems/{itemID}", s.markFavorite)
	protected.Delete("/Users/{userID}/FavoriteItems/{itemID}", s.markUnfavorite)
	protected.Get("/Playlists", s.listPlaylists)
	protected.Post("/Playlists", s.createPlaylist)
	protected.Get("/Playlists/{playlistID}/Items", s.playlistItems)
	protected.Post("/Playlists/{playlistID}/Items", s.addPlaylistItems)
	protected.Delete("/Playlists/{playlistID}/Items", s.removePlaylistItems)
	protected.Get("/System/Configuration", s.systemConfiguration)
	protected.Get("/System/Configuration/Branding", s.brandingConfig)
	protected.Get("/Localization/Options", s.localizationOptions)
	protected.Get("/Localization/Cultures", s.localizationCultures)
	protected.Get("/Localization/ParentalRatings", s.localizationParentalRatings)
	protected.Get("/Environment/DefaultDirectoryBrowser", s.defaultDirectoryBrowser)
}

func (s *Service) systemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ServerName": "JMlab", "Version": "0.1.0", "ProductName": "JMlab",
		"OperatingSystem": "Linux", "Id": serverID, "StartupWizardCompleted": true,
	})
}

func (s *Service) authenticateByName(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"Username"`
		Password string `json:"Pw"`
		AltPw    string `json:"Password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeJellyfinError(w, http.StatusBadRequest, "用户名或密码格式不正确")
		return
	}
	if input.Password == "" {
		input.Password = input.AltPw
	}
	client, device, deviceID := clientMetadata(r)
	session, token, err := s.authService.AuthenticateCredentials(r.Context(), input.Username, input.Password, client, deviceID)
	if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrDisabled) {
		writeJellyfinError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "创建客户端会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"AccessToken": token,
		"ServerId":    serverID,
		"User":        jellyfinUser(session.User),
		"SessionInfo": map[string]any{
			"PlayState": map[string]any{
				"CanSeek": false, "IsPaused": false, "IsMuted": false, "RepeatMode": "RepeatNone",
			},
			"AdditionalUsers": []any{},
			"Capabilities": map[string]any{
				"PlayableMediaTypes":           []string{"Video", "Audio"},
				"SupportedCommands":            []string{},
				"SupportsMediaControl":         true,
				"SupportsPersistentIdentifier": true,
			},
			"Id": session.ID, "UserId": session.User.ID, "UserName": session.User.DisplayName,
			"Client": client, "DeviceName": device, "DeviceId": deviceID,
			"ApplicationVersion": clientVersion(r), "ServerId": serverID,
		},
	})
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, []any{jellyfinUser(principal.User)})
}

func (s *Service) getUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	writeJSON(w, http.StatusOK, jellyfinUser(principal.User))
}

func jellyfinUser(user auth.User) map[string]any {
	return map[string]any{
		"Name":                  user.DisplayName,
		"ServerId":              serverID,
		"Id":                    user.ID,
		"HasPassword":           true,
		"HasConfiguredPassword": true,
		"EnableAutoLogin":       false,
		"LastLoginDate":         time.Now().UTC().Format(time.RFC3339),
		"LastActivityDate":      time.Now().UTC().Format(time.RFC3339),
		"Configuration": map[string]any{
			"AudioLanguagePreference":    "",
			"PlayDefaultAudioTrack":      true,
			"SubtitleLanguagePreference": "",
			"DisplayMissingEpisodes":     false,
			"GroupedFolders":             []string{},
			"SubtitleMode":               "Default",
			"DisplayCollectionsView":     true,
			"EnableLocalPassword":        false,
			"OrderedViews":               []string{},
			"LatestItemsExcludes":        []string{},
			"MyMediaExcludes":            []string{},
			"HidePlayedInLatest":         false,
			"RememberAudioSelections":    true,
			"RememberSubtitleSelections": true,
			"EnableNextEpisodeAutoPlay":  true,
		},
		"Policy": map[string]any{
			"IsAdministrator":                  user.IsAdmin,
			"IsHidden":                         false,
			"IsDisabled":                       user.Status == "disabled",
			"EnableAllFolders":                 true,
			"EnableAllChannels":                true,
			"EnableAllDevices":                 true,
			"EnableContentDeletion":            false,
			"EnableContentDeletionFromFolders": []string{},
			"EnableContentDownloading":         true,
			"EnableSharedDeviceControl":        true,
			"EnableRemoteControlOfOtherUsers":  user.IsAdmin,
			"EnableLiveTvManagement":           false,
			"EnableLiveTvAccess":               false,
			"EnableMediaPlayback":              true,
			"EnableAudioPlaybackTranscoding":   true,
			"EnableVideoPlaybackTranscoding":   true,
			"EnablePlaybackRemuxing":           true,
			"ForceRemoteSourceTranscoding":     false,
			"EnablePublicSharing":              false,
			"BlockedMediaFolders":              []string{},
			"BlockedChannels":                  []string{},
			"RemoteClientBitrateLimit":         0,
			"AuthenticationProviderId":         "Default",
			"PasswordResetProviderId":          "Default",
			"SyncPlayAccess":                   "CreateAndJoinGroups",
		},
	}
}

func (s *Service) views(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", principal.User.ID, isElevated(principal.User), false)
	libraryVisibility, libraryArgs := auth.LibraryVisibilitySQL("vl", principal.User.ID, isElevated(principal.User), false)
	query := `SELECT vl.id, vl.name, COALESCE(vl.description, ''), COUNT(DISTINCT vi.id)
FROM virtual_libraries vl
LEFT JOIN library_items li ON li.library_id = vl.id
LEFT JOIN media_items vi ON vi.id = li.item_id AND (` + visibility + `) WHERE vl.enabled=1 AND (` + libraryVisibility + `)`
	args := append(accessArgs, libraryArgs...)
	query += ` GROUP BY vl.id, vl.name, vl.description ORDER BY vl.name`
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), args...)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取媒体库失败")
		return
	}
	defer rows.Close()
	items := make([]BaseItemDto, 0)
	aspectRatio := 0.7
	for rows.Next() {
		var id, name, overview string
		var childCount int
		if err := rows.Scan(&id, &name, &overview, &childCount); err != nil {
			writeJellyfinError(w, http.StatusInternalServerError, "读取媒体库失败")
			return
		}
		items = append(items, BaseItemDto{
			Name:                    name,
			Id:                      id,
			ServerId:                serverID,
			Type:                    "CollectionFolder",
			CollectionType:          "movies",
			ChildCount:              childCount,
			RecursiveItemCount:      childCount,
			Overview:                overview,
			IsFolder:                true,
			LocationType:            "Virtual",
			ImageTags:               map[string]string{"Primary": "1"},
			PrimaryImageAspectRatio: &aspectRatio,
		})
	}
	if err := rows.Err(); err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取媒体库失败")
		return
	}
	writeQueryResult(w, items, len(items))
}

func (s *Service) latestItems(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	limit := parseNonNegative(firstValue(values, "Limit", "limit"), 16)
	if limit == 0 || limit > 100 {
		limit = 16
	}
	values.Set("Limit", strconv.Itoa(limit))
	values.Set("StartIndex", "0")
	values.Set("SortBy", "DateCreated")
	values.Set("SortOrder", "Descending")
	r.URL.RawQuery = values.Encode()
	s.serveItems(w, r, true)
}

func (s *Service) item(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	item, err := s.catalog.GetItemForUser(r.Context(), unescapeParam(r, "itemID"), principal.User)
	if errors.Is(err, sql.ErrNoRows) {
		writeJellyfinError(w, http.StatusNotFound, "作品不存在")
		return
	}
	if err != nil {
		writeJellyfinError(w, http.StatusNotFound, "作品不存在或不可见")
		return
	}
	dto, err := s.toBaseItem(r.Context(), item, principal.User.ID)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取媒体层级失败")
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Service) queryItemIDs(ctx context.Context, query string, args []any) ([]string, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) toBaseItem(ctx context.Context, item catalog.Item, userID string) (BaseItemDto, error) {
	genres := make([]string, 0, len(item.Tags))
	tags := make([]string, 0, len(item.Tags))
	for _, tag := range item.Tags {
		genres = append(genres, tag.Name)
		tags = append(tags, tag.Name)
	}
	people := make([]BaseItemPerson, 0, len(item.People))
	for _, person := range item.People {
		role := "Actor"
		if strings.EqualFold(person.Role, "director") {
			role = "Director"
		} else if strings.EqualFold(person.Role, "writer") {
			role = "Writer"
		}
		people = append(people, BaseItemPerson{Name: person.Name, Id: person.ID, Role: person.Role, Type: role})
	}
	studios := make([]NameIdPair, 0, len(item.Studios))
	for _, studio := range item.Studios {
		studios = append(studios, NameIdPair{Name: studio.Name, Id: studio.ID})
	}
	providers := mediaProviderIDs(item.Source, item.Code, item.SourceID, item.ProviderIDs)
	var runtimeTicks *int64
	if item.RuntimeSeconds != nil {
		value := int64(*item.RuntimeSeconds) * 10_000_000
		runtimeTicks = &value
	}
	state, _ := s.userData.GetItemState(ctx, userID, item.ID)
	userData := toJellyfinUserData(state)
	aspectRatio := 0.7
	mediaSources := s.itemMediaSources(ctx, item)
	result := []BaseItemDto{{
		Name: item.Title, OriginalTitle: item.OriginalTitle, ServerId: serverID, Id: item.ID,
		Etag: item.UpdatedAt, DateCreated: item.CreatedAt, SortName: item.Title,
		PremiereDate: item.ReleaseDate, CommunityRating: item.SourceRating, ProductionYear: item.Year,
		Overview: item.Overview, Genres: genres, Tags: tags, People: people, Studios: studios,
		ProviderIds: providers, ImageTags: s.imageTags(ctx, item.ID), Type: mediaType(item.ItemType), MediaType: "Video",
		ParentId: item.ParentID, IndexNumber: item.IndexNumber, IsFolder: item.ItemType == "series" || item.ItemType == "season",
		RunTimeTicks: runtimeTicks, UserData: &userData,
		PrimaryImageAspectRatio: &aspectRatio,
		MediaSources:            mediaSources,
		MediaSourceCount:        len(mediaSources),
		PlayAccess:              "Full",
		CanResume:               item.ItemType != "series" && item.ItemType != "season" && state.PositionTicks > 0 && !state.IsWatched && (state.RuntimeTicks <= 0 || state.PositionTicks < state.RuntimeTicks),
	}}
	if err := s.enrichHierarchy(ctx, result, userID); err != nil {
		return BaseItemDto{}, err
	}
	return result[0], nil
}

func (s *Service) itemMediaSources(ctx context.Context, item catalog.Item) []map[string]any {
	if item.ItemType == "series" || item.ItemType == "season" || s.mediaSourcesProvider == nil {
		return nil
	}
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}
	sources, err := s.mediaSourcesProvider(ctx, item, principal.User)
	if err != nil {
		return nil
	}
	return sources
}

func (s *Service) imageTags(ctx context.Context, itemID string) map[string]string {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT image_type, id FROM item_images WHERE item_id = ? AND status != 'error' ORDER BY sort_order"), itemID)
	if err != nil {
		return map[string]string{"Primary": "1"}
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var imageType, id string
		if rows.Scan(&imageType, &id) == nil {
			key := map[string]string{"poster": "Primary", "backdrop": "Backdrop", "gallery": "Thumb", "preview": "Thumb", "avatar": "Primary"}[imageType]
			if key != "" {
				if _, exists := result[key]; !exists {
					result[key] = id
				}
			}
		}
	}
	if _, ok := result["Primary"]; !ok {
		if b, ok := result["Backdrop"]; ok {
			result["Primary"] = b
		} else if t, ok := result["Thumb"]; ok {
			result["Primary"] = t
		} else {
			result["Primary"] = "1"
		}
	}
	return result
}

func (s *Service) searchHints(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	term := catalog.NormalizeSearchQuery(firstValue(r.URL.Query(), "SearchTerm", "searchTerm", "q"))
	if term == "" {
		writeJSON(w, http.StatusOK, map[string]any{"SearchHints": []any{}, "TotalRecordCount": 0})
		return
	}
	query := `SELECT vi.id FROM media_items vi`
	args := []any{}
	if !isElevated(principal.User) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", principal.User.ID, false, false)
		query += ` JOIN library_items li ON li.item_id=vi.id JOIN virtual_libraries vl ON vl.id=li.library_id AND (` + visibility + `)`
		args = append(args, accessArgs...)
	}
	pattern := "%" + strings.ToLower(term) + "%"
	query += ` WHERE vi.deleted_at IS NULL AND (LOWER(vi.code) LIKE ? OR LOWER(vi.title) LIKE ? OR LOWER(COALESCE(vi.original_title, '')) LIKE ? OR EXISTS (SELECT 1 FROM item_people ip JOIN people p ON p.id = ip.person_id WHERE ip.item_id = vi.id AND LOWER(p.name) LIKE ?) OR EXISTS (SELECT 1 FROM item_studios ist JOIN studios st ON st.id = ist.studio_id WHERE ist.item_id = vi.id AND LOWER(st.name) LIKE ?)) GROUP BY vi.id, vi.title, vi.code, vi.updated_at ORDER BY CASE WHEN UPPER(vi.code) = UPPER(?) THEN 0 WHEN UPPER(vi.code) LIKE UPPER(?) THEN 1 ELSE 2 END, vi.title LIMIT 50`
	args = append(args, pattern, pattern, pattern, pattern, pattern, term, term+"%")
	ids, err := s.queryItemIDs(r.Context(), query, args)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "搜索失败")
		return
	}
	hints := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		item, err := s.catalog.GetItemForUser(r.Context(), id, principal.User)
		if err != nil {
			continue
		}
		hints = append(hints, map[string]any{"ItemId": item.ID, "Id": item.ID, "Name": item.Title, "MatchedTerm": term, "Type": mediaType(item.ItemType), "ProductionYear": item.Year, "RunTimeTicks": runtimeTicks(item.RuntimeSeconds), "PrimaryImageTag": s.imageTags(r.Context(), item.ID)["Primary"]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"SearchHints": hints, "TotalRecordCount": len(hints)})
}

func (s *Service) genres(w http.ResponseWriter, r *http.Request) {
	s.listEntities(w, r, "tags", "name", "Genre")
}
func (s *Service) studios(w http.ResponseWriter, r *http.Request) {
	s.listEntities(w, r, "studios", "name", "Studio")
}
func (s *Service) persons(w http.ResponseWriter, r *http.Request) {
	s.listEntities(w, r, "people", "name", "Person")
}

func (s *Service) listEntities(w http.ResponseWriter, r *http.Request, table, column, itemType string) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	term := catalog.NormalizeSearchQuery(firstValue(r.URL.Query(), "SearchTerm", "searchTerm", "q"))
	query := fmt.Sprintf("SELECT DISTINCT e.id, e.%s FROM %s e", column, table)
	args := []any{}
	if !isElevated(principal.User) {
		visibility, accessArgs := auth.MediaVisibilitySQL("linked_media", principal.User.ID, false, false)
		query += ` JOIN ` + map[string]string{"tags": "item_tags", "studios": "item_studios", "people": "item_people"}[table] + ` rel ON rel.` + map[string]string{"tags": "tag_id", "studios": "studio_id", "people": "person_id"}[table] + `=e.id JOIN media_items linked_media ON linked_media.id=rel.item_id AND (` + visibility + `)`
		args = append(args, accessArgs...)
	}
	query += ` WHERE e.is_excluded = 0`
	if term != "" {
		query += ` AND LOWER(e.` + column + `) LIKE ?`
		args = append(args, "%"+strings.ToLower(term)+"%")
	}
	query += ` ORDER BY e.` + column + ` LIMIT 500`
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), args...)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取分类数据失败")
		return
	}
	defer rows.Close()
	items := make([]BaseItemDto, 0)
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			items = append(items, BaseItemDto{Name: name, Id: id, ServerId: serverID, Type: itemType})
		}
	}
	writeQueryResult(w, items, len(items))
}

func (s *Service) batchBaseItems(ctx context.Context, ids []string, userID string) ([]BaseItemDto, error) {
	if len(ids) == 0 {
		return []BaseItemDto{}, nil
	}
	query := fmt.Sprintf(`SELECT id, source, COALESCE(source_id, ''), code, raw_code, title, COALESCE(original_title, ''), COALESCE(overview, ''), COALESCE(release_date, ''), year, runtime_seconds, source_rating, rating_count, source_url, metadata_status, created_at, updated_at, item_type, COALESCE(parent_id, ''), index_number, provider_ids_json FROM media_items WHERE id IN (%s)`, placeholders(len(ids)))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rawItem struct {
		kind, parentID, idsJSON                                                                                                   string
		index                                                                                                                     *int
		id, source, sourceID, code, rawCode, title, origTitle, overview, releaseDate, sourceURL, metaStatus, createdAt, updatedAt string
		year, runtimeSeconds                                                                                                      *int
		sourceRating                                                                                                              *float64
		ratingCount                                                                                                               int
	}
	itemMap := make(map[string]rawItem, len(ids))
	for rows.Next() {
		var it rawItem
		if err := rows.Scan(&it.id, &it.source, &it.sourceID, &it.code, &it.rawCode, &it.title, &it.origTitle, &it.overview, &it.releaseDate, &it.year, &it.runtimeSeconds, &it.sourceRating, &it.ratingCount, &it.sourceURL, &it.metaStatus, &it.createdAt, &it.updatedAt, &it.kind, &it.parentID, &it.index, &it.idsJSON); err == nil {
			itemMap[it.id] = it
		}
	}
	_ = rows.Close()

	peopleQuery := fmt.Sprintf(`SELECT ip.item_id, p.id, p.name, ip.role FROM item_people ip JOIN people p ON p.id = ip.person_id WHERE ip.item_id IN (%s) ORDER BY ip.sort_order`, placeholders(len(ids)))
	pRows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(peopleQuery), args...)
	peopleMap := make(map[string][]BaseItemPerson)
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var itemID, pID, name, role string
			if pRows.Scan(&itemID, &pID, &name, &role) == nil {
				pType := "Actor"
				if strings.EqualFold(role, "director") {
					pType = "Director"
				} else if strings.EqualFold(role, "writer") {
					pType = "Writer"
				}
				peopleMap[itemID] = append(peopleMap[itemID], BaseItemPerson{Name: name, Id: pID, Role: role, Type: pType})
			}
		}
		_ = pRows.Close()
	}

	studiosQuery := fmt.Sprintf(`SELECT ist.item_id, st.id, st.name FROM item_studios ist JOIN studios st ON st.id = ist.studio_id WHERE ist.item_id IN (%s) ORDER BY ist.sort_order`, placeholders(len(ids)))
	sRows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(studiosQuery), args...)
	studiosMap := make(map[string][]NameIdPair)
	if err == nil {
		defer sRows.Close()
		for sRows.Next() {
			var itemID, stID, name string
			if sRows.Scan(&itemID, &stID, &name) == nil {
				studiosMap[itemID] = append(studiosMap[itemID], NameIdPair{Name: name, Id: stID})
			}
		}
		_ = sRows.Close()
	}

	tagsQuery := fmt.Sprintf(`SELECT it.item_id, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id IN (%s)`, placeholders(len(ids)))
	tRows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(tagsQuery), args...)
	tagsMap := make(map[string][]string)
	if err == nil {
		defer tRows.Close()
		for tRows.Next() {
			var itemID, name string
			if tRows.Scan(&itemID, &name) == nil {
				tagsMap[itemID] = append(tagsMap[itemID], name)
			}
		}
		_ = tRows.Close()
	}

	userStateMap := make(map[string]JellyfinUserData)
	resumableMap := make(map[string]bool)
	if userID != "" {
		uArgs := append([]any{userID}, args...)
		uQuery := fmt.Sprintf(`SELECT item_id, is_favorite, is_watched, playback_position_ticks, playback_runtime_ticks, user_rating FROM user_item_states WHERE user_id = ? AND item_id IN (%s)`, placeholders(len(ids)))
		uRows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(uQuery), uArgs...)
		if err == nil {
			defer uRows.Close()
			for uRows.Next() {
				var itemID string
				var fav, watched int
				var posTicks, runtimeTicks int64
				var rating *float64
				if uRows.Scan(&itemID, &fav, &watched, &posTicks, &runtimeTicks, &rating) == nil {
					resumableMap[itemID] = watched == 0 && posTicks > 0 && (runtimeTicks <= 0 || posTicks < runtimeTicks)
					userStateMap[itemID] = JellyfinUserData{
						PlaybackPositionTicks: posTicks,
						PlayCount:             watched,
						IsFavorite:            fav == 1,
						Played:                watched == 1,
						Key:                   itemID,
						ItemId:                itemID,
						Rating:                rating,
					}
				}
			}
			_ = uRows.Close()
		}
	}

	imgQuery := fmt.Sprintf(`SELECT item_id, image_type, id FROM item_images WHERE item_id IN (%s) AND status != 'error' ORDER BY sort_order`, placeholders(len(ids)))
	imgRows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(imgQuery), args...)
	imageTagsMap := make(map[string]map[string]string)
	backdropMap := make(map[string][]string)
	if err == nil {
		defer imgRows.Close()
		for imgRows.Next() {
			var itemID, imgType, imgID string
			if imgRows.Scan(&itemID, &imgType, &imgID) == nil {
				if imageTagsMap[itemID] == nil {
					imageTagsMap[itemID] = make(map[string]string)
				}
				key := map[string]string{"poster": "Primary", "backdrop": "Backdrop", "gallery": "Thumb", "preview": "Thumb", "avatar": "Primary"}[imgType]
				if key != "" {
					if _, exists := imageTagsMap[itemID][key]; !exists {
						imageTagsMap[itemID][key] = imgID
					}
				}
				if imgType == "backdrop" || imgType == "poster" {
					backdropMap[itemID] = append(backdropMap[itemID], imgID)
				}
			}
		}
		_ = imgRows.Close()
	}

	result := make([]BaseItemDto, 0, len(ids))
	aspectRatio := 0.7
	for _, id := range ids {
		it, ok := itemMap[id]
		if !ok {
			continue
		}
		tags := tagsMap[id]
		if tags == nil {
			tags = []string{}
		}
		people := peopleMap[id]
		if people == nil {
			people = []BaseItemPerson{}
		}
		studios := studiosMap[id]
		if studios == nil {
			studios = []NameIdPair{}
		}
		var providerIDs map[string]string
		_ = json.Unmarshal([]byte(it.idsJSON), &providerIDs)
		providers := mediaProviderIDs(it.source, it.code, it.sourceID, providerIDs)
		var runtimeTicks *int64
		if it.runtimeSeconds != nil {
			value := int64(*it.runtimeSeconds) * 10_000_000
			runtimeTicks = &value
		}
		userData, hasUserData := userStateMap[id]
		var uDataPtr *JellyfinUserData
		if hasUserData {
			uDataPtr = &userData
		} else {
			uDataPtr = &JellyfinUserData{Key: id, ItemId: id}
		}

		imgTags := imageTagsMap[id]
		if imgTags == nil {
			imgTags = make(map[string]string)
		}
		if _, ok := imgTags["Primary"]; !ok {
			if b, ok := imgTags["Backdrop"]; ok {
				imgTags["Primary"] = b
			} else if t, ok := imgTags["Thumb"]; ok {
				imgTags["Primary"] = t
			} else {
				imgTags["Primary"] = "1"
			}
		}
		bdTags := backdropMap[id]
		if len(bdTags) == 0 {
			if p, ok := imgTags["Primary"]; ok {
				bdTags = []string{p}
			} else {
				bdTags = []string{"1"}
			}
		}

		result = append(result, BaseItemDto{
			Name:              it.title,
			OriginalTitle:     it.origTitle,
			ServerId:          serverID,
			Id:                it.id,
			Etag:              it.updatedAt,
			DateCreated:       it.createdAt,
			SortName:          it.title,
			PremiereDate:      it.releaseDate,
			CommunityRating:   it.sourceRating,
			ProductionYear:    it.year,
			Overview:          it.overview,
			Genres:            tags,
			Tags:              tags,
			People:            people,
			Studios:           studios,
			ProviderIds:       providers,
			ImageTags:         imgTags,
			BackdropImageTags: bdTags,
			Type:              mediaType(it.kind),
			ParentId:          it.parentID, IndexNumber: it.index, IsFolder: it.kind == "series" || it.kind == "season",
			MediaType:               "Video",
			RunTimeTicks:            runtimeTicks,
			UserData:                uDataPtr,
			PrimaryImageAspectRatio: &aspectRatio,
			PlayAccess:              "Full",
			CanResume:               it.kind != "series" && it.kind != "season" && resumableMap[id],
		})
	}
	if err := s.enrichHierarchy(ctx, result, userID); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) image(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	itemID, rawType := unescapeParam(r, "itemID"), strings.ToLower(chi.URLParam(r, "imageType"))
	if !ok || principal.User.ID == "" {
		writeJellyfinError(w, 401, "请先登录")
		return
	}

	var actualLibrary string
	_ = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT id FROM virtual_libraries WHERE id=? OR id=? ORDER BY id LIMIT 1"), itemID, strings.TrimPrefix(itemID, "library-")).Scan(&actualLibrary)
	if actualLibrary != "" || itemID == "user-root" || itemID == "default" {
		if actualLibrary == "" {
			actualLibrary = "library-default"
		}
		libraryRule, libraryArgs := auth.LibraryVisibilitySQL("vl", principal.User.ID, isElevated(principal.User), false)
		var allowed int
		if s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM virtual_libraries vl WHERE vl.id=? AND ("+libraryRule+")"), append([]any{actualLibrary}, libraryArgs...)...).Scan(&allowed) != nil || allowed == 0 {
			writeJellyfinError(w, 404, "媒体库不可见")
			return
		}
		var firstItemID string
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", principal.User.ID, isElevated(principal.User), false)
		query := "SELECT vi.id FROM library_items li JOIN media_items vi ON vi.id=li.item_id JOIN virtual_libraries vl ON vl.id=li.library_id WHERE li.library_id=? AND (" + visibility + ") ORDER BY li.position,vi.id LIMIT 1"
		if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind(query), append([]any{actualLibrary}, accessArgs...)...).Scan(&firstItemID); err == nil && firstItemID != "" {
			if s.imageService != nil {
				data, mimeType, err := s.imageService.GetImageData(r.Context(), firstItemID, "poster")
				if err != nil || len(data) == 0 {
					data, mimeType, err = s.imageService.GetImageData(r.Context(), firstItemID, "backdrop")
				}
				if err == nil && len(data) > 0 {
					w.Header().Set("Content-Type", mimeType)
					w.Header().Set("Cache-Control", "private, no-store")
					w.Header().Set("Access-Control-Allow-Origin", "*")
					w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(data)
					return
				}
			}
		}
		// Fallback SVG folder icon
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="600" viewBox="0 0 400 600"><rect width="100%" height="100%" fill="#1a1c23"/><rect x="40" y="80" width="320" height="440" rx="16" fill="#2d3139"/><path d="M160 220 L240 280 L160 340 Z" fill="#3b82f6"/><text x="200" y="440" fill="#94a3b8" font-size="24" font-family="sans-serif" text-anchor="middle">JMlab</text></svg>`))
		return
	}

	// Handle actor/person images (e.g. people_...)
	if strings.HasPrefix(itemID, "people_") || strings.HasPrefix(itemID, "person_") {
		var personName string
		visibility, accessArgs := auth.MediaVisibilitySQL("vi", principal.User.ID, isElevated(principal.User), false)
		_ = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT p.name FROM people p WHERE p.id=? AND EXISTS (SELECT 1 FROM item_people ip JOIN media_items vi ON vi.id=ip.item_id WHERE ip.person_id=p.id AND ("+visibility+"))"), append([]any{itemID}, accessArgs...)...).Scan(&personName)
		if personName == "" {
			writeJellyfinError(w, 404, "人物不可见")
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="300" height="300" viewBox="0 0 300 300"><circle cx="150" cy="150" r="150" fill="#1e293b"/><circle cx="150" cy="115" r="55" fill="#475569"/><path d="M60 260 C60 200 240 200 240 260 Z" fill="#475569"/><text x="150" y="285" fill="#f8fafc" font-size="20" font-weight="bold" font-family="sans-serif" text-anchor="middle">%s</text></svg>`, html.EscapeString(personName))))
		return
	}

	if ok && principal.User.ID != "" {
		if _, err := s.catalog.GetItemForUser(r.Context(), itemID, principal.User); err != nil {
			writeJellyfinError(w, http.StatusNotFound, "作品不存在或不可见")
			return
		}
	} else {
		var exists int
		if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT 1 FROM media_items WHERE id = ? AND deleted_at IS NULL"), itemID).Scan(&exists); err != nil {
			writeJellyfinError(w, http.StatusNotFound, "作品不存在")
			return
		}
	}
	imageType := "poster"
	switch rawType {
	case "primary":
		imageType = "poster"
	case "backdrop":
		imageType = "backdrop"
	case "thumb", "gallery", "preview":
		imageType = "gallery"
	case "logo", "banner", "art":
		imageType = "backdrop"
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")

	if s.imageService != nil {
		data, mimeType, err := s.imageService.GetImageData(r.Context(), itemID, imageType)
		if (err != nil || len(data) == 0) && imageType == "poster" {
			data, mimeType, err = s.imageService.GetImageData(r.Context(), itemID, "backdrop")
		}
		if (err != nil || len(data) == 0) && imageType == "backdrop" {
			data, mimeType, err = s.imageService.GetImageData(r.Context(), itemID, "poster")
		}
		if err == nil && len(data) > 0 {
			w.Header().Set("Content-Type", mimeType)
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
	}

	var cacheKey string
	err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT cache_key FROM item_images WHERE item_id = ? AND image_type = ? AND status = 'ready' ORDER BY sort_order LIMIT 1"), itemID, imageType).Scan(&cacheKey)
	if errors.Is(err, sql.ErrNoRows) && imageType == "poster" {
		err = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT cache_key FROM item_images WHERE item_id = ? AND image_type = 'backdrop' AND status = 'ready' ORDER BY sort_order LIMIT 1"), itemID).Scan(&cacheKey)
	}
	if err == nil {
		path := filepath.Join(s.cfg.DataDir, "cache", "images", cacheKey)
		file, openErr := os.Open(path)
		if openErr == nil {
			defer file.Close()
			header := make([]byte, 512)
			read, _ := file.Read(header)
			_, _ = file.Seek(0, 0)
			w.Header().Set("Content-Type", http.DetectContentType(header[:read]))
			w.Header().Set("Cache-Control", "private, no-store")
			http.ServeContent(w, r, cacheKey, time.Time{}, file)
			return
		}
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="600" viewBox="0 0 400 600"><rect width="100%%" height="100%%" fill="#18181b"/><rect x="20" y="20" width="360" height="560" rx="12" fill="#27272a"/><text x="200" y="300" fill="#a1a1aa" font-size="20" font-family="sans-serif" text-anchor="middle">%s</text></svg>`, html.EscapeString(itemID))))
}

func (s *Service) getUserData(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	itemID := unescapeParam(r, "itemID")
	if _, err := s.catalog.GetItemForUser(r.Context(), itemID, principal.User); err != nil {
		writeJellyfinError(w, http.StatusNotFound, "作品不存在或不可见")
		return
	}
	state, _ := s.userData.GetItemState(r.Context(), principal.User.ID, itemID)
	writeJSON(w, http.StatusOK, toJellyfinUserData(state))
}

func (s *Service) postUserData(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	itemID := unescapeParam(r, "itemID")
	if _, err := s.catalog.GetItemForUser(r.Context(), itemID, principal.User); err != nil {
		writeJellyfinError(w, http.StatusNotFound, "作品不存在或不可见")
		return
	}
	var input struct {
		IsFavorite            *bool    `json:"IsFavorite"`
		Played                *bool    `json:"Played"`
		PlaybackPositionTicks *int64   `json:"PlaybackPositionTicks"`
		Rating                *float64 `json:"Rating"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeJellyfinError(w, http.StatusBadRequest, "用户状态格式不正确")
		return
	}
	current, err := s.userData.GetItemState(r.Context(), principal.User.ID, itemID)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取用户状态失败")
		return
	}
	var rating *int
	if input.Rating != nil {
		value := int(*input.Rating + 0.5)
		if value < 0 || value > 10 {
			writeJellyfinError(w, http.StatusBadRequest, "评分必须在 0 到 10 之间")
			return
		}
		rating = &value
	}
	state, err := s.userData.UpsertItemState(r.Context(), principal.User.ID, principal.SessionID, itemID, userdata.StateInput{
		Favorite: input.IsFavorite, Watched: input.Played, PositionTicks: input.PlaybackPositionTicks,
		Rating: rating, Version: &current.Version,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "冲突") {
			status = http.StatusConflict
		}
		writeJellyfinError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toJellyfinUserData(state))
}

type jellyfinPlaybackEvent struct {
	ItemID        string `json:"ItemId"`
	PositionTicks *int64 `json:"PositionTicks"`
	RunTimeTicks  *int64 `json:"RunTimeTicks"`
	PlaySessionID string `json:"PlaySessionId"`
	DeviceID      string `json:"DeviceId"`
	DeviceName    string `json:"DeviceName"`
	Client        string `json:"Client"`
	IsPaused      bool   `json:"IsPaused"`
	Version       *int   `json:"Version"`
	EventAt       string `json:"EventAt"`
}

func (s *Service) playing(w http.ResponseWriter, r *http.Request) {
	s.handlePlaybackEvent(w, r, "playback_start")
}

func (s *Service) playingProgress(w http.ResponseWriter, r *http.Request) {
	s.handlePlaybackEvent(w, r, "progress")
}

func (s *Service) playingStopped(w http.ResponseWriter, r *http.Request) {
	s.handlePlaybackEvent(w, r, "playback_stop")
}

func (s *Service) handlePlaybackEvent(w http.ResponseWriter, r *http.Request, eventType string) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input jellyfinPlaybackEvent
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&input); err != nil {
		writeJellyfinError(w, http.StatusBadRequest, "播放事件格式不正确")
		return
	}
	input.ItemID = strings.TrimSpace(input.ItemID)
	if input.ItemID == "" {
		writeJellyfinError(w, http.StatusBadRequest, "ItemId 不能为空")
		return
	}
	positionTicks := int64(0)
	if input.PositionTicks != nil {
		positionTicks = *input.PositionTicks
	}
	runtimeTicks := int64(0)
	if input.RunTimeTicks != nil {
		runtimeTicks = *input.RunTimeTicks
	}
	if positionTicks < 0 || runtimeTicks < 0 {
		writeJellyfinError(w, http.StatusBadRequest, "播放进度不能为负数")
		return
	}
	if _, err := s.catalog.GetItemForUser(r.Context(), input.ItemID, principal.User); err != nil {
		writeJellyfinError(w, http.StatusNotFound, "作品不存在或不可见")
		return
	}

	_, device, deviceID := clientMetadata(r)
	if strings.TrimSpace(input.DeviceName) != "" && r.Header.Get("X-Emby-Device-Name") == "" {
		device = strings.TrimSpace(input.DeviceName)
	}
	if strings.TrimSpace(input.DeviceID) != "" && r.Header.Get("X-Emby-Device-Id") == "" {
		deviceID = strings.TrimSpace(input.DeviceID)
	}
	eventAt := time.Now().UTC()
	if strings.TrimSpace(input.EventAt) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.EventAt))
		if err != nil {
			writeJellyfinError(w, http.StatusBadRequest, "EventAt 必须是 RFC3339 时间")
			return
		}
		eventAt = parsed
	}
	_, err := s.userData.UpsertPlayback(r.Context(), principal.User.ID, input.ItemID, userdata.PlaybackInput{
		EventType: eventType, SessionID: principal.SessionID, PlaybackSessionID: input.PlaySessionID,
		DeviceID: deviceID, DeviceName: device, Paused: input.IsPaused, PositionTicks: positionTicks,
		RuntimeTicks: runtimeTicks, Version: input.Version, EventAt: eventAt,
	})
	if err != nil {
		if strings.Contains(err.Error(), "版本冲突") {
			writeJellyfinError(w, http.StatusConflict, err.Error())
			return
		}
		writeJellyfinError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) getPlayedItem(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	state, err := s.userData.GetItemState(r.Context(), principal.User.ID, unescapeParam(r, "itemID"))
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取观看状态失败")
		return
	}
	if !state.IsWatched {
		writeJellyfinError(w, http.StatusNotFound, "作品未标记为已看")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) markPlayed(w http.ResponseWriter, r *http.Request)   { s.setWatched(w, r, true) }
func (s *Service) markUnplayed(w http.ResponseWriter, r *http.Request) { s.setWatched(w, r, false) }

func (s *Service) setWatched(w http.ResponseWriter, r *http.Request, watched bool) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	itemID := unescapeParam(r, "itemID")
	current, err := s.userData.GetItemState(r.Context(), principal.User.ID, itemID)
	if err == nil {
		_, err = s.userData.UpsertItemState(r.Context(), principal.User.ID, principal.SessionID, itemID, userdata.StateInput{Watched: &watched, Version: &current.Version})
	}
	if err != nil {
		writeJellyfinError(w, http.StatusBadRequest, "更新观看状态失败")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) markFavorite(w http.ResponseWriter, r *http.Request)   { s.setFavorite(w, r, true) }
func (s *Service) markUnfavorite(w http.ResponseWriter, r *http.Request) { s.setFavorite(w, r, false) }

func (s *Service) setFavorite(w http.ResponseWriter, r *http.Request, favorite bool) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	itemID := unescapeParam(r, "itemID")
	current, err := s.userData.GetItemState(r.Context(), principal.User.ID, itemID)
	if err == nil {
		_, err = s.userData.UpsertItemState(r.Context(), principal.User.ID, principal.SessionID, itemID, userdata.StateInput{Favorite: &favorite, Version: &current.Version})
	}
	if err != nil {
		writeJellyfinError(w, http.StatusBadRequest, "更新收藏状态失败")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) listPlaylists(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT id, name, COALESCE(overview, ''), owner_user_id, updated_at FROM playlists WHERE owner_user_id = ? OR is_public = 1 ORDER BY updated_at DESC"), principal.User.ID)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取播放列表失败")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, overview, owner, updated string
		if rows.Scan(&id, &name, &overview, &owner, &updated) != nil {
			continue
		}
		items = append(items, map[string]any{"Name": name, "Id": id, "ServerId": serverID, "Type": "Playlist", "Overview": overview, "UserId": owner, "DateCreated": updated, "MediaType": "Video", "IsFolder": true})
	}
	writeQueryResult(w, items, len(items))
}

func (s *Service) createPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	name := strings.TrimSpace(firstValue(r.URL.Query(), "Name", "name"))
	if name == "" && r.Body != nil {
		var input struct {
			Name string `json:"Name"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input)
		name = strings.TrimSpace(input.Name)
	}
	if name == "" {
		writeJellyfinError(w, http.StatusBadRequest, "播放列表名称不能为空")
		return
	}
	playlistID := newID("pl")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO playlists (id, owner_user_id, name, is_public, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)"), playlistID, principal.User.ID, name, now, now)
	if err != nil {
		writeJellyfinError(w, http.StatusBadRequest, "创建播放列表失败")
		return
	}
	if ids := splitValues(firstValue(r.URL.Query(), "Ids", "ids")); len(ids) > 0 {
		if err := s.addItemsToPlaylist(r.Context(), principal.User.ID, playlistID, ids); err != nil {
			writeJellyfinError(w, http.StatusBadRequest, "添加播放列表内容失败")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"Id": playlistID, "Name": name, "ServerId": serverID, "Type": "Playlist"})
}

func (s *Service) playlistItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	playlistID := chi.URLParam(r, "playlistID")
	if !s.canReadPlaylist(r.Context(), principal.User.ID, playlistID) {
		writeJellyfinError(w, http.StatusNotFound, "播放列表不存在")
		return
	}
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT item_id FROM playlist_items WHERE playlist_id = ? ORDER BY position"), playlistID)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取播放列表内容失败")
		return
	}
	itemIDs := make([]string, 0)
	for rows.Next() {
		var itemID string
		if rows.Scan(&itemID) != nil {
			continue
		}
		itemIDs = append(itemIDs, itemID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeJellyfinError(w, http.StatusInternalServerError, "读取播放列表内容失败")
		return
	}
	// SQLite may use the same connection for the detail query. Close the
	// playlist result set before loading item DTOs to avoid connection waits.
	rows.Close()
	items, err := s.batchBaseItems(r.Context(), itemIDs, principal.User.ID)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取列表内容失败")
		return
	}
	writeQueryResult(w, items, len(items))
}

func (s *Service) addPlaylistItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	playlistID := chi.URLParam(r, "playlistID")
	if !s.canEditPlaylist(r.Context(), principal.User.ID, playlistID) {
		writeJellyfinError(w, http.StatusNotFound, "播放列表不存在")
		return
	}
	ids := splitValues(firstValue(r.URL.Query(), "Ids", "ids"))
	if len(ids) == 0 {
		var input struct {
			Ids []string `json:"Ids"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input)
		ids = input.Ids
	}
	if len(ids) == 0 {
		writeJellyfinError(w, http.StatusBadRequest, "Ids 不能为空")
		return
	}
	if err := s.addItemsToPlaylist(r.Context(), principal.User.ID, playlistID, ids); err != nil {
		writeJellyfinError(w, http.StatusBadRequest, "添加播放列表内容失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) addItemsToPlaylist(ctx context.Context, userID, playlistID string, ids []string) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, itemID := range ids {
		itemID = strings.TrimSpace(itemID)
		if itemID == "" {
			continue
		}
		var position int
		if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COALESCE(MAX(position), -1) + 1 FROM playlist_items WHERE playlist_id = ?"), playlistID).Scan(&position); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO playlist_items (playlist_id, item_id, position, added_by_user_id, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(playlist_id, item_id) DO NOTHING"), playlistID, itemID, position, userID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) removePlaylistItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	playlistID := chi.URLParam(r, "playlistID")
	if !s.canEditPlaylist(r.Context(), principal.User.ID, playlistID) {
		writeJellyfinError(w, http.StatusNotFound, "播放列表不存在")
		return
	}
	ids := splitValues(firstValue(r.URL.Query(), "Ids", "ids", "EntryIds", "entryIds"))
	if len(ids) == 0 {
		writeJellyfinError(w, http.StatusBadRequest, "Ids 不能为空")
		return
	}
	for _, itemID := range ids {
		_, _ = s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM playlist_items WHERE playlist_id = ? AND item_id = ?"), playlistID, itemID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) canReadPlaylist(ctx context.Context, userID, playlistID string) bool {
	var count int
	return s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM playlists WHERE id = ? AND (owner_user_id = ? OR is_public = 1)"), playlistID, userID).Scan(&count) == nil && count == 1
}

func (s *Service) canEditPlaylist(ctx context.Context, userID, playlistID string) bool {
	var count int
	return s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM playlists WHERE id = ? AND owner_user_id = ?"), playlistID, userID).Scan(&count) == nil && count == 1
}

func (s *Service) ownsUserRoute(w http.ResponseWriter, r *http.Request, currentUserID string) bool {
	requestedUserID := chi.URLParam(r, "userID")
	if requestedUserID == "" || requestedUserID == currentUserID || strings.EqualFold(requestedUserID, "me") {
		return true
	}
	writeJellyfinError(w, http.StatusForbidden, "不能访问其他用户的数据")
	return false
}

func toJellyfinUserData(state userdata.ItemState) JellyfinUserData {
	data := JellyfinUserData{PlaybackPositionTicks: state.PositionTicks, IsFavorite: state.IsFavorite, Played: state.IsWatched, Key: state.ItemID, ItemId: state.ItemID}
	if state.IsWatched {
		data.PlayCount = 1
	}
	if state.UserRating != nil {
		rating := float64(*state.UserRating)
		data.Rating = &rating
	}
	return data
}

func clientMetadata(r *http.Request) (client, device, deviceID string) {
	client, device, deviceID = r.Header.Get("X-Emby-Client"), r.Header.Get("X-Emby-Device-Name"), r.Header.Get("X-Emby-Device-Id")
	if client == "" {
		client = "JMlab Client"
	}
	if device == "" {
		device = r.Header.Get("User-Agent")
	}
	header := r.Header.Get("X-Emby-Authorization")
	if header == "" {
		header = r.Header.Get("Authorization")
	}
	for _, match := range mediaBrowserAttribute.FindAllStringSubmatch(header, -1) {
		switch strings.ToLower(match[1]) {
		case "client":
			client = match[2]
		case "device":
			device = match[2]
		case "deviceid":
			deviceID = match[2]
		}
	}
	return
}

func clientVersion(r *http.Request) string {
	if value := r.Header.Get("X-Emby-Client-Version"); value != "" {
		return value
	}
	for _, match := range mediaBrowserAttribute.FindAllStringSubmatch(r.Header.Get("X-Emby-Authorization"), -1) {
		if strings.EqualFold(match[1], "Version") {
			return match[2]
		}
	}
	return ""
}

func isElevated(user auth.User) bool {
	for _, role := range user.Roles {
		if role == "Admin" || role == "Editor" {
			return true
		}
	}
	return false
}

func writeQueryResult(w http.ResponseWriter, items any, count int) {
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": count, "StartIndex": 0})
}

func writeJellyfinError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"Message": message, "Code": status})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func firstValue(values map[string][]string, keys ...string) string {
	for _, key := range keys {
		if valuesForKey := values[key]; len(valuesForKey) > 0 {
			if value := strings.TrimSpace(valuesForKey[0]); value != "" {
				return value
			}
		}
	}
	return ""
}

func splitValues(raw string) []string {
	values := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '|' })
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func parseNonNegative(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func runtimeTicks(seconds *int) *int64 {
	if seconds == nil {
		return nil
	}
	value := int64(*seconds) * 10_000_000
	return &value
}

func newID(prefix string) string { return fmt.Sprintf("%s_%x", prefix, time.Now().UTC().UnixNano()) }

func (s *Service) publicUsers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Service) brandingConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"LoginDisclaimer":     "",
		"CustomCss":           "",
		"SplashscreenEnabled": false,
	})
}

func (s *Service) brandingCss(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

func (s *Service) quickConnectEnabled(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, false)
}

func (s *Service) systemConfiguration(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ServerName":               "JMlab",
		"UICulture":                "zh-CN",
		"IsStartupWizardCompleted": true,
	})
}

func (s *Service) startupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"IsStartupWizardCompleted": true,
	})
}

func (s *Service) startupConfiguration(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"UICulture":                 "zh-CN",
		"MetadataCountryCode":       "CN",
		"PreferredMetadataLanguage": "zh",
	})
}

func (s *Service) startupUser(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Name": "admin",
	})
}

func (s *Service) startupComplete(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) authProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{
		map[string]any{
			"Name":        "Default",
			"DisplayName": "Default",
			"IsEnabled":   true,
		},
	})
}

func (s *Service) authKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Service) localizationOptions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Service) localizationCultures(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]string{
		{"Name": "zh-CN", "DisplayName": "Chinese (Simplified)", "TwoLetterISOLanguageName": "zh", "ThreeLetterISOLanguageName": "zho"},
		{"Name": "en-US", "DisplayName": "English (United States)", "TwoLetterISOLanguageName": "en", "ThreeLetterISOLanguageName": "eng"},
		{"Name": "ja-JP", "DisplayName": "Japanese", "TwoLetterISOLanguageName": "ja", "ThreeLetterISOLanguageName": "jpn"},
	})
}

func (s *Service) localizationParentalRatings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Service) defaultDirectoryBrowser(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Path":       "/data",
		"SubFolders": []any{},
	})
}

func (s *Service) getDisplayPreferences(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "displayPreferencesId")
	writeJSON(w, http.StatusOK, map[string]any{
		"Id":                      id,
		"ViewType":                "Movies",
		"SortBy":                  "SortName",
		"IndexBy":                 "None",
		"RememberIndexing":        false,
		"PrimaryImageAspectRatio": 0.67,
		"CustomPrefs":             map[string]string{},
		"ScrollDirection":         "Vertical",
		"ShowBackdrop":            true,
		"RememberSorting":         false,
		"SortOrder":               "Ascending",
		"ShowSidebar":             true,
		"Client":                  "Jellyfin",
	})
}

func (s *Service) saveDisplayPreferences(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) sessionCapabilities(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) sessions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Service) logout(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) itemCounts(w http.ResponseWriter, r *http.Request) {
	var count int
	_ = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM media_items WHERE deleted_at IS NULL")).Scan(&count)
	writeJSON(w, http.StatusOK, map[string]any{
		"MovieCount":      count,
		"SeriesCount":     0,
		"EpisodeCount":    0,
		"ArtistCount":     0,
		"ProgramCount":    0,
		"TrailerCount":    0,
		"SongCount":       0,
		"AlbumCount":      0,
		"MusicVideoCount": 0,
		"BoxSetCount":     0,
		"BookCount":       0,
		"ItemCount":       count,
	})
}
