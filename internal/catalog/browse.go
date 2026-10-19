package catalog

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

var errInvalidBrowse = errors.New("浏览参数或分页游标无效，请重新选择筛选条件")

// browseScope is shared by the global catalogue and individual libraries.
// Empty parameters preserve the existing API's full catalogue behaviour.
func browseScope(values url.Values) (string, []any, error) {
	query := ""
	args := []any{}
	parent := strings.TrimSpace(values.Get("parentId"))
	if raw := strings.TrimSpace(values.Get("topLevel")); raw != "" {
		top, err := strconv.ParseBool(raw)
		if err != nil || top && parent != "" {
			return "", nil, errInvalidBrowse
		}
		if top {
			query += " AND vi.parent_id IS NULL"
		}
	}
	if parent != "" {
		query += " AND vi.parent_id = ?"
		args = append(args, parent)
	}
	if kind := strings.ToLower(strings.TrimSpace(values.Get("itemType"))); kind != "" {
		switch kind {
		case "movie", "series", "season", "episode", "video":
		default:
			return "", nil, errInvalidBrowse
		}
		query += " AND vi.item_type = ?"
		args = append(args, kind)
	}
	return query, args, nil
}
