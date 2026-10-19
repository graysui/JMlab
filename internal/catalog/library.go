package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/graysui/JMlab/internal/module"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
)

func firstRaw(primary, fallback json.RawMessage) json.RawMessage {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// LibrarySort is the only sort shape accepted by virtual libraries. Keeping
// the SQL column mapping in Go prevents user supplied JSON from becoming SQL.
type LibrarySort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type LibraryPermission struct {
	SubjectType string `json:"subjectType"`
	SubjectID   string `json:"subjectId"`
	Name        string `json:"name,omitempty"`
	CanView     bool   `json:"canView"`
	CanPlay     bool   `json:"canPlay"`
}

type libraryExpression struct {
	op       string
	field    string
	values   []any
	children []*libraryExpression
}

var libraryFilterFields = map[string]struct{}{
	"actorIds":          {},
	"directorIds":       {},
	"studioIds":         {},
	"seriesIds":         {},
	"collectionIds":     {},
	"tagIds":            {},
	"excludeTagIds":     {},
	"years":             {},
	"minRating":         {},
	"isFavorite":        {},
	"isWatched":         {},
	"hasPlayableSource": {},
	"releaseDate":       {},
	"rankingType":       {},
	"rankingPeriod":     {},
	"metadataModuleId":  {},
	"streamModuleId":    {},
}

var librarySortColumns = map[string]string{
	"title":       "vi.title",
	"code":        "vi.code",
	"year":        "vi.year",
	"rating":      "vi.source_rating",
	"releaseDate": "vi.release_date",
	"updatedAt":   "vi.updated_at",
	"createdAt":   "vi.created_at",
}

func defaultLibrarySort() LibrarySort {
	return LibrarySort{Field: "updatedAt", Direction: "desc"}
}

func parseLibrarySort(raw json.RawMessage) (LibrarySort, string, error) {
	sortValue := defaultLibrarySort()
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		encoded, _ := json.Marshal(sortValue)
		return sortValue, string(encoded), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return LibrarySort{}, "", errors.New("sort 必须是对象")
	}
	for key := range object {
		if key != "field" && key != "direction" && key != "by" && key != "order" {
			return LibrarySort{}, "", fmt.Errorf("sort 不允许字段 %s", key)
		}
	}
	fieldRaw := object["field"]
	if len(fieldRaw) == 0 {
		fieldRaw = object["by"]
	}
	directionRaw := object["direction"]
	if len(directionRaw) == 0 {
		directionRaw = object["order"]
	}
	if len(fieldRaw) > 0 && json.Unmarshal(fieldRaw, &sortValue.Field) != nil {
		return LibrarySort{}, "", errors.New("sort.field 必须是字符串")
	}
	if len(directionRaw) > 0 && json.Unmarshal(directionRaw, &sortValue.Direction) != nil {
		return LibrarySort{}, "", errors.New("sort.direction 必须是字符串")
	}
	sortValue.Field = strings.TrimSpace(sortValue.Field)
	sortValue.Direction = strings.ToLower(strings.TrimSpace(sortValue.Direction))
	if _, ok := librarySortColumns[sortValue.Field]; !ok {
		return LibrarySort{}, "", fmt.Errorf("sort.field 不支持 %q", sortValue.Field)
	}
	if sortValue.Direction != "asc" && sortValue.Direction != "desc" {
		return LibrarySort{}, "", errors.New("sort.direction 只能是 asc 或 desc")
	}
	encoded, _ := json.Marshal(sortValue)
	return sortValue, string(encoded), nil
}

func parseLibraryFilter(raw json.RawMessage) (map[string]any, string, *libraryExpression, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) {
		return map[string]any{}, "{}", nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, "", nil, errors.New("filter 必须是 JSON 对象")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, "", nil, errors.New("filter 必须是 JSON 对象")
	}
	expr, normalized, err := parseLibraryExpression(object, 0)
	if err != nil {
		return nil, "", nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", nil, errors.New("filter 无法保存")
	}
	return normalized, string(encoded), expr, nil
}

