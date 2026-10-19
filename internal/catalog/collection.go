package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
)

type Collection struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Source        string `json:"source"`
	SourceID      string `json:"sourceId,omitempty"`
	PosterImageID string `json:"posterImageId,omitempty"`
	OwnerUserID   string `json:"ownerUserId,omitempty"`
	IsPublic      bool   `json:"isPublic"`
	ItemCount     int    `json:"itemCount"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type CollectionItem struct {
	ItemID        string `json:"itemId"`
	Code          string `json:"code"`
	Title         string `json:"title"`
	Position      int    `json:"position"`
	AddedByUserID string `json:"addedByUserId,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

func (s *Service) registerCollectionRoutes(router chi.Router) {
	router.With(s.authService.RequireAuth).Get("/collections", s.listCollections)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/collections", s.createCollection)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/collections/import", s.importCollection)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/collections/cleanup", s.cleanupCollections)
	router.With(s.authService.RequireAuth).Get("/collections/{collectionID}", s.getCollection)
	router.With(s.authService.RequireAuth).Get("/collections/{collectionID}/items", s.listCollectionItems)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Patch("/collections/{collectionID}", s.updateCollection)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Delete("/collections/{collectionID}", s.deleteCollection)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/collections/{collectionID}/items", s.addCollectionItems)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Patch("/collections/{collectionID}/items/{itemID}", s.updateCollectionItem)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Delete("/collections/{collectionID}/items/{itemID}", s.deleteCollectionItem)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"), s.authService.RequireCSRF).Post("/collections/{collectionID}/clear", s.clearCollection)
}

func (s *Service) listCollections(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	query, args := collectionSelect(principal.User)
	if !isElevated(principal.User) {
		query += " WHERE c.is_public = 1 OR c.owner_user_id = ?"
		args = append(args, principal.User.ID)
	}
	query += " ORDER BY c.updated_at DESC, c.id DESC LIMIT 200"
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTIONS_READ_FAILED", "读取合集失败")
		return
	}
	defer rows.Close()
	collections := make([]Collection, 0)
	for rows.Next() {
		collection, err := scanCollection(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "COLLECTIONS_READ_FAILED", "读取合集失败")
			return
		}
		collections = append(collections, collection)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTIONS_READ_FAILED", "读取合集失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": collections})
}

func (s *Service) getCollection(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collection, visible, err := s.loadCollection(r.Context(), chi.URLParam(r, "collectionID"), principal.User)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_READ_FAILED", "读取合集失败")
		return
	}
	if !visible {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在或不可见")
		return
	}
	writeJSON(w, http.StatusOK, collection)
}

func (s *Service) createCollection(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input collectionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_COLLECTION", "合集格式不正确")
		return
	}
	collection, err := s.createCollectionRecord(r.Context(), principal.User, input, nil)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_CREATE_FAILED", err.Error())
		return
	}
	s.audit(r.Context(), principal.User.ID, "create_collection", "collection", collection.ID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusCreated, collection)
}

type collectionInput struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Source        string   `json:"source"`
	SourceID      string   `json:"sourceId"`
	PosterImageID string   `json:"posterImageId"`
	OwnerUserID   string   `json:"ownerUserId"`
	IsPublic      bool     `json:"isPublic"`
	ItemIDs       []string `json:"itemIds"`
	Codes         []string `json:"codes"`
	Items         []struct {
		ItemID   string `json:"itemId"`
		Position *int   `json:"position"`
	} `json:"items"`
}

