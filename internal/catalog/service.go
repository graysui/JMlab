package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/entities"
	"github.com/graysui/JMlab/internal/metadata"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

type Service struct {
	libraryPolicyMu sync.Mutex
	events          *module.EventBus
	modules         *module.Registry
	db              *repository.Database
	authService     *auth.Service
	logger          *slog.Logger
	adapter         metadata.SourceAdapter
	imageImporter   func(context.Context, string, string, string, string, int, int) error
	sourceSeeder    func(context.Context, Item) error
	workerCancel    context.CancelFunc
	workerDone      chan struct{}
}

type ImportRequest struct {
	Items []ImportItem `json:"items"`
}
type ImportItem struct {
	Code            string          `json:"code"`
	RawCode         string          `json:"rawCode"`
	Source          string          `json:"source"`
	SourceID        string          `json:"sourceId"`
	Title           string          `json:"title"`
	TranslatedTitle string          `json:"translatedTitle"`
	OriginalTitle   string          `json:"originalTitle"`
	Overview        string          `json:"overview"`
	ReleaseDate     string          `json:"releaseDate"`
	Year            *int            `json:"year"`
	RuntimeSeconds  *int            `json:"runtimeSeconds"`
	SourceRating    *float64        `json:"sourceRating"`
	RatingCount     int             `json:"ratingCount"`
	SourceURL       string          `json:"sourceUrl"`
	IsAdult         *bool           `json:"isAdult"`
	People          []RelationInput `json:"people"`
	Studios         []RelationInput `json:"studios"`
	Series          []RelationInput `json:"series"`
	Tags            []RelationInput `json:"tags"`
}
type RelationInput struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}
type Item struct {
	ItemType         string            `json:"itemType"`
	ParentID         string            `json:"parentId,omitempty"`
	IndexNumber      *int              `json:"indexNumber,omitempty"`
	ProviderIDs      map[string]string `json:"providerIds,omitempty"`
	ExtraAttributes  map[string]any    `json:"extraAttributes,omitempty"`
	MetadataModuleID string            `json:"metadataModuleId,omitempty"`
	ID               string            `json:"id"`
	Source           string            `json:"source"`
	SourceID         string            `json:"sourceId,omitempty"`
	Code             string            `json:"code"`
	RawCode          string            `json:"rawCode"`
	Title            string            `json:"title"`
	TranslatedTitle  string            `json:"translatedTitle,omitempty"`
	OriginalTitle    string            `json:"originalTitle,omitempty"`
	Overview         string            `json:"overview,omitempty"`
	ReleaseDate      string            `json:"releaseDate,omitempty"`
	Year             *int              `json:"year,omitempty"`
	RuntimeSeconds   *int              `json:"runtimeSeconds,omitempty"`
	SourceRating     *float64          `json:"sourceRating,omitempty"`
	RatingCount      int               `json:"ratingCount"`
	SourceURL        string            `json:"sourceUrl"`
	MetadataStatus   string            `json:"metadataStatus"`
	IsAdult          bool              `json:"isAdult"`
	CreatedAt        string            `json:"createdAt"`
	UpdatedAt        string            `json:"updatedAt"`
	People           []Relation        `json:"people,omitempty"`
	Studios          []Relation        `json:"studios,omitempty"`
	Series           []Relation        `json:"series,omitempty"`
	Tags             []Relation        `json:"tags,omitempty"`
}
type Relation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type SearchHint struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Label    string `json:"label"`
	Code     string `json:"code,omitempty"`
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
}

type Library struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	ItemType      string              `json:"itemType"`
	Filter        any                 `json:"filter"`
	Sort          LibrarySort         `json:"sort"`
	PosterImageID string              `json:"posterImageId,omitempty"`
	IsPublic      bool                `json:"isPublic"`
	IsAdult       bool                `json:"isAdult"`
	Enabled       bool                `json:"enabled"`
	Permissions   []LibraryPermission `json:"permissions,omitempty"`
}

func NewService(db *repository.Database, authService *auth.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, authService: authService, logger: logger}
}

func (s *Service) RegisterRoutes(router chi.Router) {
	s.registerModuleRankingRoutes(router)
	s.registerCollectionRoutes(router)
	s.registerMetadataRoutes(router)
	router.With(s.authService.RequireAuth).Get("/libraries", s.listLibraries)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/libraries", s.createLibrary)
	router.With(s.authService.RequireAuth).Get("/libraries/{libraryID}", s.getLibrary)
	router.With(s.authService.RequireAuth).Get("/libraries/{libraryID}/items", s.listLibraryItems)
	router.With(s.authService.RequireAuth).Get("/libraries/{libraryID}/preview", s.previewLibrary)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin")).Get("/libraries/{libraryID}/permissions", s.getLibraryPermissions)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Put("/libraries/{libraryID}/permissions", s.setLibraryPermissions)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Patch("/libraries/{libraryID}", s.updateLibrary)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Delete("/libraries/{libraryID}", s.deleteLibrary)
	router.With(s.authService.RequireAuth).Get("/items", s.list)
	router.With(s.authService.RequireAuth).Get("/search/hints", s.searchHints)
	router.With(s.authService.RequireAuth).Get("/items/{itemID}", s.detail)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/items/import", s.importItems)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/rankings/generate", s.generateRanking)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/admin/rankings/generate", s.generateRanking)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin")).Get("/rankings/schedule", s.getRankingSchedule)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin")).Get("/admin/rankings/schedule", s.getRankingSchedule)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Put("/rankings/schedule", s.updateRankingSchedule)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Put("/admin/rankings/schedule", s.updateRankingSchedule)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/rankings/schedule/trigger", s.triggerRankingSchedule)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/admin/rankings/schedule/trigger", s.triggerRankingSchedule)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Patch("/items/{itemID}", s.update)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/items/{itemID}/sync", s.sync)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/items/{itemID}/lock-fields", s.lockFields)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor")).Get("/tasks", s.listTasks)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor")).Get("/tasks/{taskID}", s.getTask)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/tasks/{taskID}/cancel", s.cancelTask)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/tasks/{taskID}/retry", s.retryTask)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor")).Get("/audit-logs", s.listAuditLogs)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor")).Get("/logs", s.listAuditLogs)
}

func (s *Service) listLibraries(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	query := "SELECT DISTINCT vl.id, vl.name, COALESCE(vl.description, ''), vl.item_type, vl.filter_json, vl.sort_json, COALESCE(vl.poster_image_id, ''), vl.is_public, vl.is_adult, vl.enabled FROM virtual_libraries vl"
	args := []any{}
	if !isAdmin(principal.User) {
		query += ` WHERE vl.enabled = 1 AND (vl.is_public = 1 OR EXISTS (
SELECT 1 FROM user_library_permissions ulp WHERE ulp.library_id = vl.id AND ulp.user_id = ? AND ulp.can_view = 1
) OR EXISTS (
SELECT 1 FROM user_roles ur JOIN role_library_permissions rlp ON rlp.role_id = ur.role_id
WHERE ur.user_id = ? AND rlp.library_id = vl.id AND rlp.can_view = 1
)) AND (vl.is_adult = 0 OR EXISTS (
SELECT 1 FROM user_preferences up WHERE up.user_id = ? AND up.adult_confirmed_at IS NOT NULL AND up.adult_confirmed_at <> ''
))`
		args = append(args, principal.User.ID, principal.User.ID, principal.User.ID)
	} else if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("includeDisabled")), "true") || r.URL.Query().Get("includeDisabled") == "1" {
		query += " WHERE 1 = 1"
	} else {
		query += " WHERE vl.enabled = 1"
	}
	query += " ORDER BY vl.name"
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARIES_READ_FAILED", "读取媒体库失败")
		return
	}
	defer rows.Close()
	libraries := make([]Library, 0)
	for rows.Next() {
		library, err := s.libraryFromRow(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "LIBRARIES_READ_FAILED", "读取媒体库失败")
			return
		}
		libraries = append(libraries, library)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARIES_READ_FAILED", "读取媒体库失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": libraries})
}