func parseLibraryExpression(object map[string]any, depth int) (*libraryExpression, map[string]any, error) {
	if depth > 8 {
		return nil, nil, errors.New("filter 嵌套层级不能超过 8 层")
	}
	if len(object) == 0 {
		return nil, map[string]any{}, nil
	}
	logicalCount := 0
	for _, key := range []string{"all", "any", "not"} {
		if _, exists := object[key]; exists {
			logicalCount++
		}
	}
	if logicalCount > 0 {
		if logicalCount != 1 {
			return nil, nil, errors.New("filter 只能包含一个 all、any 或 not")
		}
		for key := range object {
			if key != "all" && key != "any" && key != "not" {
				return nil, nil, fmt.Errorf("逻辑过滤器不允许字段 %s", key)
			}
		}
		if raw, exists := object["not"]; exists {
			childObject, ok := raw.(map[string]any)
			if !ok {
				return nil, nil, errors.New("filter.not 必须是对象")
			}
			child, normalized, err := parseLibraryExpression(childObject, depth+1)
			if err != nil {
				return nil, nil, err
			}
			if child == nil {
				return nil, nil, errors.New("filter.not 不能为空")
			}
			return &libraryExpression{op: "not", children: []*libraryExpression{child}}, map[string]any{"not": normalized}, nil
		}
		key := "all"
		if _, exists := object["any"]; exists {
			key = "any"
		}
		rawChildren, ok := object[key].([]any)
		if !ok || len(rawChildren) == 0 || len(rawChildren) > 128 {
			return nil, nil, fmt.Errorf("filter.%s 必须是 1 到 128 项", key)
		}
		expr := &libraryExpression{op: key, children: make([]*libraryExpression, 0, len(rawChildren))}
		normalizedChildren := make([]any, 0, len(rawChildren))
		for _, rawChild := range rawChildren {
			childObject, ok := rawChild.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("filter.%s 的每一项必须是对象", key)
			}
			child, normalized, err := parseLibraryExpression(childObject, depth+1)
			if err != nil {
				return nil, nil, err
			}
			if child == nil {
				return nil, nil, errors.New("逻辑过滤器不能包含空条件")
			}
			expr.children = append(expr.children, child)
			normalizedChildren = append(normalizedChildren, normalized)
		}
		return expr, map[string]any{key: normalizedChildren}, nil
	}

	if rawField, exists := object["field"]; exists {
		field, ok := rawField.(string)
		if !ok {
			return nil, nil, errors.New("filter.field 必须是字符串")
		}
		field = strings.TrimSpace(field)
		if _, ok := libraryFilterFields[field]; !ok {
			return nil, nil, fmt.Errorf("filter 字段不支持 %q", field)
		}
		for key := range object {
			if key != "field" && key != "value" && key != "values" {
				return nil, nil, fmt.Errorf("filter 条件不允许字段 %s", key)
			}
		}
		value, exists := object["value"]
		if !exists {
			value, exists = object["values"]
		}
		if !exists {
			return nil, nil, errors.New("filter 条件缺少 value")
		}
		values, normalizedValue, err := normalizeLibraryLeaf(field, value)
		if err != nil {
			return nil, nil, err
		}
		return &libraryExpression{op: "leaf", field: field, values: values}, map[string]any{"field": field, "value": normalizedValue}, nil
	}

	// For authoring convenience, accept the compact form
	// {"years":[2024],"minRating":8}. It is normalized to an all tree.
	children := make([]*libraryExpression, 0, len(object))
	normalized := make(map[string]any, len(object))
	for field, value := range object {
		if _, ok := libraryFilterFields[field]; !ok {
			return nil, nil, fmt.Errorf("filter 字段不支持 %q", field)
		}
		values, normalizedValue, err := normalizeLibraryLeaf(field, value)
		if err != nil {
			return nil, nil, err
		}
		children = append(children, &libraryExpression{op: "leaf", field: field, values: values})
		normalized[field] = normalizedValue
	}
	if len(children) == 0 {
		return nil, map[string]any{}, nil
	}
	return &libraryExpression{op: "all", children: children}, normalized, nil
}