func (s *Service) createCollectionRecord(ctx context.Context, actor auth.User, input collectionInput, forcedSource *string) (Collection, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 200 {
		return Collection{}, errors.New("合集名称必须为 1 到 200 个字符")
	}
	description := strings.TrimSpace(input.Description)
	if len([]rune(description)) > 2000 {
		return Collection{}, errors.New("合集说明不能超过 2000 个字符")
	}
	source := strings.ToLower(strings.TrimSpace(input.Source))
	if forcedSource != nil {
		source = *forcedSource
	}
	if source == "" {
		source = "manual"
	}
	if source != "manual" && source != "javdb" {
		return Collection{}, errors.New("source 只能是 manual 或 javdb")
	}
	ownerID := strings.TrimSpace(input.OwnerUserID)
	if !isAdmin(actor) || ownerID == "" {
		ownerID = actor.ID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := newID("collection")
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO collections (id, name, description, source, source_id, poster_image_id, owner_user_id, is_public, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, name, nullable(description), source, nullable(strings.TrimSpace(input.SourceID)), nullable(strings.TrimSpace(input.PosterImageID)), nullable(ownerID), boolInt(input.IsPublic), now, now); err != nil {
		return Collection{}, err
	}
	itemIDs, err := s.resolveCollectionItems(ctx, tx, input.ItemIDs, input.Codes, input.Items)
	if err != nil {
		return Collection{}, err
	}
	for index, item := range itemIDs {
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO collection_items (collection_id, item_id, position, created_at) VALUES (?, ?, ?, ?)"), id, item.id, item.position(index), now); err != nil {
			return Collection{}, fmt.Errorf("保存合集作品失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Collection{}, err
	}
	collection, _, err := s.loadCollection(ctx, id, auth.User{ID: actor.ID, IsAdmin: true, Roles: []string{"Admin"}})
	return collection, err
}

type collectionItemInput struct {
	id       string
	position func(int) int
}

func (s *Service) resolveCollectionItems(ctx context.Context, tx *sql.Tx, itemIDs, codes []string, items []struct {
	ItemID   string `json:"itemId"`
	Position *int   `json:"position"`
}) ([]collectionItemInput, error) {
	seen := map[string]struct{}{}
	result := make([]collectionItemInput, 0, len(itemIDs)+len(codes)+len(items))
	add := func(id string, position *int) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		value := -1
		if position != nil {
			value = *position
		}
		result = append(result, collectionItemInput{id: id, position: func(index int) int {
			if value >= 0 {
				return value
			}
			return index
		}})
	}
	for _, id := range itemIDs {
		add(id, nil)
	}
	for _, code := range codes {
		var id string
		if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM media_items WHERE UPPER(code) = UPPER(?) AND deleted_at IS NULL"), strings.TrimSpace(code)).Scan(&id); errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("作品 %q 不存在", strings.TrimSpace(code))
		} else if err != nil {
			return nil, err
		}
		add(id, nil)
	}
	for _, item := range items {
		if item.Position != nil && *item.Position < 0 {
			return nil, errors.New("position 必须是非负整数")
		}
		add(item.ItemID, item.Position)
	}
	for _, item := range result {
		var exists int
		if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM media_items WHERE id = ? AND deleted_at IS NULL"), item.id).Scan(&exists); err != nil {
			return nil, err
		}
		if exists != 1 {
			return nil, fmt.Errorf("作品 %q 不存在", item.id)
		}
	}
	return result, nil
}

func (s *Service) importCollection(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input collectionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_COLLECTION_IMPORT", "清单导入格式不正确")
		return
	}
	if strings.TrimSpace(input.Source) == "" {
		input.Source = "javdb"
	}
	collection, err := s.createCollectionRecord(r.Context(), principal.User, input, stringPtr("javdb"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_IMPORT_FAILED", err.Error())
		return
	}
	s.audit(r.Context(), principal.User.ID, "import_collection", "collection", collection.ID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusCreated, collection)
}

func stringPtr(value string) *string { return &value }