func (s *Service) createLibrary(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input struct {
		Name          string                    `json:"name"`
		Description   string                    `json:"description"`
		ItemType      string                    `json:"itemType"`
		Filter        json.RawMessage           `json:"filter"`
		FilterJSON    json.RawMessage           `json:"filterJson"`
		Sort          json.RawMessage           `json:"sort"`
		SortJSON      json.RawMessage           `json:"sortJson"`
		PosterImageID *string                   `json:"posterImageId"`
		IsPublic      *bool                     `json:"isPublic"`
		IsAdult       *bool                     `json:"isAdult"`
		Enabled       *bool                     `json:"enabled"`
		Template      *libraryTemplateSelection `json:"template"`
	}
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.Name) == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY", "媒体库名称不能为空")
		return
	}
	itemType := strings.TrimSpace(input.ItemType)
	if itemType == "" {
		itemType = "movie"
	}
	if itemType != "movie" && itemType != "collection" && itemType != "people" && itemType != "mixed" {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY", "itemType 只能是 movie、collection、people 或 mixed")
		return
	}
	filterRaw := input.Filter
	if len(filterRaw) == 0 {
		filterRaw = input.FilterJSON
	}
	filter, filterJSON, _, err := parseLibraryFilter(filterRaw)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY_FILTER", err.Error())
		return
	}
	sort, sortJSON, err := parseLibrarySort(firstRaw(input.Sort, input.SortJSON))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY_SORT", err.Error())
		return
	}
	id := newID("lib")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	adult := true
	if input.IsAdult != nil {
		adult = *input.IsAdult
	}
	public := false
	if input.IsPublic != nil {
		public = *input.IsPublic
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	posterImageID := nullable("")
	if input.PosterImageID != nil {
		posterImageID = nullable(*input.PosterImageID)
	}
	persist := func() error {
		_, saveErr := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO virtual_libraries (id, name, description, item_type, filter_json, sort_json, poster_image_id, is_public, is_adult, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), itemType, filterJSON, sortJSON, posterImageID, boolInt(public), boolInt(adult), boolInt(enabled), now, now)
		return saveErr
	}
	if input.Template != nil {
		if err = s.checkLibraryTemplate(input.Template); err != nil {
			writeError(w, r, http.StatusConflict, "LIBRARY_TEMPLATE_CHANGED", err.Error())
			return
		}
		err = s.modules.WithActiveRevision(r.Context(), input.Template.ModuleID, input.Template.Revision, persist)
		if errors.Is(err, module.ErrRevisionChanged) {
			writeError(w, r, http.StatusConflict, "LIBRARY_TEMPLATE_CHANGED", "模块模板已更新或停用，请重新选择模块和模板")
			return
		}
	} else {
		err = persist()
	}
	if err != nil {
		writeError(w, r, http.StatusConflict, "LIBRARY_CREATE_FAILED", "创建媒体库失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "create_library", "library", id, middleware.GetReqID(r.Context()), "{}")
	if err := s.SyncLibraryMemberships(r.Context(), id, ""); err != nil {
		writeError(w, r, 500, "LIBRARY_MEMBERSHIP_FAILED", "媒体库已保存，暂时无法更新内容范围，请重新保存")
		return
	}
	writeJSON(w, http.StatusCreated, Library{ID: id, Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description), ItemType: itemType, Filter: filter, Sort: sort, PosterImageID: strings.TrimSpace(stringValue(input.PosterImageID)), IsPublic: public, IsAdult: adult, Enabled: enabled})
}