func normalizeLibraryLeaf(field string, raw any) ([]any, any, error) {
	switch field {
	case "metadataModuleId", "streamModuleId":
		value, ok := raw.(string)
		if !ok || len(value) < 3 || len(value) > 63 || strings.Trim(value, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" || value[0] == '-' || value[0] == '_' {
			return nil, nil, fmt.Errorf("filter.%s 必须是有效的模块 ID", field)
		}
		return []any{value}, value, nil
	case "actorIds", "directorIds", "studioIds", "seriesIds", "collectionIds", "tagIds", "excludeTagIds":
		values, err := normalizeStringValues(raw, 100)
		if err != nil {
			return nil, nil, fmt.Errorf("filter.%s %w", field, err)
		}
		return values, values, nil
	case "years":
		rawValues := raw
		if value, ok := raw.(json.Number); ok {
			rawValues = []any{value}
		}
		values, ok := rawValues.([]any)
		if !ok || len(values) == 0 || len(values) > 100 {
			return nil, nil, errors.New("filter.years 必须是 1 到 100 个年份")
		}
		normalized := make([]int, 0, len(values))
		compiled := make([]any, 0, len(values))
		for _, rawYear := range values {
			number, ok := rawYear.(json.Number)
			if !ok {
				return nil, nil, errors.New("filter.years 必须是整数")
			}
			year, err := strconv.Atoi(number.String())
			if err != nil || year < 1900 || year > 2100 {
				return nil, nil, errors.New("filter.years 必须在 1900 到 2100 之间")
			}
			normalized = append(normalized, year)
			compiled = append(compiled, year)
		}
		return compiled, normalized, nil
	case "minRating":
		number, ok := raw.(json.Number)
		if !ok {
			return nil, nil, errors.New("filter.minRating 必须是数字")
		}
		rating, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || rating < 0 || rating > 10 {
			return nil, nil, errors.New("filter.minRating 必须在 0 到 10 之间")
		}
		return []any{rating}, rating, nil
	case "isFavorite", "isWatched", "hasPlayableSource":
		value, ok := raw.(bool)
		if !ok {
			return nil, nil, fmt.Errorf("filter.%s 必须是布尔值", field)
		}
		return []any{value}, value, nil
	case "releaseDate":
		value, ok := raw.(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" || len(value) > 10 || !validReleaseDatePrefix(value) {
			return nil, nil, errors.New("filter.releaseDate 必须是 YYYY、YYYY-MM 或 YYYY-MM-DD")
		}
		return []any{value}, value, nil

	case "rankingType", "rankingPeriod":
		value, ok := raw.(string)
		value = strings.TrimSpace(value)
		if !ok || !module.ValidRankingValue(value) {
			return nil, nil, errors.New("榜单类型或周期标识无效")
		}
		return []any{value}, value, nil
	default:
		return nil, nil, fmt.Errorf("filter 字段不支持 %q", field)
	}
}

