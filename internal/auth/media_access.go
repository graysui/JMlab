package auth

import (
	"context"

	"github.com/graysui/JMlab/internal/repository"
)

// LibraryVisibilitySQL checks the specific library being listed/requested.
// This is also used inside MediaVisibilitySQL so role/adult rules cannot drift.
func LibraryVisibilitySQL(alias, userID string, elevated, play bool) (string, []any) {
	if elevated {
		return "1=1", nil
	}
	permission := "can_view"
	if play {
		permission = "can_play"
	}
	return alias + `.enabled=1 AND (` + alias + `.is_adult=0 OR EXISTS (
	 SELECT 1 FROM user_preferences lib_pref WHERE lib_pref.user_id=? AND lib_pref.adult_confirmed_at IS NOT NULL AND lib_pref.adult_confirmed_at<>''))
	 AND (` + alias + `.is_public=1 OR EXISTS (
	 SELECT 1 FROM user_library_permissions lib_up WHERE lib_up.library_id=` + alias + `.id AND lib_up.user_id=? AND lib_up.` + permission + `=1)
	 OR EXISTS (SELECT 1 FROM user_roles lib_ur JOIN role_library_permissions lib_rp ON lib_rp.role_id=lib_ur.role_id
	 WHERE lib_ur.user_id=? AND lib_rp.library_id=` + alias + `.id AND lib_rp.` + permission + `=1))`, []any{userID, userID, userID}
}

// MediaInLibrarySQL applies the same policy to an existing media/library join.
// It prevents a requested private library borrowing access from another library.
func MediaInLibrarySQL(itemAlias, libraryAlias, userID string, elevated, play bool) (string, []any) {
	clause, args := LibraryVisibilitySQL(libraryAlias, userID, elevated, play)
	base := itemAlias + ".deleted_at IS NULL"
	if elevated {
		return base + " AND (" + clause + ")", args
	}
	return base + " AND (" + itemAlias + `.is_adult=0 OR EXISTS (SELECT 1 FROM user_preferences item_pref WHERE item_pref.user_id=? AND item_pref.adult_confirmed_at IS NOT NULL AND item_pref.adult_confirmed_at<>'')) AND (` + clause + `)`, append([]any{userID}, args...)
}

// MediaVisibilitySQL uses an internal SQL alias, never request input. Public
// playlists share their ordering, not access to otherwise restricted media.
func MediaVisibilitySQL(alias, userID string, elevated, play bool) (string, []any) {
	base := alias + ".deleted_at IS NULL"
	if elevated {
		return base, nil
	}
	library, libraryArgs := LibraryVisibilitySQL("access_lib", userID, elevated, play)
	confirmed := `EXISTS (SELECT 1 FROM user_preferences access_pref WHERE access_pref.user_id = ? AND access_pref.adult_confirmed_at IS NOT NULL AND access_pref.adult_confirmed_at <> '')`
	return base + ` AND (` + alias + `.is_adult = 0 OR ` + confirmed + `) AND EXISTS (
		SELECT 1 FROM library_items access_li JOIN virtual_libraries access_lib ON access_lib.id = access_li.library_id
		WHERE access_li.item_id = ` + alias + `.id AND ` + library + `)`, append([]any{userID}, libraryArgs...)
}

func Elevated(user User) bool {
	if user.IsAdmin {
		return true
	}
	for _, role := range user.Roles {
		if role == "Admin" || role == "Editor" {
			return true
		}
	}
	return false
}

func CanAccessMedia(ctx context.Context, db *repository.Database, user User, itemID string, play bool) bool {
	clause, args := MediaVisibilitySQL("access_item", user.ID, Elevated(user), play)
	args = append([]any{itemID}, args...)
	var count int
	err := db.DB().QueryRowContext(ctx, db.Rebind(`SELECT COUNT(*) FROM media_items access_item WHERE access_item.id = ? AND `+clause), args...).Scan(&count)
	return err == nil && count > 0
}
