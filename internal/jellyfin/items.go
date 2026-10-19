package jellyfin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
)

func (s *Service) items(w http.ResponseWriter, r *http.Request) {
	s.serveItems(w, r, false)
}

func (s *Service) serveItems(w http.ResponseWriter, r *http.Request, latest bool) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if userID := chi.URLParam(r, "userID"); userID != "" && !s.ownsUserRoute(w, r, principal.User.ID) {
		return
	}
	query := `SELECT vi.id FROM media_items vi`
	if !isElevated(principal.User) {
		query += ` JOIN library_items li ON li.item_id = vi.id JOIN virtual_libraries vl ON vl.id = li.library_id`
	}
	where := []string{"vi.deleted_at IS NULL"}
	args := make([]any, 0, 12)
	if !isElevated(principal.User) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", principal.User.ID, false, false)
		where = append(where, visibility)
		args = append(args, accessArgs...)
	}
	values := r.URL.Query()
	resumable := strings.HasSuffix(r.URL.Path, "/Items/Resume") || strings.Contains(strings.ToLower(firstValue(values, "Filters", "filters")), "isresumable")
	mediaParent := false
	recursive := resumable
	if raw := firstValue(values, "Recursive", "recursive"); raw != "" {
		var err error
		recursive, err = strconv.ParseBool(raw)
		if err != nil {
			writeJellyfinError(w, 400, "Recursive 必须是 true 或 false")
			return
		}
	}
	if seriesID := strings.TrimSpace(firstValue(values, "SeriesId", "seriesId")); seriesID != "" {
		if item, err := s.catalog.GetItemForUser(r.Context(), seriesID, principal.User); err != nil || item.ItemType != "series" {
			writeJellyfinError(w, 404, "剧集不可见")
			return
		}
		seasonVisible, seasonArgs := auth.MediaVisibilitySQL("season", principal.User.ID, isElevated(principal.User), false)
		where = append(where, "vi.parent_id IN (SELECT season.id FROM media_items season WHERE season.parent_id = ? AND season.item_type = 'season' AND ("+seasonVisible+"))")
		args = append(args, seriesID)
		args = append(args, seasonArgs...)
	}
	if parentID := strings.TrimSpace(firstValue(values, "ParentId", "parentId")); parentID != "" {
		var isLibrary int
		_ = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM virtual_libraries WHERE id = ?"), parentID).Scan(&isLibrary)
		if isLibrary == 1 {
			where = append(where, "li.library_id = ?")
			args = append(args, parentID)
			if isElevated(principal.User) {
				query += " JOIN library_items li ON li.item_id = vi.id"
			}
			if !recursive {
				where = append(where, "vi.parent_id IS NULL")
			}
		} else {
			mediaParent = true
			if _, err := s.catalog.GetItemForUser(r.Context(), parentID, principal.User); err != nil {
				writeJellyfinError(w, 404, "父级媒体不可见")
				return
			}
			if recursive {
				seasonVisible, seasonArgs := auth.MediaVisibilitySQL("season", principal.User.ID, isElevated(principal.User), false)
				where = append(where, "(vi.parent_id=? OR vi.parent_id IN (SELECT season.id FROM media_items season WHERE season.parent_id=? AND ("+seasonVisible+")))")
				args = append(args, parentID, parentID)
				args = append(args, seasonArgs...)
			} else {
				where = append(where, "vi.parent_id = ?")
				args = append(args, parentID)
			}
		}
	} else if !recursive && firstValue(values, "SeriesId", "seriesId") == "" {
		where = append(where, "vi.parent_id IS NULL")
	}
	if types := splitValues(firstValue(values, "IncludeItemTypes", "includeItemTypes")); len(types) > 0 {
		where = append(where, "vi.item_type IN ("+placeholders(len(types))+")")
		for _, kind := range types {
			args = append(args, strings.ToLower(kind))
		}
	}
	term := catalog.NormalizeSearchQuery(firstValue(values, "SearchTerm", "searchTerm", "q"))
	if term != "" {
		pattern := "%" + strings.ToLower(term) + "%"
		where = append(where, "(LOWER(vi.code) LIKE ? OR LOWER(vi.title) LIKE ? OR LOWER(COALESCE(vi.original_title, '')) LIKE ? OR EXISTS (SELECT 1 FROM item_people ip JOIN people p ON p.id = ip.person_id WHERE ip.item_id = vi.id AND LOWER(p.name) LIKE ?) OR EXISTS (SELECT 1 FROM item_studios ist JOIN studios st ON st.id = ist.studio_id WHERE ist.item_id = vi.id AND LOWER(st.name) LIKE ?))")
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	if yearValues := splitValues(firstValue(values, "Years", "years", "year")); len(yearValues) > 0 {
		conditions := make([]string, 0, len(yearValues))
		for _, raw := range yearValues {
			if year, err := strconv.Atoi(raw); err == nil && year >= 1900 && year <= 2100 {
				conditions = append(conditions, "vi.year = ?")
				args = append(args, year)
			}
		}
		if len(conditions) > 0 {
			where = append(where, "("+strings.Join(conditions, " OR ")+")")
		}
	}
	if genres := splitValues(firstValue(values, "Genres", "genres")); len(genres) > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id = vi.id AND t.name IN ("+placeholders(len(genres))+"))")
		for _, genre := range genres {
			args = append(args, genre)
		}
	}
	if tags := splitValues(firstValue(values, "Tags", "tags")); len(tags) > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id = vi.id AND t.name IN ("+placeholders(len(tags))+"))")
		for _, tag := range tags {
			args = append(args, tag)
		}
	}
	filters := strings.ToLower(firstValue(values, "Filters", "filters"))
	if strings.Contains(filters, "isfavorite") {
		where = append(where, "EXISTS (SELECT 1 FROM user_item_states uis WHERE uis.item_id = vi.id AND uis.user_id = ? AND uis.is_favorite = 1)")
		args = append(args, principal.User.ID)
	}
	if strings.Contains(filters, "isplayed") {
		where = append(where, "EXISTS (SELECT 1 FROM user_item_states uis WHERE uis.item_id = vi.id AND uis.user_id = ? AND uis.is_watched = 1)")
		args = append(args, principal.User.ID)
	}
	if strings.Contains(filters, "isunplayed") {
		where = append(where, "NOT EXISTS (SELECT 1 FROM user_item_states uis WHERE uis.item_id = vi.id AND uis.user_id = ? AND uis.is_watched = 1)")
		args = append(args, principal.User.ID)
	}
	if resumable {
		where = append(where, "vi.item_type IN ('movie','episode','video')", "EXISTS(SELECT 1 FROM user_item_states resume_state WHERE resume_state.item_id=vi.id AND resume_state.user_id=? AND resume_state.is_watched=0 AND resume_state.playback_position_ticks>0 AND (resume_state.playback_runtime_ticks<=0 OR resume_state.playback_position_ticks<resume_state.playback_runtime_ticks))")
		args = append(args, principal.User.ID)
	}
	query += " WHERE " + strings.Join(where, " AND ")
	sortColumns := map[string]string{"name": "vi.title", "sortname": "vi.title", "premieredate": "vi.release_date", "datecreated": "vi.created_at", "communityrating": "vi.source_rating", "productionyear": "vi.year", "indexnumber": "vi.index_number", "parentindexnumber": "(SELECT season.index_number FROM media_items season WHERE season.id=vi.parent_id)"}
	orderColumns := []string{}
	resumeOrder := false
	for _, field := range splitValues(firstValue(values, "SortBy", "sortBy")) {
		if column := sortColumns[strings.ToLower(field)]; column != "" && len(orderColumns) < 8 {
			orderColumns = append(orderColumns, column)
		}
	}
	if len(orderColumns) == 0 {
		if resumable {
			resumeOrder = true
			orderColumns = append(orderColumns, "(SELECT MAX(resume_order.last_played_at) FROM user_item_states resume_order WHERE resume_order.item_id=vi.id AND resume_order.user_id=?)")
		} else if firstValue(values, "SeriesId", "seriesId") != "" || recursive && mediaParent {
			orderColumns = append(orderColumns, sortColumns["parentindexnumber"], sortColumns["indexnumber"])
		} else if mediaParent {
			orderColumns = append(orderColumns, sortColumns["indexnumber"])
		} else {
			orderColumns = append(orderColumns, "vi.title")
		}
	}
	order := "ASC"
	if resumable && firstValue(values, "SortOrder", "sortOrder") == "" {
		order = "DESC"
	}
	if strings.EqualFold(firstValue(values, "SortOrder", "sortOrder"), "Descending") {
		order = "DESC"
	}
	query += " GROUP BY vi.id, vi.title, vi.code, vi.release_date, vi.created_at, vi.source_rating, vi.year, vi.parent_id, vi.index_number"
	var total int
	if !latest {
		if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM ("+query+") visible_items"), args...).Scan(&total); err != nil {
			writeJellyfinError(w, http.StatusInternalServerError, "统计作品失败")
			return
		}
	}
	if term != "" && strings.TrimSpace(firstValue(values, "SortBy", "sortBy")) == "" {
		query += " ORDER BY CASE WHEN UPPER(vi.code) = UPPER(?) THEN 0 WHEN UPPER(vi.code) LIKE UPPER(?) THEN 1 ELSE 2 END, vi.title ASC, vi.id"
		args = append(args, term, term+"%")
	} else {
		if resumeOrder {
			args = append(args, principal.User.ID)
		}
		for i, column := range orderColumns {
			orderColumns[i] = column + " " + order + " NULLS LAST"
		}
		query += " ORDER BY " + strings.Join(orderColumns, ", ") + ", vi.id"
	}
	start := parseNonNegative(firstValue(values, "StartIndex", "startIndex"), 0)
	limit := parseNonNegative(firstValue(values, "Limit", "limit"), 100)
	if limit == 0 || limit > 500 {
		limit = 100
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, start)
	ids, err := s.queryItemIDs(r.Context(), query, args)
	if err != nil {
		writeJellyfinError(w, http.StatusInternalServerError, "读取作品失败")
		return
	}
	items, err := s.batchBaseItems(r.Context(), ids, principal.User.ID)
	if err != nil {
		writeJellyfinError(w, 500, "读取媒体详情失败")
		return
	}
	if latest {
		writeJSON(w, http.StatusOK, items)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": total, "StartIndex": start})
}