func normalizeStringValues(raw any, max int) ([]any, error) {
	if value, ok := raw.(string); ok {
		parts := splitFilterValues(value)
		raw = make([]any, 0, len(parts))
		for _, part := range parts {
			raw = append(raw.([]any), part)
		}
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 || len(values) > max {
		return nil, errors.New("必须是 1 到 100 个字符串")
	}
	result := make([]any, 0, len(values))
	seen := map[string]struct{}{}
	for _, rawValue := range values {
		value, ok := rawValue.(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" || len(value) > 128 {
			return nil, errors.New("只能包含非空字符串")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, errors.New("不能是空数组")
	}
	return result, nil
}

func validReleaseDatePrefix(value string) bool {
	switch len(value) {
	case 4:
		year, err := strconv.Atoi(value)
		return err == nil && year >= 1900 && year <= 2100
	case 7:
		_, err := time.Parse("2006-01", value)
		return err == nil
	case 10:
		_, err := time.Parse("2006-01-02", value)
		return err == nil
	default:
		return false
	}
}

func (e *libraryExpression) compile(userID, availableSQL string, availableArgs []any) (string, []any, error) {
	if e == nil {
		return "1 = 1", nil, nil
	}
	if e.op == "all" || e.op == "any" {
		parts := make([]string, 0, len(e.children))
		args := make([]any, 0)
		var rankingType, rankingPeriod *libraryExpression
		if e.op == "all" {
			var typeCount, periodCount int
			for _, child := range e.children {
				if child.field == "rankingType" {
					rankingType = child
					typeCount++
				}
				if child.field == "rankingPeriod" {
					rankingPeriod = child
					periodCount++
				}
			}
			if typeCount != 1 || periodCount != 1 {
				rankingType, rankingPeriod = nil, nil
			}
		}
		if rankingType != nil && rankingPeriod != nil {
			parts = append(parts, "EXISTS (SELECT 1 FROM item_rankings ir WHERE ir.item_id = vi.id AND ir.ranking_type = ? AND ir.period = ? AND ir.is_current = 1)")
			args = append(args, rankingType.values[0], rankingPeriod.values[0])
		}
		for _, child := range e.children {
			if rankingType != nil && rankingPeriod != nil && (child == rankingType || child == rankingPeriod) {
				continue
			}
			part, childArgs, err := child.compile(userID, availableSQL, availableArgs)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, "("+part+")")
			args = append(args, childArgs...)
		}
		if len(parts) == 0 {
			return "", nil, errors.New("逻辑过滤器不能为空")
		}
		return strings.Join(parts, map[string]string{"all": " AND ", "any": " OR "}[e.op]), args, nil
	}
	if e.op == "not" {
		part, args, err := e.children[0].compile(userID, availableSQL, availableArgs)
		if err != nil {
			return "", nil, err
		}
		return "NOT (" + part + ")", args, nil
	}
	if e.field == "hasPlayableSource" {
		availableSQL = "EXISTS (SELECT 1 FROM stream_sources available_source WHERE available_source.item_id=vi.id AND (" + availableSQL + "))"
		if !e.values[0].(bool) {
			availableSQL = "NOT (" + availableSQL + ")"
		}
		return availableSQL, append([]any(nil), availableArgs...), nil
	}
	if e.field == "streamModuleId" {
		return "EXISTS (SELECT 1 FROM stream_sources available_source WHERE available_source.item_id=vi.id AND available_source.module_id=? AND (" + availableSQL + "))", append([]any{e.values[0]}, availableArgs...), nil
	}
	return compileLibraryLeaf(e.field, e.values, userID)
}

func compileLibraryLeaf(field string, values []any, userID string) (string, []any, error) {
	placeholders := func(count int) string {
		parts := make([]string, count)
		for index := range parts {
			parts[index] = "?"
		}
		return strings.Join(parts, ", ")
	}
	switch field {
	case "metadataModuleId":
		return "vi.metadata_module_id = ?", []any{values[0]}, nil
	case "actorIds", "directorIds":
		args := append([]any{}, values...)
		return "EXISTS (SELECT 1 FROM item_people ip WHERE ip.item_id = vi.id AND ip.role = '" + map[string]string{"actorIds": "actor", "directorIds": "director"}[field] + "' AND ip.person_id IN (" + placeholders(len(values)) + "))", args, nil
	case "studioIds":
		return "EXISTS (SELECT 1 FROM item_studios ist WHERE ist.item_id = vi.id AND ist.studio_id IN (" + placeholders(len(values)) + "))", append([]any{}, values...), nil
	case "seriesIds":
		return "EXISTS (SELECT 1 FROM item_series ise WHERE ise.item_id = vi.id AND ise.series_id IN (" + placeholders(len(values)) + "))", append([]any{}, values...), nil
	case "collectionIds":
		return "EXISTS (SELECT 1 FROM collection_items ci WHERE ci.item_id = vi.id AND ci.collection_id IN (" + placeholders(len(values)) + "))", append([]any{}, values...), nil
	case "tagIds":
		return "EXISTS (SELECT 1 FROM item_tags it WHERE it.item_id = vi.id AND it.tag_id IN (" + placeholders(len(values)) + "))", append([]any{}, values...), nil
	case "excludeTagIds":
		return "NOT EXISTS (SELECT 1 FROM item_tags it WHERE it.item_id = vi.id AND it.tag_id IN (" + placeholders(len(values)) + "))", append([]any{}, values...), nil
	case "years":
		return "vi.year IN (" + placeholders(len(values)) + ")", append([]any{}, values...), nil
	case "minRating":
		return "vi.source_rating >= ?", append([]any{}, values[0]), nil
	case "releaseDate":
		return "vi.release_date LIKE ?", []any{fmt.Sprint(values[0]) + "%"}, nil
	case "rankingType":
		return "EXISTS (SELECT 1 FROM item_rankings ir WHERE ir.item_id = vi.id AND ir.ranking_type = ? AND ir.is_current = 1)", []any{values[0]}, nil
	case "rankingPeriod":
		return "EXISTS (SELECT 1 FROM item_rankings ir WHERE ir.item_id = vi.id AND ir.period = ? AND ir.is_current = 1)", []any{values[0]}, nil
	case "isFavorite", "isWatched":
		if strings.TrimSpace(userID) == "" {
			return "", nil, errors.New("用户状态过滤必须绑定当前用户")
		}
		if values[0].(bool) {
			return "EXISTS (SELECT 1 FROM user_item_states uis WHERE uis.item_id = vi.id AND uis.user_id = ? AND uis." + map[string]string{"isFavorite": "is_favorite", "isWatched": "is_watched"}[field] + " = 1)", []any{userID}, nil
		}
		return "NOT EXISTS (SELECT 1 FROM user_item_states uis WHERE uis.item_id = vi.id AND uis.user_id = ? AND uis." + map[string]string{"isFavorite": "is_favorite", "isWatched": "is_watched"}[field] + " = 1)", []any{userID}, nil
	default:
		return "", nil, fmt.Errorf("filter 字段不支持 %q", field)
	}
}

func (s *Service) libraryFromRow(scanner interface{ Scan(...any) error }) (Library, error) {
	var library Library
	var public, adult, enabled int
	var filterJSON, sortJSON, poster sql.NullString
	if err := scanner.Scan(&library.ID, &library.Name, &library.Description, &library.ItemType, &filterJSON, &sortJSON, &poster, &public, &adult, &enabled); err != nil {
		return Library{}, err
	}
	library.Filter = decodeLibraryJSON(filterJSON.String, map[string]any{})
	library.Sort = decodeLibrarySort(sortJSON.String)
	library.PosterImageID = poster.String
	library.IsPublic, library.IsAdult, library.Enabled = public == 1, adult == 1, enabled == 1
	return library, nil
}

func decodeLibraryJSON(raw string, fallback any) any {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return fallback
	}
	return value
}

