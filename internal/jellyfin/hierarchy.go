package jellyfin

import (
	"context"
	"database/sql"

	"github.com/graysui/JMlab/internal/auth"
)

func (s *Service) enrichHierarchy(ctx context.Context, items []BaseItemDto, userID string) error {
	indices := map[string]int{}
	args := []any{}
	for i := range items {
		switch items[i].Type {
		case "Series", "Season", "Episode":
			indices[items[i].Id] = i
			args = append(args, items[i].Id)
			if items[i].IsFolder {
				items[i].Container = ""
				items[i].MediaType = ""
			}
		}
	}
	if len(args) == 0 {
		return nil
	}
	principal, ok := auth.PrincipalFromContext(ctx)
	user := principal.User
	if !ok || user.ID != userID {
		var err error
		user, err = s.authService.GetUser(ctx, userID)
		if err != nil {
			return err
		}
	}
	pVisible, pArgs := auth.MediaVisibilitySQL("p", userID, auth.Elevated(user), false)
	gVisible, gArgs := auth.MediaVisibilitySQL("g", userID, auth.Elevated(user), false)
	query := `SELECT vi.id,vi.item_type,COALESCE(p.id,''),COALESCE(p.item_type,''),COALESCE(p.title,''),p.index_number,COALESCE(g.id,''),COALESCE(g.title,'') FROM media_items vi
LEFT JOIN media_items p ON p.id=vi.parent_id AND (` + pVisible + `)
LEFT JOIN media_items g ON g.id=p.parent_id AND g.item_type='series' AND (` + gVisible + `)
WHERE vi.id IN (` + placeholders(len(args)) + `)`
	queryArgs := append(append(append([]any{}, pArgs...), gArgs...), args...)
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), queryArgs...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, kind, parent, parentKind, parentName, series, seriesName string
		var index sql.NullInt64
		if err := rows.Scan(&id, &kind, &parent, &parentKind, &parentName, &index, &series, &seriesName); err != nil {
			rows.Close()
			return err
		}
		item := &items[indices[id]]
		item.ParentId = parent
		if kind == "season" && parentKind == "series" {
			item.SeriesId = parent
			item.SeriesName = parentName
		}
		if kind == "episode" && parentKind == "season" {
			item.SeasonId = parent
			item.SeasonName = parentName
			item.SeriesId = series
			item.SeriesName = seriesName
			if index.Valid {
				value := int(index.Int64)
				item.ParentIndexNumber = &value
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	cVisible, cArgs := auth.MediaVisibilitySQL("c", userID, auth.Elevated(user), false)
	query = `SELECT root_id,SUM(is_direct),COUNT(*) FROM (
SELECT c.parent_id AS root_id,1 AS is_direct FROM media_items c WHERE c.parent_id IN (` + placeholders(len(args)) + `) AND (` + cVisible + `)
UNION ALL
SELECT p.parent_id AS root_id,0 AS is_direct FROM media_items c JOIN media_items p ON p.id=c.parent_id WHERE p.parent_id IN (` + placeholders(len(args)) + `) AND (` + cVisible + `) AND (` + pVisible + `)
) descendants GROUP BY root_id`
	queryArgs = append(append(append(append(append([]any{}, args...), cArgs...), args...), cArgs...), pArgs...)
	rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind(query), queryArgs...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var direct, total int
		if err := rows.Scan(&id, &direct, &total); err != nil {
			return err
		}
		item := &items[indices[id]]
		item.ChildCount = direct
		item.RecursiveItemCount = total
	}
	return rows.Err()
}