func (s *Service) updateLibrary(w http.ResponseWriter, r *http.Request) {
	s.libraryPolicyMu.Lock()
	defer s.libraryPolicyMu.Unlock()
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input struct {
		Name          *string         `json:"name"`
		Description   *string         `json:"description"`
		ItemType      *string         `json:"itemType"`
		Filter        json.RawMessage `json:"filter"`
		FilterJSON    json.RawMessage `json:"filterJson"`
		Sort          json.RawMessage `json:"sort"`
		SortJSON      json.RawMessage `json:"sortJson"`
		PosterImageID *string         `json:"posterImageId"`
		IsPublic      *bool           `json:"isPublic"`
		IsAdult       *bool           `json:"isAdult"`
		Enabled       *bool           `json:"enabled"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY", "媒体库更新格式不正确")
		return
	}
	updates := make([]string, 0, 10)
	args := make([]any, 0, 12)
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" || len([]rune(strings.TrimSpace(*input.Name))) > 200 {
			writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY", "媒体库名称必须为 1 到 200 个字符")
			return
		}
		updates = append(updates, "name = ?")
		args = append(args, strings.TrimSpace(*input.Name))
	}
	if input.Description != nil {
		if len([]rune(*input.Description)) > 2000 {
			writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY", "媒体库说明不能超过 2000 个字符")
			return
		}
		updates = append(updates, "description = ?")
		args = append(args, strings.TrimSpace(*input.Description))
	}
	if input.ItemType != nil {
		itemType := strings.TrimSpace(*input.ItemType)
		if itemType != "movie" && itemType != "collection" && itemType != "people" && itemType != "mixed" {
			writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY", "itemType 只能是 movie、collection、people 或 mixed")
			return
		}
		updates = append(updates, "item_type = ?")
		args = append(args, itemType)
	}
	if len(input.Filter) > 0 || len(input.FilterJSON) > 0 {
		filterRaw := input.Filter
		if len(filterRaw) == 0 {
			filterRaw = input.FilterJSON
		}
		_, filterJSON, _, err := parseLibraryFilter(filterRaw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY_FILTER", err.Error())
			return
		}
		if err := s.checkPlaybackLibraryChange(chi.URLParam(r, "libraryID"), json.RawMessage(filterJSON)); err != nil {
			writeError(w, r, 400, "LIBRARY_USED_BY_STREAM_MODULE", err.Error())
			return
		}
		updates = append(updates, "filter_json = ?")
		args = append(args, filterJSON)
	}
	if len(input.Sort) > 0 || len(input.SortJSON) > 0 {
		sortRaw := firstRaw(input.Sort, input.SortJSON)
		_, sortJSON, err := parseLibrarySort(sortRaw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY_SORT", err.Error())
			return
		}
		updates = append(updates, "sort_json = ?")
		args = append(args, sortJSON)
	}
	if input.PosterImageID != nil {
		updates = append(updates, "poster_image_id = ?")
		args = append(args, nullable(*input.PosterImageID))
	}
	if input.IsPublic != nil {
		updates = append(updates, "is_public = ?")
		args = append(args, boolInt(*input.IsPublic))
	}
	if input.IsAdult != nil {
		updates = append(updates, "is_adult = ?")
		args = append(args, boolInt(*input.IsAdult))
	}
	if input.Enabled != nil {
		updates = append(updates, "enabled = ?")
		args = append(args, boolInt(*input.Enabled))
	}
	if len(updates) == 0 {
		writeError(w, r, http.StatusBadRequest, "NO_CHANGES", "没有要更新的字段")
		return
	}
	updates = append(updates, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano), chi.URLParam(r, "libraryID"))
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE virtual_libraries SET "+strings.Join(updates, ", ")+" WHERE id = ?"), args...)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "LIBRARY_UPDATE_FAILED", "更新媒体库失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "媒体库不存在")
		return
	}
	s.audit(r.Context(), principal.User.ID, "update_library", "library", chi.URLParam(r, "libraryID"), middleware.GetReqID(r.Context()), "{}")
	if err := s.SyncLibraryMemberships(r.Context(), chi.URLParam(r, "libraryID"), ""); err != nil {
		writeError(w, r, 500, "LIBRARY_MEMBERSHIP_FAILED", "媒体库已保存，暂时无法更新内容范围，请重新保存")
		return
	}
	updated, found, readErr := s.libraryVisible(r.Context(), chi.URLParam(r, "libraryID"), principal.User, false)
	if readErr != nil || !found {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_READ_FAILED", "读取更新后的媒体库失败")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Service) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	libraryID := chi.URLParam(r, "libraryID")
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM virtual_libraries WHERE id = ?"), libraryID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "LIBRARY_DELETE_FAILED", "删除媒体库失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "媒体库不存在")
		return
	}
	s.audit(r.Context(), principal.User.ID, "delete_library", "library", libraryID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) EnsureDefaultLibrary(ctx context.Context, adminUserID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO virtual_libraries (id, name, description, item_type, filter_json, sort_json, is_public, is_adult, enabled, created_at, updated_at) VALUES ('library-default', '全部作品', '全部媒体内容', 'movie', '{}', '{}', 1, 0, 1, ?, ?) ON CONFLICT(id) DO NOTHING"), now, now)
	if err != nil {
		return err
	}

	// Rename legacy library-ranking-ranking to library-ranking-daily if present
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE virtual_libraries SET id = 'library-ranking-daily', name = '排行榜日榜' WHERE id = 'library-ranking-ranking'"))
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE library_items SET library_id = 'library-ranking-daily' WHERE library_id = 'library-ranking-ranking'"))
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE user_library_permissions SET library_id = 'library-ranking-daily' WHERE library_id = 'library-ranking-ranking'"))

	definitions := []struct {
		id, name, description, rankingType, period string
	}{
		{id: "library-ranking-daily", name: "排行榜日榜", description: "JavDB 排行日榜当前快照", rankingType: "ranking", period: "daily"},
		{id: "library-ranking-weekly", name: "排行榜周榜", description: "JavDB 排行周榜当前快照", rankingType: "ranking", period: "weekly"},
		{id: "library-ranking-monthly", name: "排行榜月榜", description: "JavDB 排行月榜当前快照", rankingType: "ranking", period: "monthly"},
		{id: "library-ranking-hot", name: "热播日榜", description: "JavDB 热播日榜当前快照", rankingType: "playback", period: "daily"},
		{id: "library-ranking-top250", name: "TOP 250", description: "JavDB TOP 250 历史总榜", rankingType: "top250", period: "all"},
		{id: "library-ranking-2026", name: "2026年榜", description: "JavDB 2026年度榜单", rankingType: "top250", period: "2026"},
	}
	sortJSON, _ := json.Marshal(defaultLibrarySort())
	for _, definition := range definitions {
		filterJSON, _ := json.Marshal(map[string]string{"rankingType": definition.rankingType, "rankingPeriod": definition.period})
		if _, err := s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO virtual_libraries (id, name, description, item_type, filter_json, sort_json, is_public, is_adult, enabled, created_at, updated_at) VALUES (?, ?, ?, 'movie', ?, ?, 1, 1, 1, ?, ?) ON CONFLICT(id) DO NOTHING"), definition.id, definition.name, definition.description, string(filterJSON), string(sortJSON), now, now); err != nil {
			return err
		}
	}
	if adminUserID != "" {
		allLibs := []string{"library-default", "library-ranking-daily", "library-ranking-weekly", "library-ranking-monthly", "library-ranking-hot", "library-ranking-top250", "library-ranking-2026"}
		for _, libID := range allLibs {
			_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO user_library_permissions (user_id, library_id, can_view, can_play, created_at, updated_at) VALUES (?, ?, 1, 1, ?, ?) ON CONFLICT(user_id, library_id) DO UPDATE SET can_view = 1, can_play = 1, updated_at = excluded.updated_at"), adminUserID, libID, now, now)
		}
	}
	_ = s.SyncAllRankingLibraries(ctx)
	return err
}

func (s *Service) importItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var request ImportRequest
	if err := decodeJSON(r, &request); err != nil || len(request.Items) == 0 {
		writeError(w, r, http.StatusBadRequest, "INVALID_SUPPLEMENT_SYNC", "items 必须是非空数组")
		return
	}
	updated := 0
	result := make([]Item, 0, len(request.Items))
	for _, input := range request.Items {
		code, err := NormalizeCode(input.Code)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_SUPPLEMENT_SYNC", err.Error())
			return
		}
		var source, sourceID, existingCode string
		err = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT source, COALESCE(source_id, ''), code FROM media_items WHERE code = ? AND deleted_at IS NULL"), code).Scan(&source, &sourceID, &existingCode)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, r, http.StatusNotFound, "ITEM_NOT_FOUND", "该番号尚未通过排行榜生成，请从榜单选择并生成作品")
			return
		}
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "ITEM_READ_FAILED", "读取已有作品失败")
			return
		}
		if strings.TrimSpace(input.Title) == "" {
			writeError(w, r, http.StatusBadRequest, "INVALID_SUPPLEMENT_SYNC", "title 不能为空")
			return
		}
		// The compatibility endpoint may enrich a resource that already exists,
		// but it must never mint a new VirtualItem or change its stable identity.
		input.Code = existingCode
		input.Source = source
		input.SourceID = sourceID
		item, _, err := s.upsert(r.Context(), input, principal.User.ID)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "SUPPLEMENT_SYNC_FAILED", err.Error())
			return
		}
		s.seedSources(r.Context(), item)
		updated++
		result = append(result, item)
	}
	s.audit(r.Context(), principal.User.ID, "supplement_sync_items", "virtual_item", "", middleware.GetReqID(r.Context()), fmt.Sprintf(`{"created":0,"updated":%d}`, updated))
	writeJSON(w, http.StatusOK, map[string]any{"items": result, "created": 0, "updated": updated})
}

func (s *Service) upsert(ctx context.Context, input ImportItem, actorID string) (Item, bool, error) {
	code, err := NormalizeCode(input.Code)
	if err != nil {
		return Item{}, false, err
	}
	if strings.TrimSpace(input.Title) == "" {
		return Item{}, false, errors.New("title 不能为空")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "javdb"
	}
	sourceID := strings.TrimSpace(input.SourceID)
	rawCode := strings.TrimSpace(input.RawCode)
	if rawCode == "" {
		rawCode = input.Code
	}
	sourceURL := strings.TrimSpace(input.SourceURL)
	if sourceURL == "" {
		sourceURL = "https://javdb.com/search?q=" + code
	}
	isAdult := true
	if input.IsAdult != nil {
		isAdult = *input.IsAdult
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return Item{}, false, err
	}
	defer tx.Rollback()
	itemID, created, err := findOrInsertItem(ctx, tx, s.db, source, sourceID, code, rawCode, input, sourceURL, isAdult, now)
	if err != nil {
		return Item{}, false, err
	}
	if err := s.linkRelations(ctx, tx, itemID, input); err != nil {
		return Item{}, false, err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO library_items (library_id, item_id, position, created_at) SELECT id, ?, 0, ? FROM virtual_libraries WHERE id='library-default' ON CONFLICT(library_id, item_id) DO NOTHING"), itemID, now); err != nil {
		return Item{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, false, err
	}
	if err := s.SyncLibraryMemberships(ctx, "", itemID); err != nil {
		return Item{}, false, err
	}
	s.emitMedia(ctx, itemID, created)
	item, err := s.getByID(ctx, itemID, actorID, true)
	return item, created, err
}

func (s *Service) searchHints(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	queryText := NormalizeSearchQuery(r.URL.Query().Get("q"))
	limit := 8
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			limit = value
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	hints := make([]SearchHint, 0, limit*2)
	if queryText == "" {
		writeJSON(w, http.StatusOK, map[string]any{"query": queryText, "items": hints})
		return
	}

	itemQuery := "SELECT vi.id, vi.code, vi.title, COALESCE(vi.original_title, '') FROM media_items vi"
	args := make([]any, 0, 6)
	if !isElevated(principal.User) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", principal.User.ID, false, false)
		itemQuery += " JOIN library_items li ON li.item_id=vi.id JOIN virtual_libraries vl ON vl.id=li.library_id AND (" + visibility + ")"
		args = append(args, accessArgs...)
	}
	pattern := "%" + queryText + "%"
	itemQuery += " WHERE vi.deleted_at IS NULL AND (UPPER(vi.code) LIKE UPPER(?) OR LOWER(vi.title) LIKE LOWER(?) OR LOWER(COALESCE(vi.original_title, '')) LIKE LOWER(?))"
	args = append(args, pattern, pattern, pattern, queryText, queryText+"%")
	itemQuery += " GROUP BY vi.id, vi.code, vi.title, vi.original_title, vi.updated_at ORDER BY CASE WHEN UPPER(vi.code) = UPPER(?) THEN 0 WHEN UPPER(vi.code) LIKE UPPER(?) THEN 1 ELSE 2 END, vi.updated_at DESC, vi.id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(itemQuery), args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取搜索提示失败")
		return
	}
	for rows.Next() {
		var id, code, title, originalTitle string
		if err := rows.Scan(&id, &code, &title, &originalTitle); err != nil {
			_ = rows.Close()
			writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取作品提示失败")
			return
		}
		hints = append(hints, SearchHint{Kind: "item", ID: id, Label: title, Code: code, Title: title, Subtitle: originalTitle})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取作品提示失败")
		return
	}
	if err := rows.Close(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取作品提示失败")
		return
	}

	for _, relation := range []struct {
		kind, table, alias, idColumn, linkTable, linkColumn, subtitle string
	}{
		{kind: "person", table: "people", alias: "p", idColumn: "person_id", linkTable: "item_people", linkColumn: "person_id", subtitle: "演员或导演"},
		{kind: "studio", table: "studios", alias: "st", idColumn: "studio_id", linkTable: "item_studios", linkColumn: "studio_id", subtitle: "片商"},
		{kind: "series", table: "series", alias: "se", idColumn: "series_id", linkTable: "item_series", linkColumn: "series_id", subtitle: "系列"},
		{kind: "tag", table: "tags", alias: "t", idColumn: "tag_id", linkTable: "item_tags", linkColumn: "tag_id", subtitle: "标签"},
	} {
		relationQuery := "SELECT DISTINCT " + relation.alias + ".id, " + relation.alias + ".name FROM " + relation.table + " " + relation.alias
		relationArgs := make([]any, 0, 5)
		if !isElevated(principal.User) {
			visibility, accessArgs := auth.MediaVisibilitySQL("linked_media", principal.User.ID, false, false)
			relationQuery += " JOIN " + relation.linkTable + " rel_link ON rel_link." + relation.linkColumn + "=" + relation.alias + ".id JOIN media_items linked_media ON linked_media.id=rel_link.item_id AND (" + visibility + ")"
			relationArgs = append(relationArgs, accessArgs...)
		}
		relationQuery += " WHERE " + relation.alias + ".is_excluded = 0 AND LOWER(" + relation.alias + ".name) LIKE LOWER(?) ORDER BY CASE WHEN LOWER(" + relation.alias + ".name) = LOWER(?) THEN 0 WHEN LOWER(" + relation.alias + ".name) LIKE LOWER(?) THEN 1 ELSE 2 END, " + relation.alias + ".name LIMIT ?"
		relationArgs = append(relationArgs, pattern, queryText, queryText+"%", limit)
		relationRows, queryErr := s.db.DB().QueryContext(r.Context(), s.db.Rebind(relationQuery), relationArgs...)
		if queryErr != nil {
			writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取关系提示失败")
			return
		}
		for relationRows.Next() {
			var id, name string
			if scanErr := relationRows.Scan(&id, &name); scanErr != nil {
				_ = relationRows.Close()
				writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取关系提示失败")
				return
			}
			hints = append(hints, SearchHint{Kind: relation.kind, ID: id, Label: name, Subtitle: relation.subtitle})
		}
		if rowsErr := relationRows.Err(); rowsErr != nil {
			_ = relationRows.Close()
			writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取关系提示失败")
			return
		}
		if closeErr := relationRows.Close(); closeErr != nil {
			writeError(w, r, http.StatusInternalServerError, "SEARCH_HINTS_READ_FAILED", "读取关系提示失败")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": queryText, "items": hints})
}

func findOrInsertItem(ctx context.Context, tx *sql.Tx, db *repository.Database, source, sourceID, code, rawCode string, input ImportItem, sourceURL string, isAdult bool, now string) (string, bool, error) {
	var id string
	query := "SELECT id FROM media_items WHERE source = ? AND source_id = ?"
	args := []any{source, sourceID}
	if sourceID == "" {
		query = "SELECT id FROM media_items WHERE code = ?"
		args = []any{code}
	}
	err := tx.QueryRowContext(ctx, db.Rebind(query), args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) && sourceID != "" {
		// The JavDB code is the long-lived resource key. If a source ID changes,
		// reuse the existing item by normalized code instead of creating a second
		// row or conflicting with the unique code constraint.
		err = tx.QueryRowContext(ctx, db.Rebind("SELECT id FROM media_items WHERE code = ?"), code).Scan(&id)
	}
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		id = newID("item")
		if source == "javdb" {
			id = "javdb:" + code
		}
		created = true
		_, err = tx.ExecContext(ctx, db.Rebind("INSERT INTO media_items (id, source, source_id, code, raw_code, title, translated_title, original_title, overview, release_date, year, runtime_seconds, source_rating, rating_count, source_url, source_updated_at, last_synced_at, metadata_status, is_adult, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'complete', ?, ?, ?)"), id, source, nullable(sourceID), code, rawCode, input.Title, nullable(input.TranslatedTitle), nullable(input.OriginalTitle), nullable(input.Overview), nullable(input.ReleaseDate), input.Year, input.RuntimeSeconds, input.SourceRating, input.RatingCount, sourceURL, now, now, boolInt(isAdult), now, now)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, db.Rebind("UPDATE media_items SET raw_code = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'raw_code' AND locked = 1) THEN raw_code ELSE ? END, title = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'title' AND locked = 1) THEN title ELSE ? END, translated_title = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'title' AND locked = 1) THEN title ELSE ? END, original_title = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'title' AND locked = 1) THEN title ELSE ? END, overview = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'overview' AND locked = 1) THEN overview ELSE ? END, release_date = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'release_date' AND locked = 1) THEN release_date ELSE ? END, year = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'year' AND locked = 1) THEN year ELSE ? END, runtime_seconds = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'runtime_seconds' AND locked = 1) THEN runtime_seconds ELSE ? END, source_rating = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'source_rating' AND locked = 1) THEN source_rating ELSE ? END, rating_count = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'rating_count' AND locked = 1) THEN rating_count ELSE ? END, source_url = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'source_url' AND locked = 1) THEN source_url ELSE ? END, source_updated_at = ?, last_synced_at = ?, metadata_status = 'complete', is_adult = CASE WHEN EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = media_items.id AND field_name = 'is_adult' AND locked = 1) THEN is_adult ELSE ? END, updated_at = ? WHERE id = ?"), rawCode, input.Title, nullable(input.TranslatedTitle), nullable(input.OriginalTitle), nullable(input.Overview), nullable(input.ReleaseDate), input.Year, input.RuntimeSeconds, input.SourceRating, input.RatingCount, sourceURL, now, now, boolInt(isAdult), now, id)
	}
	if err != nil {
		return "", false, err
	}
	return id, created, nil
}

func (s *Service) linkRelations(ctx context.Context, tx *sql.Tx, itemID string, input ImportItem) error {
	groups := []struct {
		kind, role string
		values     []RelationInput
	}{
		{"people", "actor", input.People}, {"studios", "studio", input.Studios}, {"series", "series", input.Series}, {"tags", "tag", input.Tags},
	}
	for _, group := range groups {
		for order, relation := range group.values {
			if strings.TrimSpace(relation.Name) == "" {
				continue
			}
			role := group.role
			if group.kind == "people" && relation.Role != "" {
				role = relation.Role
			}
			id, err := ensureRelation(ctx, tx, s.db, group.kind, relation.Name, "custom")
			if err != nil {
				return err
			}
			if err := entities.Link(ctx, tx, s.db, itemID, group.kind, id, role, order); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureRelation(ctx context.Context, tx *sql.Tx, db *repository.Database, table, name, kind string) (string, error) {
	return entities.Ensure(ctx, tx, db, entities.Input{Kind: table, Name: name, TagKind: kind})
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	limit := 24
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			limit = value
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	values := r.URL.Query()
	cursorHash := filterCursorHash(values)
	offset, validCursor := decodeFilterCursor(values.Get("cursor"), cursorHash)
	if !validCursor {
		writeError(w, r, http.StatusBadRequest, "INVALID_CURSOR", "分页游标与当前筛选条件不匹配")
		return
	}
	queryText := NormalizeSearchQuery(values.Get("q"))
	query := "SELECT vi.id, vi.source, COALESCE(vi.source_id, ''), vi.code, vi.raw_code, vi.title, COALESCE(vi.translated_title, ''), COALESCE(vi.original_title, ''), COALESCE(vi.overview, ''), COALESCE(vi.release_date, ''), vi.year, vi.runtime_seconds, vi.source_rating, vi.rating_count, vi.source_url, vi.metadata_status, vi.is_adult, vi.created_at, vi.updated_at, vi.item_type, COALESCE(vi.parent_id, ''), vi.index_number, vi.provider_ids_json, vi.extra_attributes_json, vi.metadata_module_id FROM media_items vi"
	args := make([]any, 0, 8)
	visibility, accessArgs := auth.MediaVisibilitySQL("vi", principal.User.ID, isElevated(principal.User), false)
	query += " WHERE " + visibility
	args = append(args, accessArgs...)
	scope, scopeArgs, err := browseScope(values)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BROWSE", err.Error())
		return
	}
	query += scope
	args = append(args, scopeArgs...)
	if queryText != "" {
		query += " AND (LOWER(vi.code) LIKE LOWER(?) OR LOWER(vi.title) LIKE LOWER(?) OR LOWER(vi.original_title) LIKE LOWER(?))"
		pattern := "%" + queryText + "%"
		args = append(args, pattern, pattern, pattern)
	}
	if years := splitFilterValues(values.Get("years")); len(years) > 0 {
		conditions := make([]string, 0, len(years))
		for _, raw := range years {
			year, err := strconv.Atoi(raw)
			if err == nil && year >= 1900 && year <= 2100 {
				conditions = append(conditions, "vi.year = ?")
				args = append(args, year)
			}
		}
		if len(conditions) > 0 {
			query += " AND (" + strings.Join(conditions, " OR ") + ")"
		}
	} else if yearText := strings.TrimSpace(values.Get("year")); yearText != "" {
		if year, err := strconv.Atoi(yearText); err == nil {
			query += " AND vi.year = ?"
			args = append(args, year)
		}
	}
	for _, personID := range splitFilterValues(values.Get("actorIds")) {
		query += " AND EXISTS (SELECT 1 FROM item_people ip WHERE ip.item_id = vi.id AND ip.person_id = ? AND ip.role = 'actor')"
		args = append(args, personID)
	}
	for _, personID := range splitFilterValues(values.Get("directorIds")) {
		query += " AND EXISTS (SELECT 1 FROM item_people ip WHERE ip.item_id = vi.id AND ip.person_id = ? AND ip.role = 'director')"
		args = append(args, personID)
	}
	for _, studioID := range splitFilterValues(values.Get("studioIds")) {
		query += " AND EXISTS (SELECT 1 FROM item_studios ist WHERE ist.item_id = vi.id AND ist.studio_id = ?)"
		args = append(args, studioID)
	}
	for _, seriesID := range splitFilterValues(values.Get("seriesIds")) {
		query += " AND EXISTS (SELECT 1 FROM item_series ise WHERE ise.item_id = vi.id AND ise.series_id = ?)"
		args = append(args, seriesID)
	}
	for _, tagID := range splitFilterValues(values.Get("tagIds")) {
		query += " AND EXISTS (SELECT 1 FROM item_tags it WHERE it.item_id = vi.id AND it.tag_id = ?)"
		args = append(args, tagID)
	}
	for _, tagID := range splitFilterValues(values.Get("excludeTagIds")) {
		query += " AND NOT EXISTS (SELECT 1 FROM item_tags it WHERE it.item_id = vi.id AND it.tag_id = ?)"
		args = append(args, tagID)
	}
	for _, collectionID := range splitFilterValues(values.Get("collectionIds")) {
		query += " AND EXISTS (SELECT 1 FROM collection_items ci WHERE ci.item_id = vi.id AND ci.collection_id = ?)"
		args = append(args, collectionID)
	}
	if raw := strings.TrimSpace(values.Get("minRating")); raw != "" {
		if rating, err := strconv.ParseFloat(raw, 64); err == nil && rating >= 0 && rating <= 10 {
			query += " AND vi.source_rating >= ?"
			args = append(args, rating)
		}
	}
	for _, filter := range []struct {
		name, column string
	}{
		{name: "isFavorite", column: "is_favorite"},
		{name: "isWatched", column: "is_watched"},
	} {
		if enabled, ok := parseOptionalBool(values.Get(filter.name)); ok {
			exists := "EXISTS"
			if !enabled {
				exists = "NOT EXISTS"
			}
			query += " AND " + exists + " (SELECT 1 FROM user_item_states uis WHERE uis.item_id = vi.id AND uis.user_id = ? AND uis." + filter.column + " = 1)"
			args = append(args, principal.User.ID)
		}
	}
	if playable, ok := parseOptionalBool(values.Get("hasPlayableSource")); ok {
		clause, sourceArgs := s.HasAvailableSourceSQL("vi")
		if !playable {
			clause = "NOT (" + clause + ")"
		}
		query += " AND (" + clause + ")"
		args = append(args, sourceArgs...)
	}
	if releaseDate := strings.TrimSpace(values.Get("releaseDate")); releaseDate != "" {
		query += " AND vi.release_date LIKE ?"
		args = append(args, releaseDate+"%")
	}
	sortColumn := map[string]string{"title": "vi.title", "code": "vi.code", "year": "vi.year", "rating": "vi.source_rating", "releaseDate": "vi.release_date", "updatedAt": "vi.updated_at", "indexNumber": "vi.index_number"}[values.Get("sort")]
	if sortColumn == "" {
		sortColumn = "vi.updated_at"
	}
	sortDirection := "DESC"
	if strings.EqualFold(values.Get("sortOrder"), "asc") {
		sortDirection = "ASC"
	}
	if values.Get("parentId") != "" && values.Get("sort") == "" {
		sortColumn = "vi.index_number"
		sortDirection = "ASC"
	}
	if queryText != "" && strings.TrimSpace(values.Get("sort")) == "" {
		query += " ORDER BY CASE WHEN UPPER(vi.code) = UPPER(?) THEN 0 WHEN UPPER(vi.code) LIKE UPPER(?) THEN 1 ELSE 2 END, vi.updated_at DESC, vi.id DESC"
		args = append(args, queryText, queryText+"%")
	} else {
		query += " ORDER BY " + sortColumn + " " + sortDirection + " NULLS LAST, vi.id DESC"
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ITEMS_READ_FAILED", "读取作品失败")
		return
	}
	defer rows.Close()
	items := make([]Item, 0, limit)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "ITEMS_READ_FAILED", "读取作品失败")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "ITEMS_READ_FAILED", "读取作品失败")
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = encodeFilterCursor(offset+limit, cursorHash)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func filterCursorHash(values url.Values) string {
	canonical := make(url.Values)
	for key, rawValues := range values {
		if key == "cursor" || key == "limit" {
			continue
		}
		for _, value := range rawValues {
			canonical.Add(key, value)
		}
	}
	digest := sha256.Sum256([]byte(canonical.Encode()))
	return hex.EncodeToString(digest[:8])
}

func decodeFilterCursor(raw, expectedHash string) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, true
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || parts[1] != expectedHash {
		return 0, false
	}
	offset, err := strconv.Atoi(parts[0])
	if err != nil || offset < 0 {
		return 0, false
	}
	return offset, true
}

func encodeFilterCursor(offset int, hash string) string {
	return strconv.Itoa(offset) + "." + hash
}

func unescapeParam(r *http.Request, key string) string {
	val := chi.URLParam(r, key)
	if unescaped, err := url.PathUnescape(val); err == nil {
		return unescaped
	}
	return val
}

func (s *Service) detail(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	item, err := s.getByID(r.Context(), unescapeParam(r, "itemID"), principal.User.ID, isElevated(principal.User))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusForbidden, "ITEM_NOT_VISIBLE", "作品不可见")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Service) getByID(ctx context.Context, itemID, userID string, elevated bool) (Item, error) {
	query := "SELECT vi.id, vi.source, COALESCE(vi.source_id, ''), vi.code, vi.raw_code, vi.title, COALESCE(vi.translated_title, ''), COALESCE(vi.original_title, ''), COALESCE(vi.overview, ''), COALESCE(vi.release_date, ''), vi.year, vi.runtime_seconds, vi.source_rating, vi.rating_count, vi.source_url, vi.metadata_status, vi.is_adult, vi.created_at, vi.updated_at, vi.item_type, COALESCE(vi.parent_id, ''), vi.index_number, vi.provider_ids_json, vi.extra_attributes_json, vi.metadata_module_id FROM media_items vi"
	visibility, accessArgs := auth.MediaVisibilitySQL("vi", userID, elevated, false)
	query += " WHERE vi.id = ? AND " + visibility + " LIMIT 1"
	args := append([]any{itemID}, accessArgs...)
	var row *sql.Row
	row = s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...)
	item, err := scanItem(row)
	if err != nil {
		return Item{}, err
	}
	item.People, item.Studios, item.Series, item.Tags = s.relations(ctx, item.ID)
	return item, nil
}

// GetItemForUser loads a catalog item while enforcing the caller's library
// permissions. Protocol adapters should use this instead of querying the
// catalog tables directly.
func (s *Service) GetItemForUser(ctx context.Context, itemID string, user auth.User) (Item, error) {
	return s.getByID(ctx, itemID, user.ID, isElevated(user))
}

func (s *Service) relations(ctx context.Context, itemID string) ([]Relation, []Relation, []Relation, []Relation) {
	people := s.queryRelations(ctx, "SELECT p.id, p.name, ip.role FROM people p JOIN item_people ip ON ip.person_id = p.id WHERE ip.item_id = ? ORDER BY ip.sort_order, p.name", itemID)
	studios := s.queryRelations(ctx, "SELECT st.id, st.name, '' FROM studios st JOIN item_studios ist ON ist.studio_id = st.id WHERE ist.item_id = ? ORDER BY ist.sort_order, st.name", itemID)
	series := s.queryRelations(ctx, "SELECT se.id, se.name, '' FROM series se JOIN item_series ise ON ise.series_id = se.id WHERE ise.item_id = ? ORDER BY ise.sort_order, se.name", itemID)
	tags := s.queryRelations(ctx, "SELECT t.id, t.name, '' FROM tags t JOIN item_tags it ON it.tag_id = t.id WHERE it.item_id = ? ORDER BY t.name", itemID)
	return people, studios, series, tags
}
func (s *Service) queryRelations(ctx context.Context, query, itemID string) []Relation {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), itemID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var values []Relation
	for rows.Next() {
		var value Relation
		if rows.Scan(&value.ID, &value.Name, &value.Role) == nil {
			values = append(values, value)
		}
	}
	return values
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	itemID := unescapeParam(r, "itemID")
	var input struct {
		Title           *string  `json:"title"`
		TranslatedTitle *string  `json:"translatedTitle"`
		OriginalTitle   *string  `json:"originalTitle"`
		Overview        *string  `json:"overview"`
		Year            *int     `json:"year"`
		SourceRating    *float64 `json:"sourceRating"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "作品字段格式不正确")
		return
	}
	updates := make([]string, 0, 6)
	args := make([]any, 0, 7)
	fields := []struct {
		name  string
		value any
		set   bool
	}{{"title", input.Title, input.Title != nil}, {"translated_title", input.TranslatedTitle, input.TranslatedTitle != nil}, {"original_title", input.OriginalTitle, input.OriginalTitle != nil}, {"overview", input.Overview, input.Overview != nil}, {"year", input.Year, input.Year != nil}, {"source_rating", input.SourceRating, input.SourceRating != nil}}
	for _, field := range fields {
		if field.set {
			updates = append(updates, field.name+" = ?")
			args = append(args, field.value)
		}
	}
	if len(updates) == 0 {
		writeError(w, r, http.StatusBadRequest, "NO_CHANGES", "没有要更新的字段")
		return
	}
	updates = append(updates, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano), itemID)
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE media_items SET "+strings.Join(updates, ", ")+" WHERE id = ? AND deleted_at IS NULL"), args...)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "ITEM_UPDATE_FAILED", "更新作品失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
		return
	}
	item, _ := s.getByID(r.Context(), itemID, principal.User.ID, true)
	s.emitMedia(r.Context(), itemID, false)
	s.audit(r.Context(), principal.User.ID, "update_item", "virtual_item", itemID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusOK, item)
}