func decodeLibrarySort(raw string) LibrarySort {
	value, _, err := parseLibrarySort(json.RawMessage(raw))
	if err != nil {
		return defaultLibrarySort()
	}
	return value
}

func (s *Service) libraryVisible(ctx context.Context, libraryID string, user auth.User, requireEnabled bool) (Library, bool, error) {
	query := `SELECT vl.id, vl.name, COALESCE(vl.description, ''), vl.item_type, vl.filter_json, vl.sort_json,
COALESCE(vl.poster_image_id, ''), vl.is_public, vl.is_adult, vl.enabled FROM virtual_libraries vl WHERE vl.id = ?`
	args := []any{libraryID}
	if requireEnabled || !isAdmin(user) {
		query += " AND vl.enabled = 1"
	}
	if !isAdmin(user) {
		query += ` AND (vl.is_public = 1 OR EXISTS (
SELECT 1 FROM user_library_permissions ulp WHERE ulp.library_id = vl.id AND ulp.user_id = ? AND ulp.can_view = 1
) OR EXISTS (
SELECT 1 FROM user_roles ur JOIN role_library_permissions rlp ON rlp.role_id = ur.role_id
WHERE ur.user_id = ? AND rlp.library_id = vl.id AND rlp.can_view = 1
)) AND (vl.is_adult = 0 OR EXISTS (
SELECT 1 FROM user_preferences up WHERE up.user_id = ? AND up.adult_confirmed_at IS NOT NULL AND up.adult_confirmed_at <> ''
))`
		args = append(args, user.ID, user.ID, user.ID)
	}
	var row Library
	var public, adult, enabled int
	var filterJSON, sortJSON, poster sql.NullString
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&row.ID, &row.Name, &row.Description, &row.ItemType, &filterJSON, &sortJSON, &poster, &public, &adult, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, false, nil
	}
	if err != nil {
		return Library{}, false, err
	}
	row.Filter = decodeLibraryJSON(filterJSON.String, map[string]any{})
	row.Sort = decodeLibrarySort(sortJSON.String)
	row.PosterImageID = poster.String
	row.IsPublic, row.IsAdult, row.Enabled = public == 1, adult == 1, enabled == 1
	return row, true, nil
}