func (s *Service) updateCollection(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collectionID := chi.URLParam(r, "collectionID")
	var input struct {
		Name          *string `json:"name"`
		Description   *string `json:"description"`
		PosterImageID *string `json:"posterImageId"`
		IsPublic      *bool   `json:"isPublic"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_COLLECTION", "合集更新格式不正确")
		return
	}
	updates := make([]string, 0, 4)
	args := make([]any, 0, 6)
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 200 {
			writeError(w, r, http.StatusBadRequest, "INVALID_COLLECTION", "合集名称必须为 1 到 200 个字符")
			return
		}
		updates = append(updates, "name = ?")
		args = append(args, name)
	}
	if input.Description != nil {
		if len([]rune(*input.Description)) > 2000 {
			writeError(w, r, http.StatusBadRequest, "INVALID_COLLECTION", "合集说明不能超过 2000 个字符")
			return
		}
		updates = append(updates, "description = ?")
		args = append(args, nullable(strings.TrimSpace(*input.Description)))
	}
	if input.PosterImageID != nil {
		updates = append(updates, "poster_image_id = ?")
		args = append(args, nullable(*input.PosterImageID))
	}
	if input.IsPublic != nil {
		updates = append(updates, "is_public = ?")
		args = append(args, boolInt(*input.IsPublic))
	}
	if len(updates) == 0 {
		writeError(w, r, http.StatusBadRequest, "NO_CHANGES", "没有要更新的字段")
		return
	}
	updates = append(updates, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano), collectionID)
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE collections SET "+strings.Join(updates, ", ")+" WHERE id = ? AND (owner_user_id = ? OR ? = 1 OR ? = 1)"), append(args, principal.User.ID, boolInt(isAdmin(principal.User)), boolInt(containsRole(principal.User, "Editor")))...)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_UPDATE_FAILED", "更新合集失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在或无权修改")
		return
	}
	s.audit(r.Context(), principal.User.ID, "update_collection", "collection", collectionID, middleware.GetReqID(r.Context()), "{}")
	collection, _, _ := s.loadCollection(r.Context(), collectionID, auth.User{ID: principal.User.ID, IsAdmin: true, Roles: []string{"Admin"}})
	writeJSON(w, http.StatusOK, collection)
}

func (s *Service) loadCollection(ctx context.Context, collectionID string, user auth.User) (Collection, bool, error) {
	query, args := collectionSelect(user)
	query += " WHERE c.id = ?"
	args = append(args, collectionID)
	if !isElevated(user) {
		query += " AND (c.is_public = 1 OR c.owner_user_id = ?)"
		args = append(args, user.ID)
	}
	var row Collection
	var public int
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&row.ID, &row.Name, &row.Description, &row.Source, &row.SourceID, &row.PosterImageID, &row.OwnerUserID, &public, &row.ItemCount, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Collection{}, false, nil
	}
	row.IsPublic = public == 1
	return row, err == nil, err
}

// A public collection shares its description and order, never media access.
// Count and cover use the same permission rule as the actual item query.
func collectionSelect(user auth.User) (string, []any) {
	cover, coverArgs := auth.MediaVisibilitySQL("cover_item", user.ID, auth.Elevated(user), false)
	items, itemArgs := auth.MediaVisibilitySQL("vi", user.ID, auth.Elevated(user), false)
	query := `SELECT c.id,c.name,COALESCE(c.description,''),c.source,COALESCE(c.source_id,''),
COALESCE((SELECT image.id FROM item_images image JOIN media_items cover_item ON cover_item.id=image.item_id
 WHERE image.id=c.poster_image_id AND (` + cover + `)),''),COALESCE(c.owner_user_id,''),c.is_public,
(SELECT COUNT(*) FROM collection_items ci JOIN media_items vi ON vi.id=ci.item_id
 WHERE ci.collection_id=c.id AND (` + items + `)),c.created_at,c.updated_at FROM collections c`
	return query, append(coverArgs, itemArgs...)
}

func scanCollection(scanner interface{ Scan(...any) error }) (Collection, error) {
	var collection Collection
	var public int
	if err := scanner.Scan(&collection.ID, &collection.Name, &collection.Description, &collection.Source, &collection.SourceID, &collection.PosterImageID, &collection.OwnerUserID, &public, &collection.ItemCount, &collection.CreatedAt, &collection.UpdatedAt); err != nil {
		return Collection{}, err
	}
	collection.IsPublic = public == 1
	return collection, nil
}

func (s *Service) listCollectionItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collection, visible, err := s.loadCollection(r.Context(), chi.URLParam(r, "collectionID"), principal.User)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_READ_FAILED", "读取合集失败")
		return
	}
	if !visible {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在或不可见")
		return
	}
	query := `SELECT ci.item_id, vi.code, vi.title, ci.position, COALESCE(ci.created_at, ''), ''
FROM collection_items ci JOIN media_items vi ON vi.id = ci.item_id AND vi.deleted_at IS NULL
WHERE ci.collection_id = ?`
	visibility, accessArgs := auth.MediaVisibilitySQL("vi", principal.User.ID, auth.Elevated(principal.User), false)
	query += " AND (" + visibility + ") ORDER BY ci.position, ci.item_id"
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), append([]any{collection.ID}, accessArgs...)...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEMS_READ_FAILED", "读取合集作品失败")
		return
	}
	defer rows.Close()
	items := make([]CollectionItem, 0)
	for rows.Next() {
		var item CollectionItem
		if err := rows.Scan(&item.ItemID, &item.Code, &item.Title, &item.Position, &item.CreatedAt, &item.AddedByUserID); err != nil {
			writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEMS_READ_FAILED", "读取合集作品失败")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEMS_READ_FAILED", "读取合集作品失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collection": collection, "items": items})
}

func (s *Service) addCollectionItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collectionID := chi.URLParam(r, "collectionID")
	if !s.collectionEditable(r.Context(), collectionID, principal.User) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在或无权修改")
		return
	}
	var input struct {
		ItemIDs []string `json:"itemIds"`
		Items   []struct {
			ItemID   string `json:"itemId"`
			Position *int   `json:"position"`
		} `json:"items"`
		Codes []string `json:"codes"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_COLLECTION_ITEMS", "合集作品格式不正确")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEMS_WRITE_FAILED", "保存合集作品失败")
		return
	}
	defer tx.Rollback()
	resolved, err := s.resolveCollectionItems(r.Context(), tx, input.ItemIDs, input.Codes, input.Items)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_ITEMS_WRITE_FAILED", err.Error())
		return
	}
	for index, item := range resolved {
		position := item.position(index)
		if _, err := tx.ExecContext(r.Context(), s.db.Rebind("INSERT INTO collection_items (collection_id, item_id, position, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(collection_id, item_id) DO UPDATE SET position = excluded.position"), collectionID, item.id, position, now); err != nil {
			writeError(w, r, http.StatusBadRequest, "COLLECTION_ITEMS_WRITE_FAILED", "保存合集作品失败")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE collections SET updated_at = ? WHERE id = ?"), now, collectionID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEMS_WRITE_FAILED", "更新合集时间失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEMS_WRITE_FAILED", "保存合集作品失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "add_collection_items", "collection", collectionID, middleware.GetReqID(r.Context()), fmt.Sprintf(`{"count":%d}`, len(resolved)))
	writeJSON(w, http.StatusOK, map[string]any{"collectionId": collectionID, "added": len(resolved)})
}

func (s *Service) collectionEditable(ctx context.Context, collectionID string, user auth.User) bool {
	if isAdmin(user) || containsRole(user, "Editor") {
		var count int
		return s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM collections WHERE id = ?"), collectionID).Scan(&count) == nil && count == 1
	}
	var count int
	return s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM collections WHERE id = ? AND owner_user_id = ?"), collectionID, user.ID).Scan(&count) == nil && count == 1
}

func containsRole(user auth.User, role string) bool {
	for _, value := range user.Roles {
		if value == role {
			return true
		}
	}
	return false
}

func (s *Service) updateCollectionItem(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collectionID, itemID := chi.URLParam(r, "collectionID"), unescapeParam(r, "itemID")
	if !s.collectionEditable(r.Context(), collectionID, principal.User) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在或无权修改")
		return
	}
	var input struct {
		Position *int `json:"position"`
	}
	if err := decodeJSON(r, &input); err != nil || input.Position == nil || *input.Position < 0 {
		writeError(w, r, http.StatusBadRequest, "INVALID_POSITION", "position 必须是非负整数")
		return
	}
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_UPDATE_FAILED", "更新合集顺序失败")
		return
	}
	defer tx.Rollback()
	var oldPosition, itemCount int
	if err := tx.QueryRowContext(r.Context(), s.db.Rebind("SELECT position FROM collection_items WHERE collection_id = ? AND item_id = ?"), collectionID, itemID).Scan(&oldPosition); errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集作品不存在")
		return
	} else if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_UPDATE_FAILED", "读取合集顺序失败")
		return
	}
	if err := tx.QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM collection_items WHERE collection_id = ?"), collectionID).Scan(&itemCount); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_UPDATE_FAILED", "读取合集顺序失败")
		return
	}
	targetPosition := *input.Position
	if targetPosition >= itemCount {
		targetPosition = itemCount - 1
	}
	if targetPosition > oldPosition {
		if _, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE collection_items SET position = position - 1 WHERE collection_id = ? AND item_id <> ? AND position > ? AND position <= ?"), collectionID, itemID, oldPosition, targetPosition); err != nil {
			writeError(w, r, http.StatusBadRequest, "COLLECTION_ITEM_UPDATE_FAILED", "更新合集顺序失败")
			return
		}
	} else if targetPosition < oldPosition {
		if _, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE collection_items SET position = position + 1 WHERE collection_id = ? AND item_id <> ? AND position >= ? AND position < ?"), collectionID, itemID, targetPosition, oldPosition); err != nil {
			writeError(w, r, http.StatusBadRequest, "COLLECTION_ITEM_UPDATE_FAILED", "更新合集顺序失败")
			return
		}
	}
	result, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE collection_items SET position = ? WHERE collection_id = ? AND item_id = ?"), targetPosition, collectionID, itemID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_ITEM_UPDATE_FAILED", "更新合集顺序失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集作品不存在")
		return
	}
	if _, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE collections SET updated_at = ? WHERE id = ?"), time.Now().UTC().Format(time.RFC3339Nano), collectionID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_UPDATE_FAILED", "更新合集时间失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_UPDATE_FAILED", "更新合集顺序失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "update_collection_item", "collection", collectionID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusOK, map[string]any{"collectionId": collectionID, "itemId": itemID, "position": targetPosition})
}

