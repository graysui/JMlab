package home

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/ranking"
	"github.com/graysui/JMlab/internal/repository"
)

type Service struct {
	db      *repository.Database
	auth    *auth.Service
	catalog *catalog.Service
	logger  *slog.Logger
}

type Module struct {
	Title string         `json:"title"`
	Items []catalog.Item `json:"items"`
}

type RankedItem struct {
	Rank int          `json:"rank"`
	Item catalog.Item `json:"item"`
}

type DashboardStats struct {
	VisibleItems  int `json:"visibleItems"`
	PlayableItems int `json:"playableItems"`
	People        int `json:"people"`
	Studios       int `json:"studios"`
	Series        int `json:"series"`
	Tags          int `json:"tags"`
}

type Dashboard struct {
	GeneratedAt string                  `json:"generatedAt"`
	LibraryID   string                  `json:"libraryId,omitempty"`
	Modules     map[string]Module       `json:"modules"`
	Rankings    map[string][]RankedItem `json:"rankings"`
	Stats       DashboardStats          `json:"stats"`
}

type DashboardResourceCounts struct {
	Items           int `json:"items"`
	PlayableItems   int `json:"playableItems"`
	People          int `json:"people"`
	Studios         int `json:"studios"`
	Series          int `json:"series"`
	Tags            int `json:"tags"`
	Collections     int `json:"collections"`
	Libraries       int `json:"libraries"`
	Providers       int `json:"providers"`
	PlaybackSources int `json:"playbackSources"`
}

type DashboardTaskSummary struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type DashboardRecentTask struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type DashboardErrorSummary struct {
	TaskFailures  int `json:"taskFailures"`
	AuditFailures int `json:"auditFailures"`
	Total         int `json:"total"`
}