func isAdmin(user auth.User) bool {
	if user.IsAdmin {
		return true
	}
	for _, role := range user.Roles {
		if role == "Admin" {
			return true
		}
	}
	return false
}

func (s *Service) listLibraryItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	library, visible, err := s.libraryVisible(r.Context(), chi.URLParam(r, "libraryID"), principal.User, true)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_READ_FAILED", "读取媒体库失败")
		return
	}
	if !visible {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "媒体库不存在或不可见")
		return
	}
	items, nextCursor, err := s.queryLibraryItems(r.Context(), library, principal.User, r.URL.Query())
	if err != nil {
		if errors.Is(err, errInvalidBrowse) {
			writeError(w, r, http.StatusBadRequest, "INVALID_BROWSE", err.Error())
			return
		}
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_ITEMS_READ_FAILED", "读取媒体库作品失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"library": library, "items": items, "nextCursor": nextCursor})
}

func (s *Service) getLibrary(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	library, visible, err := s.libraryVisible(r.Context(), chi.URLParam(r, "libraryID"), principal.User, false)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_READ_FAILED", "读取媒体库失败")
		return
	}
	if !visible {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "媒体库不存在或不可见")
		return
	}
	if isAdmin(principal.User) {
		library.Permissions, _ = s.readLibraryPermissions(r.Context(), library.ID)
	}
	writeJSON(w, http.StatusOK, library)
}

func (s *Service) previewLibrary(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	library, visible, err := s.libraryVisible(r.Context(), chi.URLParam(r, "libraryID"), principal.User, false)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_READ_FAILED", "读取媒体库失败")
		return
	}
	if !visible {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "媒体库不存在或不可见")
		return
	}
	where, args, err := s.libraryFilterWhere(library.Filter, principal.User.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_FILTER_INVALID", "媒体库过滤器无效")
		return
	}
	query := "SELECT COUNT(DISTINCT vi.id) FROM media_items vi"
	if isEmptyLibraryFilter(library.Filter) {
		query += " JOIN library_items li ON li.item_id = vi.id AND li.library_id = ?"
		args = append([]any{library.ID}, args...)
	}
	query += " WHERE vi.deleted_at IS NULL"
	if where != "" {
		query += " AND " + where
	}
	var count int
	if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind(query), args...).Scan(&count); err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_PREVIEW_FAILED", "预览媒体库失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraryId": library.ID, "itemCount": count})
}

func (s *Service) libraryFilterWhere(filter any, userID string) (string, []any, error) {
	encoded, err := json.Marshal(filter)
	if err != nil {
		return "", nil, err
	}
	_, _, expr, err := parseLibraryFilter(encoded)
	if err != nil {
		return "", nil, err
	}
	if expr == nil {
		return "", nil, nil
	}
	availableSQL, args := s.AvailableSourceSQL("available_source", "vi")
	return expr.compile(userID, availableSQL, args)
}