func (s *Service) deleteCollectionItem(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collectionID, itemID := chi.URLParam(r, "collectionID"), unescapeParam(r, "itemID")
	if !s.collectionEditable(r.Context(), collectionID, principal.User) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在或无权修改")
		return
	}
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_DELETE_FAILED", "移除合集作品失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), s.db.Rebind("DELETE FROM collection_items WHERE collection_id = ? AND item_id = ?"), collectionID, itemID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_ITEM_DELETE_FAILED", "移除合集作品失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集作品不存在")
		return
	}
	if _, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE collections SET updated_at = ? WHERE id = ?"), time.Now().UTC().Format(time.RFC3339Nano), collectionID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_DELETE_FAILED", "更新合集时间失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_ITEM_DELETE_FAILED", "移除合集作品失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "delete_collection_item", "collection", collectionID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) deleteCollection(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collectionID := chi.URLParam(r, "collectionID")
	if !s.collectionEditable(r.Context(), collectionID, principal.User) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在或无权修改")
		return
	}
	collection, visible, err := s.loadCollection(r.Context(), collectionID, auth.User{ID: principal.User.ID, IsAdmin: true, Roles: []string{"Admin"}})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_READ_FAILED", "读取合集失败")
		return
	}
	if !visible {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在")
		return
	}
	if !validCollectionConfirmation(r, "DELETE", collection.Name) {
		writeError(w, r, http.StatusConflict, "CONFIRMATION_REQUIRED", "删除合集需要 confirmation=DELETE 或合集名称")
		return
	}
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM collections WHERE id = ?"), collectionID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_DELETE_FAILED", "删除合集失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在")
		return
	}
	s.audit(r.Context(), principal.User.ID, "delete_collection", "collection", collectionID, middleware.GetReqID(r.Context()), "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func validCollectionConfirmation(r *http.Request, expected ...string) bool {
	value := strings.TrimSpace(r.URL.Query().Get("confirmation"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("confirm"))
	}
	if value == "" && r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err == nil && len(bytes.TrimSpace(body)) > 0 {
			var input struct {
				Confirmation     string `json:"confirmation"`
				Confirm          string `json:"confirm"`
				ConfirmationText string `json:"confirmationText"`
			}
			if json.Unmarshal(body, &input) == nil {
				value = strings.TrimSpace(input.Confirmation)
				if value == "" {
					value = strings.TrimSpace(input.Confirm)
				}
				if value == "" {
					value = strings.TrimSpace(input.ConfirmationText)
				}
			}
		}
	}
	for _, candidate := range expected {
		if value == candidate || strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}