func (s *Service) sync(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	itemID := unescapeParam(r, "itemID")
	var exists int
	if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM media_items WHERE id = ? AND deleted_at IS NULL"), itemID).Scan(&exists); err != nil || exists != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	taskID := newID("task")
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO tasks (id, type, idempotency_key, status, payload_json, created_at, updated_at) VALUES (?, 'sync_item', ?, 'queued', ?, ?, ?)"), taskID, "sync_item:"+itemID, fmt.Sprintf(`{"itemId":"%s","requestedBy":"%s"}`, itemID, principal.User.ID), now, now)
	if err != nil {
		writeError(w, r, http.StatusConflict, "TASK_CREATE_FAILED", "同步任务创建失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "sync_item", "virtual_item", itemID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusAccepted, map[string]string{"taskId": taskID, "status": "queued"})
}

func (s *Service) lockFields(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	itemID := unescapeParam(r, "itemID")
	var input struct {
		Fields []string `json:"fields"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "fields 必须是数组")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, field := range input.Fields {
		if strings.TrimSpace(field) == "" {
			continue
		}
		if _, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO metadata_field_states (item_id, field_name, value_json, source, locked, updated_by_user_id, created_at, updated_at) VALUES (?, ?, 'null', 'manual', 1, ?, ?, ?) ON CONFLICT(item_id, field_name) DO UPDATE SET locked = 1, source = 'manual', updated_by_user_id = excluded.updated_by_user_id, updated_at = excluded.updated_at"), itemID, field, principal.User.ID, now, now); err != nil {
			writeError(w, r, http.StatusBadRequest, "LOCK_FIELD_FAILED", "锁定字段失败")
			return
		}
	}
	s.audit(r.Context(), principal.User.ID, "lock_item_fields", "virtual_item", itemID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusOK, map[string]any{"itemId": itemID, "lockedFields": input.Fields})
}

func (s *Service) audit(ctx context.Context, actor, action, resource, resourceID, requestID, metadata string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), newID("aud"), actor, action, resource, resourceID, requestID, "success", metadata, time.Now().UTC().Format(time.RFC3339Nano))
}

func scanItem(scanner interface{ Scan(...any) error }) (Item, error) {
	var item Item
	var sourceID, translated, original, overview, releaseDate sql.NullString
	var year, runtime sql.NullInt64
	var rating sql.NullFloat64
	var isAdult int
	var idsJSON, extraJSON string
	if err := scanner.Scan(&item.ID, &item.Source, &sourceID, &item.Code, &item.RawCode, &item.Title, &translated, &original, &overview, &releaseDate, &year, &runtime, &rating, &item.RatingCount, &item.SourceURL, &item.MetadataStatus, &isAdult, &item.CreatedAt, &item.UpdatedAt, &item.ItemType, &item.ParentID, &item.IndexNumber, &idsJSON, &extraJSON, &item.MetadataModuleID); err != nil {
		return Item{}, err
	}
	if err := json.Unmarshal([]byte(idsJSON), &item.ProviderIDs); err != nil {
		return Item{}, err
	}
	if err := json.Unmarshal([]byte(extraJSON), &item.ExtraAttributes); err != nil {
		return Item{}, err
	}
	if len(item.ProviderIDs) == 0 && sourceID.String != "" {
		item.ProviderIDs = map[string]string{item.Source: sourceID.String}
	}
	item.SourceID = sourceID.String
	item.TranslatedTitle = translated.String
	item.OriginalTitle = original.String
	item.Overview = overview.String
	item.ReleaseDate = releaseDate.String
	if year.Valid {
		value := int(year.Int64)
		item.Year = &value
	}
	if runtime.Valid {
		value := int(runtime.Int64)
		item.RuntimeSeconds = &value
	}
	if rating.Valid {
		value := rating.Float64
		item.SourceRating = &value
	}
	item.IsAdult = isAdult == 1
	return item, nil
}

func NormalizeCode(raw string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "_", "-")
	if value == "" {
		return "", errors.New("番号不能为空")
	}
	parts := strings.Split(value, "-")
	if len(parts) < 2 || parts[0] == "" || parts[len(parts)-1] == "" {
		return "", fmt.Errorf("番号格式不正确: %s", raw)
	}
	for partIndex, part := range parts {
		for _, character := range part {
			if (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
				return "", fmt.Errorf("番号格式不正确: %s", raw)
			}
		}
		if partIndex == len(parts)-1 {
			for _, character := range part {
				if character < '0' || character > '9' {
					return "", fmt.Errorf("番号格式不正确: %s", raw)
				}
			}
		}
	}
	return value, nil
}
func isElevated(user auth.User) bool {
	for _, role := range user.Roles {
		if role == "Admin" || role == "Editor" {
			return true
		}
	}
	return false
}

func splitFilterValues(raw string) []string {
	values := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' })
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(result) >= 100 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func NormalizeSearchQuery(raw string) string {
	value := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	codeInput := strings.ReplaceAll(value, " ", "-")
	if code, err := NormalizeCode(codeInput); err == nil {
		return code
	}
	return value
}

func parseOptionalBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	default:
		return false, false
	}
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
func newID(prefix string) string { return prefix + "_" + rand.Text() }
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": middleware.GetReqID(r.Context())}})
}

func (s *Service) SyncRankingLibraryItems(ctx context.Context, rankingType, period string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, filter_json FROM virtual_libraries WHERE enabled = 1"))
	if err != nil {
		return err
	}
	defer rows.Close()

	var matchingLibIDs []string
	for rows.Next() {
		var libID, filterJSON string
		if err := rows.Scan(&libID, &filterJSON); err != nil {
			continue
		}
		var filterMap map[string]any
		if json.Unmarshal([]byte(filterJSON), &filterMap) == nil {
			rType, _ := filterMap["rankingType"].(string)
			rPeriod, _ := filterMap["rankingPeriod"].(string)
			if strings.EqualFold(rType, rankingType) && strings.EqualFold(rPeriod, period) {
				matchingLibIDs = append(matchingLibIDs, libID)
			}
		}
	}
	_ = rows.Close()

	for _, libID := range matchingLibIDs {
		tx, err := s.db.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		query := `INSERT INTO library_items (library_id, item_id, position, created_at)
			SELECT ?, ir.item_id, ir.rank, ?
			FROM item_rankings ir
			JOIN media_items vi ON vi.id = ir.item_id AND vi.deleted_at IS NULL
			WHERE ir.ranking_type = ? AND ir.period = ? AND ir.is_current = 1
			ORDER BY ir.rank ASC
			ON CONFLICT(library_id, item_id) DO UPDATE SET position = excluded.position`
		if _, err := tx.ExecContext(ctx, s.db.Rebind(query), libID, now, rankingType, period); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) SyncAllRankingLibraries(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind(`
		INSERT INTO library_items (library_id, item_id, position, created_at)
		SELECT 'library-default', id, 0, ?
		FROM media_items
		WHERE deleted_at IS NULL AND EXISTS (SELECT 1 FROM virtual_libraries WHERE id='library-default')
		ON CONFLICT(library_id, item_id) DO NOTHING
	`), now)

	targets := []struct{ RankingType, Period string }{
		{RankingType: "ranking", Period: "daily"},
		{RankingType: "ranking", Period: "weekly"},
		{RankingType: "ranking", Period: "monthly"},
		{RankingType: "playback", Period: "daily"},
		{RankingType: "top250", Period: "all"},
		{RankingType: "top250", Period: "2026"},
	}
	for _, t := range targets {
		_ = s.SyncRankingLibraryItems(ctx, t.RankingType, t.Period)
	}
	return nil
}

type RankingScheduleConfig struct {
	Enabled       bool    `json:"enabled"`
	UpdateTime    string  `json:"updateTime"`
	IntervalHours int     `json:"intervalHours"`
	LastRunAt     *string `json:"lastRunAt,omitempty"`
	UpdatedAt     string  `json:"updatedAt"`
	NextRunAt     *string `json:"nextRunAt,omitempty"`
}

func (s *Service) readRankingScheduleConfig(ctx context.Context) (RankingScheduleConfig, error) {
	var cfg RankingScheduleConfig
	var enabledInt int
	var lastRun sql.NullString
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT enabled, update_time, interval_hours, last_run_at, updated_at FROM ranking_schedule_config WHERE id = 'default'")).Scan(&enabledInt, &cfg.UpdateTime, &cfg.IntervalHours, &lastRun, &cfg.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		cfg.Enabled = true
		cfg.UpdateTime = "04:00"
		cfg.IntervalHours = 24
		cfg.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return cfg, nil
	} else if err != nil {
		return cfg, err
	}
	cfg.Enabled = enabledInt == 1
	if lastRun.Valid && lastRun.String != "" && lastRun.String != "1970-01-01T00:00:00Z" {
		cfg.LastRunAt = &lastRun.String
	}
	now := time.Now().Local()
	parts := strings.Split(cfg.UpdateTime, ":")
	targetHour, targetMin := 4, 0
	if len(parts) == 2 {
		if h, err := strconv.Atoi(parts[0]); err == nil && h >= 0 && h < 24 {
			targetHour = h
		}
		if m, err := strconv.Atoi(parts[1]); err == nil && m >= 0 && m < 60 {
			targetMin = m
		}
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), targetHour, targetMin, 0, 0, now.Location())
	if cfg.IntervalHours >= 24 {
		if !now.Before(next) {
			next = next.AddDate(0, 0, 1)
		}
	} else {
		if cfg.LastRunAt != nil {
			if lt, err := time.Parse(time.RFC3339Nano, *cfg.LastRunAt); err == nil {
				next = lt.Local().Add(time.Duration(cfg.IntervalHours) * time.Hour)
			}
		}
		if next.Before(now) {
			next = now.Add(time.Duration(cfg.IntervalHours) * time.Hour)
		}
	}
	nextStr := next.Format("2006-01-02 15:04:05")
	cfg.NextRunAt = &nextStr
	return cfg, nil
}

func (s *Service) getRankingSchedule(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.readRankingScheduleConfig(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SCHEDULE_CONFIG_READ_FAILED", "读取榜单定时设置失败")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Service) updateRankingSchedule(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var req struct {
		Enabled       *bool   `json:"enabled"`
		UpdateTime    *string `json:"updateTime"`
		IntervalHours *int    `json:"intervalHours"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "请求参数无效")
		return
	}
	current, err := s.readRankingScheduleConfig(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SCHEDULE_CONFIG_READ_FAILED", "读取榜单定时设置失败")
		return
	}
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.UpdateTime != nil {
		timeStr := strings.TrimSpace(*req.UpdateTime)
		parts := strings.Split(timeStr, ":")
		if len(parts) != 2 {
			writeError(w, r, http.StatusBadRequest, "INVALID_UPDATE_TIME", "更新时间格式必须是 HH:MM")
			return
		}
		h, errH := strconv.Atoi(parts[0])
		m, errM := strconv.Atoi(parts[1])
		if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			writeError(w, r, http.StatusBadRequest, "INVALID_UPDATE_TIME", "更新时间必须在 00:00 到 23:59 之间")
			return
		}
		current.UpdateTime = fmt.Sprintf("%02d:%02d", h, m)
	}
	if req.IntervalHours != nil {
		if *req.IntervalHours < 1 || *req.IntervalHours > 720 {
			writeError(w, r, http.StatusBadRequest, "INVALID_INTERVAL_HOURS", "更新周期必须在 1 到 720 小时之间")
			return
		}
		current.IntervalHours = *req.IntervalHours
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	current.UpdatedAt = now
	enabledInt := 0
	if current.Enabled {
		enabledInt = 1
	}
	_, err = s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE ranking_schedule_config SET enabled = ?, update_time = ?, interval_hours = ?, updated_at = ? WHERE id = 'default'"), enabledInt, current.UpdateTime, current.IntervalHours, now)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SCHEDULE_CONFIG_UPDATE_FAILED", "保存榜单定时设置失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "update_ranking_schedule", "ranking_schedule", "default", middleware.GetReqID(r.Context()), "{}")
	cfg, _ := s.readRankingScheduleConfig(r.Context())
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Service) triggerRankingSchedule(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	taskIDs, err := s.TriggerAllRankingsUpdate(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TRIGGER_FAILED", "触发榜单更新失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "trigger_all_rankings", "ranking_schedule", "default", middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "triggered", "taskIds": taskIDs, "count": len(taskIDs)})
}