func (s *Service) queryLibraryItems(ctx context.Context, library Library, user auth.User, values map[string][]string) ([]Item, string, error) {
	limit := 24
	if raw := strings.TrimSpace(firstValue(values, "limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	offset := 0
	if raw := strings.TrimSpace(firstValue(values, "offset")); raw != "" {
		offset, _ = strconv.Atoi(raw)
		if offset < 0 {
			offset = 0
		}
	}
	params := url.Values(values)
	cursorValues := make(url.Values)
	for key, entries := range params {
		cursorValues[key] = append([]string(nil), entries...)
	}
	cursorValues.Set("_library", library.ID)
	cursorHash := filterCursorHash(cursorValues)
	if raw := params.Get("cursor"); raw != "" {
		var valid bool
		offset, valid = decodeFilterCursor(raw, cursorHash)
		if !valid {
			return nil, "", errInvalidBrowse
		}
	}
	where, args, err := s.libraryFilterWhere(library.Filter, user.ID)
	if err != nil {
		return nil, "", err
	}
	query := "SELECT vi.id, vi.source, COALESCE(vi.source_id, ''), vi.code, vi.raw_code, vi.title, COALESCE(vi.translated_title, ''), COALESCE(vi.original_title, ''), COALESCE(vi.overview, ''), COALESCE(vi.release_date, ''), vi.year, vi.runtime_seconds, vi.source_rating, vi.rating_count, vi.source_url, vi.metadata_status, vi.is_adult, vi.created_at, vi.updated_at, vi.item_type, COALESCE(vi.parent_id, ''), vi.index_number, vi.provider_ids_json, vi.extra_attributes_json, vi.metadata_module_id FROM media_items vi"
	if isEmptyLibraryFilter(library.Filter) {
		query += " JOIN library_items li ON li.item_id = vi.id AND li.library_id = ?"
		args = append([]any{library.ID}, args...)
	}
	query += " WHERE vi.deleted_at IS NULL"
	visibility, accessArgs := auth.MediaVisibilitySQL("vi", user.ID, auth.Elevated(user), false)
	query += " AND (" + visibility + ")"
	// Filter placeholders occur after visibility in SQL.
	filterArgs := args
	if isEmptyLibraryFilter(library.Filter) {
		args = append([]any{library.ID}, accessArgs...)
	} else {
		args = append(accessArgs, filterArgs...)
	}
	if where != "" {
		query += " AND " + where
	}
	scope, scopeArgs, err := browseScope(params)
	if err != nil {
		return nil, "", err
	}
	query += scope
	args = append(args, scopeArgs...)
	column := librarySortColumns[library.Sort.Field]
	if field := params.Get("sort"); field != "" {
		column = librarySortColumns[field]
		if column == "" {
			return nil, "", errInvalidBrowse
		}
	}
	if column == "" {
		column = librarySortColumns["updatedAt"]
	}
	direction := strings.ToUpper(library.Sort.Direction)
	if order := params.Get("sortOrder"); order != "" {
		if !strings.EqualFold(order, "asc") && !strings.EqualFold(order, "desc") {
			return nil, "", errInvalidBrowse
		}
		direction = strings.ToUpper(order)
	}
	if direction != "ASC" {
		direction = "DESC"
	}
	query += " ORDER BY " + column + " " + direction + " NULLS LAST, vi.id DESC LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]Item, 0, limit)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = encodeFilterCursor(offset+limit, cursorHash)
	}
	return items, next, nil
}

func isEmptyLibraryFilter(filter any) bool {
	if filter == nil {
		return true
	}
	encoded, err := json.Marshal(filter)
	return err != nil || bytes.Equal(bytes.TrimSpace(encoded), []byte("{}"))
}

func firstValue(values map[string][]string, key string) string {
	if raw := values[key]; len(raw) > 0 {
		return raw[0]
	}
	return ""
}

func (s *Service) getLibraryPermissions(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	libraryID := chi.URLParam(r, "libraryID")
	if _, ok, err := s.libraryVisible(r.Context(), libraryID, principal.User, false); err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_READ_FAILED", "读取媒体库失败")
		return
	} else if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "媒体库不存在")
		return
	}
	permissions, err := s.readLibraryPermissions(r.Context(), libraryID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_PERMISSION_READ_FAILED", "读取媒体库授权失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraryId": libraryID, "permissions": permissions})
}