func (s *Service) clearCollection(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	collectionID := chi.URLParam(r, "collectionID")
	collection, ok, err := s.loadCollection(r.Context(), collectionID, auth.User{ID: principal.User.ID, IsAdmin: true, Roles: []string{"Admin"}})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_READ_FAILED", "读取合集失败")
		return
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "合集不存在")
		return
	}
	if !validCollectionConfirmation(r, "CLEAR", "清空", collection.Name) {
		writeError(w, r, http.StatusConflict, "CONFIRMATION_REQUIRED", "清空合集需要确认词 CLEAR、清空或合集名称")
		return
	}
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_CLEAR_FAILED", "清空合集失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), s.db.Rebind("DELETE FROM collection_items WHERE collection_id = ?"), collectionID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_CLEAR_FAILED", "清空合集失败")
		return
	}
	removed, _ := result.RowsAffected()
	if _, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE collections SET updated_at = ? WHERE id = ?"), time.Now().UTC().Format(time.RFC3339Nano), collectionID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_CLEAR_FAILED", "更新合集时间失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_CLEAR_FAILED", "清空合集失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "clear_collection", "collection", collectionID, middleware.GetReqID(r.Context()), fmt.Sprintf(`{"removed":%d}`, removed))
	writeJSON(w, http.StatusOK, map[string]any{"collectionId": collectionID, "removed": removed})
}