type DashboardRecentError struct {
	Source       string `json:"source"`
	ID           string `json:"id"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
	OccurredAt   string `json:"occurredAt"`
	ResourceType string `json:"resourceType,omitempty"`
	ResourceID   string `json:"resourceId,omitempty"`
}

type DashboardOnlineSummary struct {
	Sessions       int `json:"sessions"`
	Users          int `json:"users"`
	ActivePlayback int `json:"activePlayback"`
}

type DashboardProviderHealth struct {
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Unknown   int `json:"unknown"`
}

type DashboardHealth struct {
	Database string `json:"database"`
	Service  string `json:"service"`
}

type AdminDashboard struct {
	GeneratedAt    string                  `json:"generatedAt"`
	LibraryID      string                  `json:"libraryId,omitempty"`
	Resources      DashboardResourceCounts `json:"resources"`
	Tasks          []DashboardTaskSummary  `json:"tasks"`
	RecentTasks    []DashboardRecentTask   `json:"recentTasks"`
	Errors         DashboardErrorSummary   `json:"errors"`
	RecentErrors   []DashboardRecentError  `json:"recentErrors"`
	ProviderHealth DashboardProviderHealth `json:"providerHealth"`
	Online         DashboardOnlineSummary  `json:"online"`
	Health         DashboardHealth         `json:"health"`
}

type SourceHealthStats struct {
	Active            int     `json:"active"`
	Unavailable       int     `json:"unavailable"`
	Expired           int     `json:"expired"`
	ProbeCount        int     `json:"probeCount"`
	ProbeSuccessCount int     `json:"probeSuccessCount"`
	SuccessRate       float64 `json:"successRate"`
}

type StatisticsOverview struct {
	GeneratedAt           string               `json:"generatedAt"`
	LibraryID             string               `json:"libraryId,omitempty"`
	Stats                 DashboardStats       `json:"stats"`
	RatingDistribution    []DistributionBucket `json:"ratingDistribution"`
	YearDistribution      []DistributionBucket `json:"yearDistribution"`
	WatchedItems          int                  `json:"watchedItems"`
	FavoriteItems         int                  `json:"favoriteItems"`
	WatchedDurationSecond int64                `json:"watchedDurationSeconds"`
	SourceHealth          SourceHealthStats    `json:"sourceHealth"`
}

type StatisticsEntityBucket struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type StatisticsItemsResponse struct {
	GeneratedAt string                   `json:"generatedAt"`
	LibraryID   string                   `json:"libraryId,omitempty"`
	Limit       int                      `json:"limit"`
	Stats       DashboardStats           `json:"stats"`
	ByYear      []DistributionBucket     `json:"byYear"`
	ByRating    []DistributionBucket     `json:"byRating"`
	People      []StatisticsEntityBucket `json:"people"`
	Studios     []StatisticsEntityBucket `json:"studios"`
	Series      []StatisticsEntityBucket `json:"series"`
	Tags        []StatisticsEntityBucket `json:"tags"`
}

type DistributionBucket struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type RankingResponse struct {
	RankingType        string               `json:"rankingType"`
	Period             string               `json:"period"`
	ObservedAt         string               `json:"observedAt,omitempty"`
	Items              []RankedItem         `json:"items"`
	RatingDistribution []DistributionBucket `json:"ratingDistribution"`
	YearDistribution   []DistributionBucket `json:"yearDistribution"`
}

type rankingInput struct {
	ItemID string `json:"itemId"`
	Rank   int    `json:"rank"`
}

func NewService(db *repository.Database, authService *auth.Service, catalogService *catalog.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, auth: authService, catalog: catalogService, logger: logger}
}

func (s *Service) RegisterRoutes(router chi.Router) {
	router.With(s.auth.RequireAuth).Get("/home", s.dashboard)
	router.With(s.auth.RequireAuth).Get("/rankings/{rankingType}", s.ranking)
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin", "Editor")).Get("/dashboard", s.adminDashboard)
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin", "Editor")).Get("/statistics/overview", s.statisticsOverview)
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin", "Editor")).Get("/statistics/items", s.statisticsItems)
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin", "Editor")).Get("/admin/rankings/{rankingType}", s.adminRanking)
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin", "Editor"), s.auth.RequireCSRF).Put("/admin/rankings/{rankingType}", s.replaceRanking)
}

func (s *Service) ranking(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	rankingType, err := normalizeRankingType(chi.URLParam(r, "rankingType"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_TYPE", err.Error())
		return
	}
	period, err := normalizeRankingPeriod(rankingType, r.URL.Query().Get("period"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_PERIOD", err.Error())
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 250)
	libraryID := strings.TrimSpace(r.URL.Query().Get("libraryId"))
	items, observedAt, err := s.visibleRankingItems(r.Context(), principal.User, rankingType, period, libraryID, limit)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "RANKING_READ_FAILED", "读取榜单失败")
		return
	}
	ratingDistribution, yearDistribution, err := s.visibleDistributions(r.Context(), principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "RANKING_STATS_READ_FAILED", "读取排行统计失败")
		return
	}
	writeJSON(w, http.StatusOK, RankingResponse{
		RankingType:        rankingType,
		Period:             period,
		ObservedAt:         observedAt,
		Items:              items,
		RatingDistribution: ratingDistribution,
		YearDistribution:   yearDistribution,
	})
}

func (s *Service) visibleRankingItems(ctx context.Context, user auth.User, rankingType, period, libraryID string, limit int) ([]RankedItem, string, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT item_id, rank, observed_at FROM item_rankings WHERE ranking_type = ? AND period = ? AND is_current = 1 ORDER BY rank LIMIT ?"), rankingType, period, limit)
	if err != nil {
		return nil, "", err
	}
	type rankingRow struct {
		itemID     string
		rank       int
		observedAt string
	}
	rowsData := make([]rankingRow, 0, limit)
	for rows.Next() {
		var value rankingRow
		if err := rows.Scan(&value.itemID, &value.rank, &value.observedAt); err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		rowsData = append(rowsData, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, "", err
	}
	if err := rows.Close(); err != nil {
		return nil, "", err
	}
	items := make([]RankedItem, 0, len(rowsData))
	observedAt := ""
	for _, row := range rowsData {
		if observedAt == "" {
			observedAt = row.observedAt
		}
		if libraryID != "" && !s.itemInLibrary(ctx, user, row.itemID, libraryID) {
			continue
		}
		item, err := s.catalog.GetItemForUser(ctx, row.itemID, user)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		items = append(items, RankedItem{Rank: row.rank, Item: item})
	}
	return items, observedAt, nil
}

func (s *Service) visibleDistributions(ctx context.Context, user auth.User, libraryID string) ([]DistributionBucket, []DistributionBucket, error) {
	subquery, args := s.visibleItemSubquery(user, libraryID)
	query := "SELECT visible.id, visible.source_rating, visible.year FROM (" + subquery + ") visible"
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, nil, err
	}
	ratingCounts := make([]int, 5)
	yearCounts := make(map[int]int)
	seenItems := make(map[string]struct{})
	for rows.Next() {
		var rating sql.NullFloat64
		var year sql.NullInt64
		var itemID string
		if err := rows.Scan(&itemID, &rating, &year); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if _, ok := seenItems[itemID]; ok {
			continue
		}
		seenItems[itemID] = struct{}{}
		if rating.Valid && rating.Float64 >= 0 {
			bucket := int(rating.Float64 / 2)
			if bucket > 4 {
				bucket = 4
			}
			ratingCounts[bucket]++
		}
		if year.Valid && year.Int64 >= 1900 && year.Int64 <= 2100 {
			decade := int(year.Int64/10) * 10
			yearCounts[decade]++
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	ratingLabels := []string{"0–2", "2–4", "4–6", "6–8", "8–10"}
	ratingDistribution := make([]DistributionBucket, 0, len(ratingCounts))
	for index, count := range ratingCounts {
		ratingDistribution = append(ratingDistribution, DistributionBucket{Key: strconv.Itoa(index), Label: ratingLabels[index], Count: count})
	}
	decades := make([]int, 0, len(yearCounts))
	for decade := range yearCounts {
		decades = append(decades, decade)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(decades)))
	yearDistribution := make([]DistributionBucket, 0, len(decades))
	for _, decade := range decades {
		yearDistribution = append(yearDistribution, DistributionBucket{Key: strconv.Itoa(decade), Label: fmt.Sprintf("%d年代", decade), Count: yearCounts[decade]})
	}
	return ratingDistribution, yearDistribution, nil
}

func (s *Service) userStateStatistics(ctx context.Context, user auth.User, libraryID string) (int, int, int64, error) {
	subquery, args := s.visibleItemSubquery(user, libraryID)
	query := "SELECT COUNT(DISTINCT CASE WHEN states.is_watched = 1 THEN states.item_id END), COUNT(DISTINCT CASE WHEN states.is_favorite = 1 THEN states.item_id END), COALESCE(SUM(states.playback_position_ticks), 0) FROM user_item_states states JOIN (" + subquery + ") visible ON visible.id = states.item_id"
	var watched, favorite int
	var ticks int64
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&watched, &favorite, &ticks); err != nil {
		return 0, 0, 0, err
	}
	return watched, favorite, ticks / 10000000, nil
}

func (s *Service) sourceHealthStatistics(ctx context.Context, user auth.User, libraryID string) (SourceHealthStats, error) {
	subquery, args := s.visibleItemSubquery(user, libraryID)
	query := "SELECT COUNT(CASE WHEN ps.status = 'active' THEN 1 END), COUNT(CASE WHEN ps.status = 'unavailable' THEN 1 END), COUNT(CASE WHEN ps.status = 'expired' THEN 1 END), COALESCE(SUM(ps.probe_count), 0), COALESCE(SUM(ps.probe_success_count), 0) FROM stream_sources ps JOIN (" + subquery + ") visible ON visible.id = ps.item_id"
	var result SourceHealthStats
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&result.Active, &result.Unavailable, &result.Expired, &result.ProbeCount, &result.ProbeSuccessCount); err != nil {
		return SourceHealthStats{}, err
	}
	if result.ProbeCount > 0 {
		result.SuccessRate = float64(result.ProbeSuccessCount) / float64(result.ProbeCount)
	}
	return result, nil
}

func (s *Service) yearStatistics(ctx context.Context, user auth.User, libraryID string, limit int) ([]DistributionBucket, error) {
	subquery, args := s.visibleItemSubquery(user, libraryID)
	query := "SELECT vi.year, COUNT(*) FROM media_items vi JOIN (" + subquery + ") visible ON visible.id = vi.id WHERE vi.year IS NOT NULL GROUP BY vi.year ORDER BY vi.year DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DistributionBucket, 0, limit)
	for rows.Next() {
		var year, count int
		if err := rows.Scan(&year, &count); err != nil {
			return nil, err
		}
		result = append(result, DistributionBucket{Key: strconv.Itoa(year), Label: strconv.Itoa(year), Count: count})
	}
	return result, rows.Err()
}

func (s *Service) entityStatistics(ctx context.Context, user auth.User, libraryID, entityTable, relationTable, relationID string, limit int) ([]StatisticsEntityBucket, error) {
	allowed := map[string]struct {
		table string
		link  string
		id    string
	}{
		"people":  {"people", "item_people", "person_id"},
		"studios": {"studios", "item_studios", "studio_id"},
		"series":  {"series", "item_series", "series_id"},
		"tags":    {"tags", "item_tags", "tag_id"},
	}
	definition, ok := allowed[entityTable]
	if !ok || definition.link != relationTable || definition.id != relationID {
		return nil, fmt.Errorf("不支持的统计关系")
	}
	subquery, args := s.visibleItemSubquery(user, libraryID)
	query := "SELECT entity.id, entity.name, COUNT(DISTINCT visible.id) FROM " + definition.table + " entity JOIN " + definition.link + " relation ON relation." + definition.id + " = entity.id JOIN (" + subquery + ") visible ON visible.id = relation.item_id WHERE entity.is_excluded = 0 GROUP BY entity.id, entity.name ORDER BY COUNT(DISTINCT visible.id) DESC, entity.name, entity.id LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]StatisticsEntityBucket, 0, limit)
	for rows.Next() {
		var value StatisticsEntityBucket
		if err := rows.Scan(&value.ID, &value.Name, &value.Count); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Service) visibleItemSubquery(user auth.User, libraryID string) (string, []any) {
	query := "SELECT DISTINCT vi.id, vi.source_rating, vi.year FROM media_items vi JOIN library_items li ON li.item_id = vi.id JOIN virtual_libraries vl ON vl.id = li.library_id AND vl.enabled = 1"
	args := make([]any, 0, 2)
	if !isElevated(user) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", user.ID, false, false)
		query += " AND (" + visibility + ")"
		args = append(args, accessArgs...)
	}
	query += " WHERE vi.deleted_at IS NULL"
	if libraryID != "" {
		query += " AND li.library_id = ?"
		args = append(args, libraryID)
	}
	return query, args
}

func (s *Service) dashboard(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	libraryID := strings.TrimSpace(r.URL.Query().Get("libraryId"))
	limit := parseLimit(r.URL.Query().Get("limit"), 12, 24)
	preferences, err := s.auth.GetPreferences(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "HOME_PREFERENCES_READ_FAILED", "读取首页偏好失败")
		return
	}
	enabledModules := enabledHomeModules(preferences.ModuleFlags)

	moduleQueries := []struct {
		key, title, order string
	}{
		{"recentSync", "最近同步", "COALESCE(vi.last_synced_at, vi.updated_at) DESC, vi.id DESC"},
		{"recentRelease", "最近发行", "CASE WHEN vi.release_date IS NULL OR vi.release_date = '' THEN 1 ELSE 0 END, vi.release_date DESC, vi.id DESC"},
		{"topRated", "高分作品", "CASE WHEN vi.source_rating IS NULL THEN 1 ELSE 0 END, vi.source_rating DESC, vi.rating_count DESC, vi.id DESC"},
		{"popular", "热门作品", "(SUM(CASE WHEN us.is_watched = 1 THEN 2 ELSE 0 END) + SUM(CASE WHEN us.is_favorite = 1 THEN 1 ELSE 0 END)) DESC, vi.updated_at DESC, vi.id DESC"},
		{"playable", "有在线源", "vi.updated_at DESC, vi.id DESC"},
		{"recentlyPlayed", "最近播放", "MAX(us.last_played_at) DESC, vi.id DESC"},
	}
	modules := make(map[string]Module, len(moduleQueries))
	for _, module := range moduleQueries {
		if !enabledModules[module.key] {
			continue
		}
		ids, err := s.visibleItemIDs(r.Context(), principal.User, libraryID, module.key, module.order, limit)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "HOME_READ_FAILED", "读取首页聚合失败")
			return
		}
		items, err := s.loadItems(r.Context(), principal.User, ids)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "HOME_READ_FAILED", "读取首页作品失败")
			return
		}
		modules[module.key] = Module{Title: module.title, Items: items}
	}
	rankings, err := s.currentRankings(r.Context(), principal.User, libraryID, limit, enabledModules)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "HOME_RANKINGS_READ_FAILED", "读取首页榜单失败")
		return
	}
	stats, err := s.stats(r.Context(), principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "HOME_STATS_READ_FAILED", "读取首页统计失败")
		return
	}
	writeJSON(w, http.StatusOK, Dashboard{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), LibraryID: libraryID, Modules: modules, Rankings: rankings, Stats: stats})
}

func enabledHomeModules(flags map[string]any) map[string]bool {
	keys := []string{"recentSync", "recentRelease", "topRated", "popular", "playable", "recentlyPlayed", "ranking", "playback", "top250", "hot"}
	enabled := make(map[string]bool, len(keys))
	for _, key := range keys {
		enabled[key] = true
		value, ok := flags[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			enabled[key] = typed
		case string:
			enabled[key] = !strings.EqualFold(strings.TrimSpace(typed), "false")
		case float64:
			enabled[key] = typed != 0
		default:
			enabled[key] = true
		}
	}
	return enabled
}

func (s *Service) adminDashboard(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	libraryID := strings.TrimSpace(r.URL.Query().Get("libraryId"))
	ctx := r.Context()

	resources, err := s.dashboardResourceCounts(ctx, principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DASHBOARD_READ_FAILED", "读取资源统计失败")
		return
	}
	tasks, err := s.dashboardTaskSummaries(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DASHBOARD_READ_FAILED", "读取任务统计失败")
		return
	}
	recentTasks, err := s.dashboardRecentTasks(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DASHBOARD_READ_FAILED", "读取最近任务失败")
		return
	}
	errorsSummary, recentErrors, err := s.dashboardErrors(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DASHBOARD_READ_FAILED", "读取错误统计失败")
		return
	}
	providerHealth, err := s.dashboardProviderHealth(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DASHBOARD_READ_FAILED", "读取 Provider 健康状态失败")
		return
	}
	online, err := s.dashboardOnline(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "DASHBOARD_READ_FAILED", "读取在线状态失败")
		return
	}
	health := DashboardHealth{Database: "healthy", Service: "healthy"}
	if err := s.db.Ready(ctx); err != nil {
		health.Database = "unhealthy"
	}
	writeJSON(w, http.StatusOK, AdminDashboard{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		LibraryID:      libraryID,
		Resources:      resources,
		Tasks:          tasks,
		RecentTasks:    recentTasks,
		Errors:         errorsSummary,
		RecentErrors:   recentErrors,
		ProviderHealth: providerHealth,
		Online:         online,
		Health:         health,
	})
}

func (s *Service) statisticsOverview(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	libraryID := strings.TrimSpace(r.URL.Query().Get("libraryId"))
	ctx := r.Context()
	stats, err := s.stats(ctx, principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取统计资源失败")
		return
	}
	ratingDistribution, yearDistribution, err := s.visibleDistributions(ctx, principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取评分和年代统计失败")
		return
	}
	watchedItems, favoriteItems, watchedDuration, err := s.userStateStatistics(ctx, principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取用户状态统计失败")
		return
	}
	sourceHealth, err := s.sourceHealthStatistics(ctx, principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取播放源统计失败")
		return
	}
	writeJSON(w, http.StatusOK, StatisticsOverview{
		GeneratedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		LibraryID:             libraryID,
		Stats:                 stats,
		RatingDistribution:    ratingDistribution,
		YearDistribution:      yearDistribution,
		WatchedItems:          watchedItems,
		FavoriteItems:         favoriteItems,
		WatchedDurationSecond: watchedDuration,
		SourceHealth:          sourceHealth,
	})
}

func (s *Service) statisticsItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	libraryID := strings.TrimSpace(r.URL.Query().Get("libraryId"))
	limit := parseLimit(r.URL.Query().Get("limit"), 20, 100)
	ctx := r.Context()
	stats, err := s.stats(ctx, principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取统计资源失败")
		return
	}
	byYear, err := s.yearStatistics(ctx, principal.User, libraryID, limit)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取年代统计失败")
		return
	}
	ratingDistribution, _, err := s.visibleDistributions(ctx, principal.User, libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取评分统计失败")
		return
	}
	people, err := s.entityStatistics(ctx, principal.User, libraryID, "people", "item_people", "person_id", limit)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取演员统计失败")
		return
	}
	studios, err := s.entityStatistics(ctx, principal.User, libraryID, "studios", "item_studios", "studio_id", limit)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取片商统计失败")
		return
	}
	series, err := s.entityStatistics(ctx, principal.User, libraryID, "series", "item_series", "series_id", limit)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取系列统计失败")
		return
	}
	tags, err := s.entityStatistics(ctx, principal.User, libraryID, "tags", "item_tags", "tag_id", limit)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATISTICS_READ_FAILED", "读取标签统计失败")
		return
	}
	writeJSON(w, http.StatusOK, StatisticsItemsResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		LibraryID:   libraryID,
		Limit:       limit,
		Stats:       stats,
		ByYear:      byYear,
		ByRating:    ratingDistribution,
		People:      people,
		Studios:     studios,
		Series:      series,
		Tags:        tags,
	})
}

func (s *Service) dashboardResourceCounts(ctx context.Context, user auth.User, libraryID string) (DashboardResourceCounts, error) {
	stats, err := s.stats(ctx, user, libraryID)
	if err != nil {
		return DashboardResourceCounts{}, err
	}
	resources := DashboardResourceCounts{
		Items:         stats.VisibleItems,
		PlayableItems: stats.PlayableItems,
		People:        stats.People,
		Studios:       stats.Studios,
		Series:        stats.Series,
		Tags:          stats.Tags,
	}
	for query, target := range map[string]*int{
		"SELECT COUNT(*) FROM collections":                         &resources.Collections,
		"SELECT COUNT(*) FROM virtual_libraries WHERE enabled = 1": &resources.Libraries,
		"SELECT COUNT(*) FROM online_providers WHERE enabled = 1":  &resources.Providers,
	} {
		if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query)).Scan(target); err != nil {
			return DashboardResourceCounts{}, err
		}
	}
	subquery, args := s.visibleItemSubquery(user, libraryID)
	availableSQL, sourceArgs := s.catalog.AvailableSourceSQL("ps", "vi")
	query := "SELECT COUNT(*) FROM stream_sources ps JOIN media_items vi ON vi.id=ps.item_id JOIN (" + subquery + ") visible ON visible.id = ps.item_id WHERE " + availableSQL
	args = append(args, sourceArgs...)
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&resources.PlaybackSources); err != nil {
		return DashboardResourceCounts{}, err
	}
	return resources, nil
}

func (s *Service) dashboardTaskSummaries(ctx context.Context) ([]DashboardTaskSummary, error) {
	statuses := []string{"queued", "running", "succeeded", "partial", "failed", "cancelled"}
	counts := make(map[string]int, len(statuses))
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT status, COUNT(*) FROM tasks GROUP BY status"))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			_ = rows.Close()
			return nil, err
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]DashboardTaskSummary, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, DashboardTaskSummary{Status: status, Count: counts[status]})
	}
	return result, nil
}

func (s *Service) dashboardRecentTasks(ctx context.Context) ([]DashboardRecentTask, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, type, status, progress, COALESCE(error_code, ''), COALESCE(error_message, ''), created_at, updated_at FROM tasks ORDER BY created_at DESC, id DESC LIMIT 10"))
	if err != nil {
		return nil, err
	}
	result := make([]DashboardRecentTask, 0, 10)
	for rows.Next() {
		var task DashboardRecentTask
		if err := rows.Scan(&task.ID, &task.Type, &task.Status, &task.Progress, &task.ErrorCode, &task.ErrorMessage, &task.CreatedAt, &task.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return result, rows.Close()
}

func (s *Service) dashboardErrors(ctx context.Context) (DashboardErrorSummary, []DashboardRecentError, error) {
	var taskFailures, auditFailures int
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM tasks WHERE status IN ('failed', 'partial')")).Scan(&taskFailures); err != nil {
		return DashboardErrorSummary{}, nil, err
	}
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM audit_logs WHERE result <> 'success'")).Scan(&auditFailures); err != nil {
		return DashboardErrorSummary{}, nil, err
	}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT 'task', id, COALESCE(error_code, ''), COALESCE(error_message, ''), updated_at, '', '' FROM tasks WHERE status IN ('failed', 'partial') ORDER BY updated_at DESC, id DESC LIMIT 10"))
	if err != nil {
		return DashboardErrorSummary{}, nil, err
	}
	errorsList := make([]DashboardRecentError, 0, 10)
	for rows.Next() {
		var value DashboardRecentError
		if err := rows.Scan(&value.Source, &value.ID, &value.Code, &value.Message, &value.OccurredAt, &value.ResourceType, &value.ResourceID); err != nil {
			_ = rows.Close()
			return DashboardErrorSummary{}, nil, err
		}
		errorsList = append(errorsList, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return DashboardErrorSummary{}, nil, err
	}
	if err := rows.Close(); err != nil {
		return DashboardErrorSummary{}, nil, err
	}
	auditRows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT 'audit', id, result, '', created_at, resource_type, COALESCE(resource_id, '') FROM audit_logs WHERE result <> 'success' ORDER BY created_at DESC, id DESC LIMIT 10"))
	if err != nil {
		return DashboardErrorSummary{}, nil, err
	}
	for auditRows.Next() {
		var value DashboardRecentError
		if err := auditRows.Scan(&value.Source, &value.ID, &value.Code, &value.Message, &value.OccurredAt, &value.ResourceType, &value.ResourceID); err != nil {
			_ = auditRows.Close()
			return DashboardErrorSummary{}, nil, err
		}
		errorsList = append(errorsList, value)
	}
	if err := auditRows.Err(); err != nil {
		_ = auditRows.Close()
		return DashboardErrorSummary{}, nil, err
	}
	if err := auditRows.Close(); err != nil {
		return DashboardErrorSummary{}, nil, err
	}
	sort.SliceStable(errorsList, func(i, j int) bool { return errorsList[i].OccurredAt > errorsList[j].OccurredAt })
	if len(errorsList) > 10 {
		errorsList = errorsList[:10]
	}
	return DashboardErrorSummary{TaskFailures: taskFailures, AuditFailures: auditFailures, Total: taskFailures + auditFailures}, errorsList, nil
}

func (s *Service) dashboardOnline(ctx context.Context) (DashboardOnlineSummary, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var online DashboardOnlineSummary
	query := "SELECT COUNT(*) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.status = 'active' AND s.revoked_at IS NULL AND s.expires_at > ?"
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), now).Scan(&online.Sessions); err != nil {
		return DashboardOnlineSummary{}, err
	}
	query = "SELECT COUNT(DISTINCT s.user_id) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.status = 'active' AND s.revoked_at IS NULL AND s.expires_at > ?"
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), now).Scan(&online.Users); err != nil {
		return DashboardOnlineSummary{}, err
	}
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM user_item_states WHERE playback_state = 'playing'")).Scan(&online.ActivePlayback); err != nil {
		return DashboardOnlineSummary{}, err
	}
	return online, nil
}

func (s *Service) dashboardProviderHealth(ctx context.Context) (DashboardProviderHealth, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT COALESCE(last_probe_status, '') FROM online_providers WHERE enabled = 1"))
	if err != nil {
		return DashboardProviderHealth{}, err
	}
	var result DashboardProviderHealth
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			_ = rows.Close()
			return DashboardProviderHealth{}, err
		}
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "healthy", "ok", "available":
			result.Healthy++
		case "unhealthy", "error", "failed":
			result.Unhealthy++
		default:
			result.Unknown++
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return DashboardProviderHealth{}, err
	}
	return result, rows.Close()
}

func (s *Service) visibleItemIDs(ctx context.Context, user auth.User, libraryID, module, order string, limit int) ([]string, error) {
	query := "SELECT vi.id FROM media_items vi JOIN library_items li ON li.item_id = vi.id JOIN virtual_libraries vl ON vl.id = li.library_id AND vl.enabled = 1"
	args := make([]any, 0, 4)
	if !isElevated(user) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", user.ID, false, false)
		query += " AND (" + visibility + ")"
		args = append(args, accessArgs...)
	}
	if module == "popular" || module == "recentlyPlayed" {
		query += " LEFT JOIN user_item_states us ON us.item_id = vi.id"
		if module == "recentlyPlayed" {
			query += " AND us.user_id = ?"
			args = append(args, user.ID)
		}
	}
	if libraryID != "" {
		query += " WHERE li.library_id = ? AND vi.deleted_at IS NULL"
		args = append(args, libraryID)
	} else {
		query += " WHERE vi.deleted_at IS NULL"
	}
	switch module {
	case "recentlyPlayed":
		query += " AND us.last_played_at IS NOT NULL"
	case "topRated":
		query += " AND vi.source_rating IS NOT NULL"
	case "playable":
		clause, sourceArgs := s.catalog.HasAvailableSourceSQL("vi")
		query += " AND (" + clause + ")"
		args = append(args, sourceArgs...)
	}
	query += " GROUP BY vi.id ORDER BY " + order + " LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) loadItems(ctx context.Context, user auth.User, ids []string) ([]catalog.Item, error) {
	items := make([]catalog.Item, 0, len(ids))
	for _, id := range ids {
		item, err := s.catalog.GetItemForUser(ctx, id, user)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) currentRankings(ctx context.Context, user auth.User, libraryID string, limit int, enabled map[string]bool) (map[string][]RankedItem, error) {
	types := make([]string, 0, 3)
	for _, rankingType := range []string{"ranking", "playback", "top250"} {
		if enabled[rankingType] || (rankingType == "playback" && enabled["hot"]) {
			types = append(types, rankingType)
		}
	}
	result := make(map[string][]RankedItem)
	if len(types) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(types))
	args := make([]any, 0, len(types)+1)
	for index, rankingType := range types {
		placeholders[index] = "?"
		args = append(args, rankingType)
	}
	args = append(args, limit*5)
	query := "SELECT DISTINCT ranking_type, item_id, rank FROM item_rankings WHERE ranking_type IN (" + strings.Join(placeholders, ",") + ") AND (period = 'daily' OR period = 'all') AND is_current = 1 ORDER BY ranking_type, rank LIMIT ?"
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	type rankingRow struct {
		rankingType string
		itemID      string
		rank        int
	}
	rowsData := make([]rankingRow, 0, limit*5)
	for rows.Next() {
		var rankingType, itemID string
		var rank int
		if err := rows.Scan(&rankingType, &itemID, &rank); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rowsData = append(rowsData, rankingRow{rankingType: rankingType, itemID: itemID, rank: rank})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, row := range rowsData {
		if len(result[row.rankingType]) >= limit {
			continue
		}
		if libraryID != "" && !s.itemInLibrary(ctx, user, row.itemID, libraryID) {
			continue
		}
		item, err := s.catalog.GetItemForUser(ctx, row.itemID, user)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[row.rankingType] = append(result[row.rankingType], RankedItem{Rank: row.rank, Item: item})
	}
	if pb, ok := result["playback"]; ok {
		result["hot"] = pb
	}
	return result, nil
}

func (s *Service) itemInLibrary(ctx context.Context, user auth.User, itemID, libraryID string) bool {
	query := "SELECT COUNT(*) FROM library_items li JOIN media_items vi ON vi.id=li.item_id JOIN virtual_libraries vl ON vl.id = li.library_id AND vl.enabled = 1"
	args := make([]any, 0, 3)
	if !isElevated(user) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", user.ID, false, false)
		query += " AND (" + visibility + ")"
		args = append(args, accessArgs...)
	}
	query += " WHERE li.item_id = ? AND li.library_id = ?"
	args = append(args, itemID, libraryID)
	var count int
	return s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&count) == nil && count > 0
}

func (s *Service) stats(ctx context.Context, user auth.User, libraryID string) (DashboardStats, error) {
	visible, err := s.countVisibleItems(ctx, user, libraryID, false)
	if err != nil {
		return DashboardStats{}, err
	}
	playable, err := s.countVisibleItems(ctx, user, libraryID, true)
	if err != nil {
		return DashboardStats{}, err
	}
	stats := DashboardStats{VisibleItems: visible, PlayableItems: playable}
	for table, relation := range map[string]struct {
		relationTable string
		relationID    string
		target        *int
	}{
		"people":  {"item_people", "person_id", &stats.People},
		"studios": {"item_studios", "studio_id", &stats.Studios},
		"series":  {"item_series", "series_id", &stats.Series},
		"tags":    {"item_tags", "tag_id", &stats.Tags},
	} {
		count, err := s.countVisibleRelations(ctx, user, libraryID, table, relation.relationTable, relation.relationID)
		if err != nil {
			return DashboardStats{}, err
		}
		*relation.target = count
	}
	return stats, nil
}

func (s *Service) countVisibleItems(ctx context.Context, user auth.User, libraryID string, playable bool) (int, error) {
	query := "SELECT COUNT(DISTINCT vi.id) FROM media_items vi JOIN library_items li ON li.item_id = vi.id JOIN virtual_libraries vl ON vl.id = li.library_id AND vl.enabled = 1"
	args := make([]any, 0, 3)
	if !isElevated(user) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", user.ID, false, false)
		query += " AND (" + visibility + ")"
		args = append(args, accessArgs...)
	}
	query += " WHERE vi.deleted_at IS NULL"
	if libraryID != "" {
		query += " AND li.library_id = ?"
		args = append(args, libraryID)
	}
	if playable {
		clause, sourceArgs := s.catalog.HasAvailableSourceSQL("vi")
		query += " AND (" + clause + ")"
		args = append(args, sourceArgs...)
	}
	var count int
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) countVisibleRelations(ctx context.Context, user auth.User, libraryID, table, relationTable, relationID string) (int, error) {
	query := "SELECT COUNT(DISTINCT rel.id) FROM " + table + " rel JOIN " + relationTable + " rel_link ON rel_link." + relationID + " = rel.id JOIN library_items li ON li.item_id = rel_link.item_id JOIN media_items vi ON vi.id = li.item_id JOIN virtual_libraries vl ON vl.id = li.library_id AND vl.enabled = 1"
	args := make([]any, 0, 3)
	if !isElevated(user) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", user.ID, false, false)
		query += " AND (" + visibility + ")"
		args = append(args, accessArgs...)
	}
	query += " WHERE vi.deleted_at IS NULL"
	if libraryID != "" {
		query += " AND li.library_id = ?"
		args = append(args, libraryID)
	}
	var count int
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) adminRanking(w http.ResponseWriter, r *http.Request) {
	rankingType, err := normalizeRankingType(chi.URLParam(r, "rankingType"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_TYPE", err.Error())
		return
	}
	period, err := normalizeRankingPeriod(rankingType, r.URL.Query().Get("period"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_PERIOD", err.Error())
		return
	}
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT item_id, rank, observed_at FROM item_rankings WHERE ranking_type = ? AND period = ? AND is_current = 1 ORDER BY rank LIMIT 250"), rankingType, period)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "RANKING_READ_FAILED", "读取榜单失败")
		return
	}
	defer rows.Close()
	items := make([]rankingInput, 0)
	var observedAt string
	for rows.Next() {
		var value rankingInput
		if err := rows.Scan(&value.ItemID, &value.Rank, &observedAt); err != nil {
			writeError(w, r, http.StatusInternalServerError, "RANKING_READ_FAILED", "读取榜单失败")
			return
		}
		items = append(items, value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rankingType": rankingType, "period": period, "observedAt": observedAt, "items": items})
}

func (s *Service) replaceRanking(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	rankingType, err := normalizeRankingType(chi.URLParam(r, "rankingType"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_TYPE", err.Error())
		return
	}
	period, err := normalizeRankingPeriod(rankingType, r.URL.Query().Get("period"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_PERIOD", err.Error())
		return
	}
	var input struct {
		Items []rankingInput `json:"items"`
	}
	if err := decodeJSON(r, &input); err != nil || len(input.Items) > 250 {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING", "榜单最多包含 250 项")
		return
	}
	seenItems, seenRanks := map[string]struct{}{}, map[int]struct{}{}
	for _, item := range input.Items {
		if strings.TrimSpace(item.ItemID) == "" || item.Rank < 1 || item.Rank > 250 {
			writeError(w, r, http.StatusBadRequest, "INVALID_RANKING", "作品 ID 和排名必须有效")
			return
		}
		if _, ok := seenItems[item.ItemID]; ok {
			writeError(w, r, http.StatusBadRequest, "INVALID_RANKING", "榜单不能重复作品")
			return
		}
		if _, ok := seenRanks[item.Rank]; ok {
			writeError(w, r, http.StatusBadRequest, "INVALID_RANKING", "榜单不能重复排名")
			return
		}
		seenItems[item.ItemID], seenRanks[item.Rank] = struct{}{}, struct{}{}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "RANKING_WRITE_FAILED", "保存榜单失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE item_rankings SET is_current = 0 WHERE ranking_type = ? AND period = ? AND is_current = 1"), rankingType, period); err != nil {
		writeError(w, r, http.StatusBadRequest, "RANKING_WRITE_FAILED", "保存榜单失败")
		return
	}
	for _, item := range input.Items {
		var exists int
		if err := tx.QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM media_items WHERE id = ? AND deleted_at IS NULL"), item.ItemID).Scan(&exists); err != nil || exists != 1 {
			writeError(w, r, http.StatusBadRequest, "RANKING_ITEM_NOT_FOUND", "榜单包含不存在的作品")
			return
		}
		if _, err := tx.ExecContext(r.Context(), s.db.Rebind("INSERT INTO item_rankings (item_id, ranking_type, period, rank, observed_at, is_current, created_at) VALUES (?, ?, ?, ?, ?, 1, ?)"), item.ItemID, rankingType, period, item.Rank, now, now); err != nil {
			writeError(w, r, http.StatusBadRequest, "RANKING_WRITE_FAILED", "保存榜单失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "RANKING_WRITE_FAILED", "保存榜单失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "replace_ranking", "ranking", rankingType+":"+period, middleware.GetReqID(r.Context()), fmt.Sprintf(`{"count":%d}`, len(input.Items)))
	writeJSON(w, http.StatusOK, map[string]any{"rankingType": rankingType, "period": period, "count": len(input.Items), "observedAt": now})
}

func (s *Service) audit(ctx context.Context, actor, action, resource, resourceID, requestID, metadata string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, 'success', ?, ?)"), "home_"+strconv.FormatInt(time.Now().UnixNano(), 36), actor, action, resource, resourceID, requestID, metadata, time.Now().UTC().Format(time.RFC3339Nano))
}

func normalizeRankingType(value string) (string, error)         { return ranking.Type(value) }
func normalizeRankingPeriod(kind, value string) (string, error) { return ranking.Period(kind, value) }

func parseLimit(raw string, fallback, maximum int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func isElevated(user auth.User) bool {
	for _, role := range user.Roles {
		if role == "Admin" || role == "Editor" {
			return true
		}
	}
	return false
}

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