func (s *Service) setLibraryPermissions(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	libraryID := chi.URLParam(r, "libraryID")
	var input struct {
		Users []struct {
			ID      string `json:"id"`
			UserID  string `json:"userId"`
			CanView bool   `json:"canView"`
			CanPlay bool   `json:"canPlay"`
		} `json:"users"`
		Roles []struct {
			ID      string `json:"id"`
			RoleID  string `json:"roleId"`
			CanView bool   `json:"canView"`
			CanPlay bool   `json:"canPlay"`
		} `json:"roles"`
		Permissions []LibraryPermission `json:"permissions"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY_PERMISSION", "媒体库授权格式不正确")
		return
	}
	if _, ok, err := s.libraryVisible(r.Context(), libraryID, principal.User, false); err != nil {
		writeError(w, r, http.StatusInternalServerError, "LIBRARY_READ_FAILED", "读取媒体库失败")
		return
	} else if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "媒体库不存在")
		return
	}
	users := make([]LibraryPermission, 0, len(input.Users)+len(input.Permissions))
	roles := make([]LibraryPermission, 0, len(input.Roles)+len(input.Permissions))
	for _, permission := range input.Permissions {
		switch strings.ToLower(strings.TrimSpace(permission.SubjectType)) {
		case "", "user":
			users = append(users, permission)
		case "role":
			roles = append(roles, permission)
		default:
			writeError(w, r, http.StatusBadRequest, "INVALID_LIBRARY_PERMISSION", "subjectType 只能是 user 或 role")
			return
		}
	}
	for _, item := range input.Users {
		id := strings.TrimSpace(item.UserID)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		users = append(users, LibraryPermission{SubjectType: "user", SubjectID: id, CanView: item.CanView, CanPlay: item.CanPlay})
	}
	for _, item := range input.Roles {
		id := strings.TrimSpace(item.RoleID)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		roles = append(roles, LibraryPermission{SubjectType: "role", SubjectID: id, CanView: item.CanView, CanPlay: item.CanPlay})
	}
	if err := s.writeLibraryPermissions(r.Context(), libraryID, users, roles); err != nil {
		writeError(w, r, http.StatusBadRequest, "LIBRARY_PERMISSION_FAILED", err.Error())
		return
	}
	s.audit(r.Context(), principal.User.ID, "set_library_permissions", "library", libraryID, middleware.GetReqID(r.Context()), "{}")
	permissions, _ := s.readLibraryPermissions(r.Context(), libraryID)
	writeJSON(w, http.StatusOK, map[string]any{"libraryId": libraryID, "permissions": permissions})
}

func (s *Service) readLibraryPermissions(ctx context.Context, libraryID string) ([]LibraryPermission, error) {
	query := `SELECT 'user', ulp.user_id, COALESCE(u.username, ''), ulp.can_view, ulp.can_play
FROM user_library_permissions ulp LEFT JOIN users u ON u.id = ulp.user_id WHERE ulp.library_id = ?
UNION ALL
SELECT 'role', rlp.role_id, COALESCE(r.name, ''), rlp.can_view, rlp.can_play
FROM role_library_permissions rlp LEFT JOIN roles r ON r.id = rlp.role_id WHERE rlp.library_id = ?
ORDER BY 1, 3`
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), libraryID, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	permissions := make([]LibraryPermission, 0)
	for rows.Next() {
		var permission LibraryPermission
		var canView, canPlay int
		if err := rows.Scan(&permission.SubjectType, &permission.SubjectID, &permission.Name, &canView, &canPlay); err != nil {
			return nil, err
		}
		permission.CanView, permission.CanPlay = canView == 1, canPlay == 1
		permissions = append(permissions, permission)
	}
	return permissions, rows.Err()
}

func (s *Service) writeLibraryPermissions(ctx context.Context, libraryID string, users, roles []LibraryPermission) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM user_library_permissions WHERE library_id = ?"), libraryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM role_library_permissions WHERE library_id = ?"), libraryID); err != nil {
		return err
	}
	for _, permission := range users {
		if strings.TrimSpace(permission.SubjectID) == "" {
			return errors.New("用户授权缺少 userId")
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_library_permissions (user_id, library_id, can_view, can_play, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)"), permission.SubjectID, libraryID, boolInt(permission.CanView), boolInt(permission.CanPlay), now, now); err != nil {
			return fmt.Errorf("保存用户授权失败: %w", err)
		}
	}
	for _, permission := range roles {
		if strings.TrimSpace(permission.SubjectID) == "" {
			return errors.New("角色授权缺少 roleId")
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO role_library_permissions (role_id, library_id, can_view, can_play, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)"), permission.SubjectID, libraryID, boolInt(permission.CanView), boolInt(permission.CanPlay), now, now); err != nil {
			return fmt.Errorf("保存角色授权失败: %w", err)
		}
	}
	return tx.Commit()
}
