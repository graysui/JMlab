package catalog

import (
	"context"
	"encoding/json"
	"time"
)

// User-state views borrow access from an ordinary library. They cannot grant
// global access based on a different user's favorites or watched state.
func expressionHasField(expr *libraryExpression, names map[string]bool) bool {
	if expr == nil {
		return false
	}
	if names[expr.field] {
		return true
	}
	for _, child := range expr.children {
		if expressionHasField(child, names) {
			return true
		}
	}
	return false
}

// SyncLibraryMemberships materializes non-personal filters for the shared
// authorization layer. Empty filters keep their explicitly assigned members.
// It never creates a library and can run before the first one is configured.
func (s *Service) SyncLibraryMemberships(ctx context.Context, libraryID, itemID string) error {
	query := "SELECT id, filter_json, item_type FROM virtual_libraries"
	var args []any
	if libraryID != "" {
		query += " WHERE id=?"
		args = append(args, libraryID)
	}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return err
	}
	type definition struct {
		id     string
		filter json.RawMessage
		kind   string
	}
	var definitions []definition
	for rows.Next() {
		var row definition
		var raw string
		if err := rows.Scan(&row.id, &raw, &row.kind); err != nil {
			rows.Close()
			return err
		}
		row.filter = json.RawMessage(raw)
		definitions = append(definitions, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, row := range definitions {
		filter, _, expr, err := parseLibraryFilter(row.filter)
		if err != nil {
			return err
		}
		if expr == nil {
			continue
		}
		if expressionHasField(expr, map[string]bool{"isFavorite": true, "isWatched": true}) {
			if _, err := s.db.DB().ExecContext(ctx, s.db.Rebind("DELETE FROM library_items WHERE library_id=?"), row.id); err != nil {
				return err
			}
			continue
		}
		clause, filterArgs, err := s.libraryFilterWhere(filter, "")
		if err != nil {
			return err
		}
		where := "vi.deleted_at IS NULL AND (" + clause + ")"
		if row.kind == "movie" {
			where += " AND vi.item_type='movie'"
		}
		if itemID != "" {
			where += " AND vi.id=?"
			filterArgs = append(filterArgs, itemID)
		}
		tx, err := s.db.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		remove := "DELETE FROM library_items WHERE library_id=?"
		removeArgs := []any{row.id}
		if itemID != "" {
			remove += " AND item_id=?"
			removeArgs = append(removeArgs, itemID)
		}
		if _, err = tx.ExecContext(ctx, s.db.Rebind(remove), removeArgs...); err == nil {
			insertArgs := append([]any{row.id, time.Now().UTC().Format(time.RFC3339Nano)}, filterArgs...)
			_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO library_items(library_id,item_id,position,created_at) SELECT ?,vi.id,0,? FROM media_items vi WHERE "+where+" ON CONFLICT(library_id,item_id) DO NOTHING"), insertArgs...)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