func (s *Service) cleanupCollections(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !validCollectionConfirmation(r, "CLEANUP_ORPHANS", "清理孤儿") {
		writeError(w, r, http.StatusConflict, "CONFIRMATION_REQUIRED", "清理孤儿关系需要确认词 CLEANUP_ORPHANS")
		return
	}
	var input struct {
		Confirmation     string `json:"confirmation"`
		Confirm          string `json:"confirm"`
		ConfirmationText string `json:"confirmationText"`
		DeleteEmpty      bool   `json:"deleteEmpty"`
	}
	if r.URL.Query().Get("deleteEmpty") != "" {
		input.DeleteEmpty = r.URL.Query().Get("deleteEmpty") == "1" || strings.EqualFold(r.URL.Query().Get("deleteEmpty"), "true")
	} else if r.Body != nil {
		if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, r, http.StatusBadRequest, "INVALID_COLLECTION_CLEANUP", "清理参数格式不正确")
			return
		}
	}
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_CLEANUP_FAILED", "清理孤儿关系失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), s.db.Rebind("DELETE FROM collection_items WHERE item_id IN (SELECT id FROM media_items WHERE deleted_at IS NOT NULL)"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "COLLECTION_CLEANUP_FAILED", "清理孤儿关系失败")
		return
	}
	removedItems, _ := result.RowsAffected()
	removedCollections := int64(0)
	if input.DeleteEmpty {
		result, err := tx.ExecContext(r.Context(), s.db.Rebind("DELETE FROM collections WHERE NOT EXISTS (SELECT 1 FROM collection_items ci WHERE ci.collection_id = collections.id)"))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "COLLECTION_CLEANUP_FAILED", "清理空合集失败")
			return
		}
		removedCollections, _ = result.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "COLLECTION_CLEANUP_FAILED", "清理合集失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "cleanup_collections", "collection", "", middleware.GetReqID(r.Context()), fmt.Sprintf(`{"removedItems":%d,"removedCollections":%d}`, removedItems, removedCollections))
	writeJSON(w, http.StatusOK, map[string]any{"removedItems": removedItems, "removedCollections": removedCollections})
}
