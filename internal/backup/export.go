package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
)

const exportFormatVersion = "jmlab.export.v2"

func supportedExportVersion(v string) bool {
	v = strings.Replace(v, "javlab.", "jmlab.", 1)
	return v == exportFormatVersion || v == "jmlab.export.v1"
}

// ExportDocument is intentionally provider independent. Provider accounts,
// playback source URLs, cookies, tokens and encrypted configuration never
// appear in this document.
type ExportDocument struct {
	FormatVersion      string                   `json:"formatVersion"`
	ExportedAt         string                   `json:"exportedAt"`
	ApplicationVersion string                   `json:"applicationVersion"`
	IncludeUserState   bool                     `json:"includeUserState"`
	Items              []ExportItem             `json:"items"`
	People             []ExportPerson           `json:"people"`
	Studios            []ExportNamedEntity      `json:"studios"`
	Series             []ExportNamedEntity      `json:"series"`
	Tags               []ExportTag              `json:"tags"`
	ItemPeople         []ExportItemPerson       `json:"itemPeople"`
	ItemStudios        []ExportItemStudio       `json:"itemStudios"`
	ItemSeries         []ExportItemSeries       `json:"itemSeries"`
	ItemTags           []ExportItemTag          `json:"itemTags"`
	EntityData         []ExportEntityData       `json:"entityData,omitempty"`
	EntityIdentifiers  []ExportEntityIdentifier `json:"entityIdentifiers,omitempty"`
	EntityLinks        []ExportEntityLink       `json:"entityLinks,omitempty"`
	UserPersonStates   []ExportUserPersonState  `json:"userPersonStates,omitempty"`
	UserEntityNotes    []ExportUserEntityNote   `json:"userEntityNotes,omitempty"`
	Libraries          []ExportLibrary          `json:"libraries"`
	LibraryItems       []ExportLibraryItem      `json:"libraryItems"`
	Collections        []ExportCollection       `json:"collections"`
	CollectionItems    []ExportCollectionItem   `json:"collectionItems"`
	Users              []ExportUser             `json:"users,omitempty"`
	UserItemStates     []ExportUserItemState    `json:"userItemStates,omitempty"`
	Playlists          []ExportPlaylist         `json:"playlists,omitempty"`
	PlaylistItems      []ExportPlaylistItem     `json:"playlistItems,omitempty"`
}

type ExportItem struct {
	ItemType         string          `json:"itemType,omitempty"`
	ParentID         string          `json:"parentId,omitempty"`
	IndexNumber      *int            `json:"indexNumber,omitempty"`
	ProviderIDs      json.RawMessage `json:"providerIds,omitempty"`
	ExtraAttributes  json.RawMessage `json:"extraAttributes,omitempty"`
	MetadataModuleID string          `json:"metadataModuleId,omitempty"`
	ID               string          `json:"id"`
	Source           string          `json:"source"`
	SourceID         string          `json:"sourceId,omitempty"`
	Code             string          `json:"code"`
	RawCode          string          `json:"rawCode"`
	Title            string          `json:"title"`
	TranslatedTitle  string          `json:"translatedTitle,omitempty"`
	OriginalTitle    string          `json:"originalTitle,omitempty"`
	Overview         string          `json:"overview,omitempty"`
	ReleaseDate      string          `json:"releaseDate,omitempty"`
	Year             *int            `json:"year,omitempty"`
	RuntimeSeconds   *int            `json:"runtimeSeconds,omitempty"`
	SourceRating     *float64        `json:"sourceRating,omitempty"`
	RatingCount      int             `json:"ratingCount"`
	SourceURL        string          `json:"sourceUrl"`
	MetadataStatus   string          `json:"metadataStatus"`
	IsAdult          bool            `json:"isAdult"`
	CreatedAt        string          `json:"createdAt"`
	UpdatedAt        string          `json:"updatedAt"`
}

type ExportPerson struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Aliases    json.RawMessage `json:"aliases"`
	Gender     string          `json:"gender,omitempty"`
	Status     string          `json:"status"`
	Birthday   string          `json:"birthday,omitempty"`
	HeightCM   *int            `json:"heightCm,omitempty"`
	CupSize    string          `json:"cupSize,omitempty"`
	Biography  string          `json:"biography,omitempty"`
	SourceURL  string          `json:"sourceUrl,omitempty"`
	IsExcluded bool            `json:"isExcluded"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

type ExportNamedEntity struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	SourceName string          `json:"sourceName,omitempty"`
	Aliases    json.RawMessage `json:"aliases"`
	IsExcluded bool            `json:"isExcluded"`
	SortOrder  int             `json:"sortOrder"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

type ExportTag struct {
	ExportNamedEntity
	Kind     string `json:"kind"`
	ParentID string `json:"parentId,omitempty"`
}

type ExportItemPerson struct {
	ItemID    string `json:"itemId"`
	PersonID  string `json:"personId"`
	Role      string `json:"role"`
	SortOrder int    `json:"sortOrder"`
}
type ExportItemStudio struct {
	ItemID    string `json:"itemId"`
	StudioID  string `json:"studioId"`
	SortOrder int    `json:"sortOrder"`
}
type ExportItemSeries struct {
	ItemID    string `json:"itemId"`
	SeriesID  string `json:"seriesId"`
	SortOrder int    `json:"sortOrder"`
}
type ExportItemTag struct {
	ItemID string `json:"itemId"`
	TagID  string `json:"tagId"`
}

type ExportLibrary struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	ItemType      string          `json:"itemType"`
	Filter        json.RawMessage `json:"filter"`
	Sort          json.RawMessage `json:"sort"`
	PosterImageID string          `json:"posterImageId,omitempty"`
	IsPublic      bool            `json:"isPublic"`
	IsAdult       bool            `json:"isAdult"`
	Enabled       bool            `json:"enabled"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
}
type ExportLibraryItem struct {
	LibraryID string `json:"libraryId"`
	ItemID    string `json:"itemId"`
	Position  int    `json:"position"`
	CreatedAt string `json:"createdAt"`
}
type ExportCollection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	SourceID    string `json:"sourceId,omitempty"`
	OwnerUserID string `json:"ownerUserId,omitempty"`
	IsPublic    bool   `json:"isPublic"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}
type ExportCollectionItem struct {
	CollectionID string `json:"collectionId"`
	ItemID       string `json:"itemId"`
	Position     int    `json:"position"`
	CreatedAt    string `json:"createdAt"`
}
type ExportUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
}
type ExportUserItemState struct {
	UserID                string `json:"userId"`
	ItemID                string `json:"itemId"`
	IsFavorite            bool   `json:"isFavorite"`
	IsWatched             bool   `json:"isWatched"`
	IsWantToWatch         bool   `json:"isWantToWatch"`
	PlaybackPositionTicks int64  `json:"playbackPositionTicks"`
	PlaybackRuntimeTicks  int64  `json:"playbackRuntimeTicks"`
	LastPlayedAt          string `json:"lastPlayedAt,omitempty"`
	UserRating            *int   `json:"userRating,omitempty"`
	Note                  string `json:"note,omitempty"`
	Version               int    `json:"version"`
	CreatedAt             string `json:"createdAt"`
	UpdatedAt             string `json:"updatedAt"`
}

type ExportPlaylist struct {
	ID          string `json:"id"`
	OwnerUserID string `json:"ownerUserId"`
	Name        string `json:"name"`
	Overview    string `json:"overview,omitempty"`
	IsPublic    bool   `json:"isPublic"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type ExportPlaylistItem struct {
	PlaylistID    string `json:"playlistId"`
	ItemID        string `json:"itemId"`
	Position      int    `json:"position"`
	AddedByUserID string `json:"addedByUserId"`
	CreatedAt     string `json:"createdAt"`
}

type importOptions struct {
	Mode             string
	IncludeUserState bool
}
type ImportConflictField struct {
	Field      string `json:"field"`
	Existing   any    `json:"existing,omitempty"`
	Incoming   any    `json:"incoming,omitempty"`
	Resolution string `json:"resolution"`
}
type ImportConflict struct {
	Entity     string                `json:"entity"`
	Key        string                `json:"key"`
	ID         string                `json:"id,omitempty"`
	Fields     []ImportConflictField `json:"fields"`
	Resolution string                `json:"resolution"`
}
type ImportSummary struct {
	Items         int `json:"items"`
	People        int `json:"people"`
	Studios       int `json:"studios"`
	Series        int `json:"series"`
	Tags          int `json:"tags"`
	Libraries     int `json:"libraries"`
	Collections   int `json:"collections"`
	Playlists     int `json:"playlists"`
	PlaylistItems int `json:"playlistItems"`
	UserStates    int `json:"userStates"`
	Created       int `json:"created"`
	Updated       int `json:"updated"`
	Skipped       int `json:"skipped"`
}
type ImportPreview struct {
	Valid            bool             `json:"valid"`
	FormatVersion    string           `json:"formatVersion"`
	Mode             string           `json:"mode"`
	IncludeUserState bool             `json:"includeUserState"`
	Summary          ImportSummary    `json:"summary"`
	Conflicts        []ImportConflict `json:"conflicts"`
	Errors           []string         `json:"errors"`
}

func (s *Service) registerExportRoutes(router chi.Router) {
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin")).Get("/backup/export", s.exportJSON)
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"), s.auth.RequireCSRF).Post("/backup/export", s.exportJSON)
	router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"), s.auth.RequireCSRF).Post("/import/export", s.exportJSON)
	for _, path := range []string{"/import/preview", "/backup/import/preview"} {
		router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"), s.auth.RequireCSRF).Post(path, s.previewJSONImport)
	}
	for _, path := range []string{"/import/apply", "/backup/import"} {
		router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"), s.auth.RequireCSRF).Post(path, s.applyJSONImport)
	}
}

func (s *Service) exportJSON(w http.ResponseWriter, r *http.Request) {
	include := parseBool(r.URL.Query().Get("includeUserState"))
	if r.Method != http.MethodGet && r.Body != nil {
		var input struct {
			IncludeUserState *bool `json:"includeUserState"`
		}
		if err := decodeBackupJSON(r, &input); err == nil && input.IncludeUserState != nil {
			include = *input.IncludeUserState
		}
	}
	doc, err := s.buildExport(r.Context(), include)
	if err != nil {
		slog.Error("生成 JSON 导出失败", "error", err)
		writeError(w, r, http.StatusInternalServerError, "EXPORT_FAILED", "生成 JSON 导出失败")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=jmlab-export.json")
	writeJSON(w, http.StatusOK, doc)
}

func (s *Service) previewJSONImport(w http.ResponseWriter, r *http.Request) {
	doc, options, err := decodeImportPayload(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_IMPORT", err.Error())
		return
	}
	preview, err := s.previewImport(r.Context(), doc, options)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, previewError(err, doc, options))
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Service) applyJSONImport(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	doc, options, err := decodeImportPayload(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_IMPORT", err.Error())
		return
	}
	preview, err := s.previewImport(r.Context(), doc, options)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, previewError(err, doc, options))
		return
	}
	if len(preview.Errors) > 0 {
		writeJSON(w, http.StatusConflict, preview)
		return
	}
	summary, err := s.applyImport(r.Context(), doc, options)
	if err != nil {
		writeError(w, r, http.StatusConflict, "IMPORT_ROLLED_BACK", "导入失败，现有数据未修改")
		return
	}
	metadata, _ := json.Marshal(map[string]any{"summary": summary, "mode": options.Mode, "includeUserState": options.IncludeUserState})
	auditID := s.writeAudit(r.Context(), principal.User.ID, "import_json", "database", "", middleware.GetReqID(r.Context()), string(metadata))
	writeJSON(w, http.StatusOK, map[string]any{"status": "imported", "summary": summary, "conflicts": preview.Conflicts, "auditId": auditID})
}

func (s *Service) buildExport(ctx context.Context, includeUserState bool) (ExportDocument, error) {
	doc := ExportDocument{FormatVersion: exportFormatVersion, ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), ApplicationVersion: "jmlab", IncludeUserState: includeUserState}
	doc.Items = []ExportItem{}
	doc.People = []ExportPerson{}
	doc.Studios = []ExportNamedEntity{}
	doc.Series = []ExportNamedEntity{}
	doc.Tags = []ExportTag{}
	doc.ItemPeople = []ExportItemPerson{}
	doc.ItemStudios = []ExportItemStudio{}
	doc.ItemSeries = []ExportItemSeries{}
	doc.ItemTags = []ExportItemTag{}
	doc.Libraries = []ExportLibrary{}
	doc.LibraryItems = []ExportLibraryItem{}
	doc.Collections = []ExportCollection{}
	doc.CollectionItems = []ExportCollectionItem{}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, source, COALESCE(source_id, ''), code, raw_code, title, COALESCE(translated_title, ''), COALESCE(original_title, ''), COALESCE(overview, ''), COALESCE(release_date, ''), year, runtime_seconds, source_rating, rating_count, source_url, metadata_status, is_adult, created_at, updated_at, item_type, COALESCE(parent_id, ''), index_number, provider_ids_json, extra_attributes_json, metadata_module_id FROM media_items WHERE deleted_at IS NULL ORDER BY code, id"))
	if err != nil {
		return doc, err
	}
	for rows.Next() {
		var item ExportItem
		var year, runtime sql.NullInt64
		var rating sql.NullFloat64
		var adult int
		var providerJSON, extraJSON string
		if err := rows.Scan(&item.ID, &item.Source, &item.SourceID, &item.Code, &item.RawCode, &item.Title, &item.TranslatedTitle, &item.OriginalTitle, &item.Overview, &item.ReleaseDate, &year, &runtime, &rating, &item.RatingCount, &item.SourceURL, &item.MetadataStatus, &adult, &item.CreatedAt, &item.UpdatedAt, &item.ItemType, &item.ParentID, &item.IndexNumber, &providerJSON, &extraJSON, &item.MetadataModuleID); err != nil {
			rows.Close()
			return doc, err
		}
		item.ProviderIDs = json.RawMessage(providerJSON)
		item.ExtraAttributes = json.RawMessage(extraJSON)
		item.Year = nullableInt(year)
		item.RuntimeSeconds = nullableInt(runtime)
		item.SourceRating = nullableFloat(rating)
		item.IsAdult = adult == 1
		doc.Items = append(doc.Items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return doc, err
	}
	rows.Close()
	if err := s.exportRelations(ctx, &doc); err != nil {
		return doc, err
	}
	if err := s.exportEntities(ctx, &doc, includeUserState); err != nil {
		return doc, err
	}
	if err := s.exportLibraries(ctx, &doc); err != nil {
		return doc, err
	}
	if err := s.exportCollections(ctx, &doc, includeUserState); err != nil {
		return doc, err
	}
	if includeUserState {
		if err := s.exportUserState(ctx, &doc); err != nil {
			return doc, err
		}
	}
	return doc, nil
}

func (s *Service) exportRelations(ctx context.Context, doc *ExportDocument) error {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, name, COALESCE(aliases_json, '[]'), COALESCE(gender, ''), status, COALESCE(birthday, ''), height_cm, COALESCE(cup_size, ''), COALESCE(biography, ''), COALESCE(source_url, ''), is_excluded, created_at, updated_at FROM people ORDER BY name, id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportPerson
		var aliases string
		var height sql.NullInt64
		var excluded int
		if err := rows.Scan(&x.ID, &x.Name, &aliases, &x.Gender, &x.Status, &x.Birthday, &height, &x.CupSize, &x.Biography, &x.SourceURL, &excluded, &x.CreatedAt, &x.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		x.Aliases = ensureJSON([]byte(aliases), "[]")
		x.HeightCM = nullableInt(height)
		x.IsExcluded = excluded == 1
		doc.People = append(doc.People, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, spec := range []struct {
		table string
		dest  *[]ExportNamedEntity
	}{{"studios", &doc.Studios}, {"series", &doc.Series}} {
		rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, name, COALESCE(source_name, ''), COALESCE(aliases_json, '[]'), is_excluded, sort_order, created_at, updated_at FROM "+spec.table+" ORDER BY name, id"))
		if err != nil {
			return err
		}
		for rows.Next() {
			var x ExportNamedEntity
			var aliases string
			var excluded int
			if err := rows.Scan(&x.ID, &x.Name, &x.SourceName, &aliases, &excluded, &x.SortOrder, &x.CreatedAt, &x.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			x.Aliases = ensureJSON([]byte(aliases), "[]")
			x.IsExcluded = excluded == 1
			*spec.dest = append(*spec.dest, x)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, name, COALESCE(source_name, ''), COALESCE(aliases_json, '[]'), kind, COALESCE(parent_id, ''), is_excluded, sort_order, created_at, updated_at FROM tags ORDER BY name, id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportTag
		var aliases string
		var excluded int
		if err := rows.Scan(&x.ID, &x.Name, &x.SourceName, &aliases, &x.Kind, &x.ParentID, &excluded, &x.SortOrder, &x.CreatedAt, &x.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		x.Aliases = ensureJSON([]byte(aliases), "[]")
		x.IsExcluded = excluded == 1
		doc.Tags = append(doc.Tags, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	linkQueries := []struct {
		query string
		scan  func(*sql.Rows) error
	}{
		{"SELECT item_id, person_id, role, sort_order FROM item_people WHERE item_id IN (SELECT id FROM media_items WHERE deleted_at IS NULL) ORDER BY item_id, sort_order, person_id", func(rows *sql.Rows) error {
			var x ExportItemPerson
			if err := rows.Scan(&x.ItemID, &x.PersonID, &x.Role, &x.SortOrder); err != nil {
				return err
			}
			doc.ItemPeople = append(doc.ItemPeople, x)
			return nil
		}},
		{"SELECT item_id, studio_id, sort_order FROM item_studios WHERE item_id IN (SELECT id FROM media_items WHERE deleted_at IS NULL) ORDER BY item_id, sort_order, studio_id", func(rows *sql.Rows) error {
			var x ExportItemStudio
			if err := rows.Scan(&x.ItemID, &x.StudioID, &x.SortOrder); err != nil {
				return err
			}
			doc.ItemStudios = append(doc.ItemStudios, x)
			return nil
		}},
		{"SELECT item_id, series_id, sort_order FROM item_series WHERE item_id IN (SELECT id FROM media_items WHERE deleted_at IS NULL) ORDER BY item_id, sort_order, series_id", func(rows *sql.Rows) error {
			var x ExportItemSeries
			if err := rows.Scan(&x.ItemID, &x.SeriesID, &x.SortOrder); err != nil {
				return err
			}
			doc.ItemSeries = append(doc.ItemSeries, x)
			return nil
		}},
		{"SELECT item_id, tag_id FROM item_tags WHERE item_id IN (SELECT id FROM media_items WHERE deleted_at IS NULL) ORDER BY item_id, tag_id", func(rows *sql.Rows) error {
			var x ExportItemTag
			if err := rows.Scan(&x.ItemID, &x.TagID); err != nil {
				return err
			}
			doc.ItemTags = append(doc.ItemTags, x)
			return nil
		}},
	}
	for _, link := range linkQueries {
		rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(link.query))
		if err != nil {
			return err
		}
		for rows.Next() {
			if err := link.scan(rows); err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (s *Service) exportLibraries(ctx context.Context, doc *ExportDocument) error {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, name, COALESCE(description, ''), item_type, filter_json, sort_json, is_public, is_adult, enabled, created_at, updated_at FROM virtual_libraries ORDER BY name, id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportLibrary
		var filter, sort string
		var public, adult, enabled int
		if err := rows.Scan(&x.ID, &x.Name, &x.Description, &x.ItemType, &filter, &sort, &public, &adult, &enabled, &x.CreatedAt, &x.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		x.Filter = ensureJSON([]byte(filter), "{}")
		x.Sort = ensureJSON([]byte(sort), "{}")
		x.IsPublic = public == 1
		x.IsAdult = adult == 1
		x.Enabled = enabled == 1
		doc.Libraries = append(doc.Libraries, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT library_id, item_id, position, created_at FROM library_items ORDER BY library_id, position, item_id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportLibraryItem
		if err := rows.Scan(&x.LibraryID, &x.ItemID, &x.Position, &x.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		doc.LibraryItems = append(doc.LibraryItems, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return nil
}

func (s *Service) exportCollections(ctx context.Context, doc *ExportDocument, includeUsers bool) error {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, name, COALESCE(description, ''), source, COALESCE(source_id, ''), COALESCE(owner_user_id, ''), is_public, created_at, updated_at FROM collections ORDER BY name, id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportCollection
		var public int
		if err := rows.Scan(&x.ID, &x.Name, &x.Description, &x.Source, &x.SourceID, &x.OwnerUserID, &public, &x.CreatedAt, &x.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		x.IsPublic = public == 1
		if !includeUsers {
			x.OwnerUserID = ""
		}
		doc.Collections = append(doc.Collections, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT collection_id, item_id, position, created_at FROM collection_items ORDER BY collection_id, position, item_id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportCollectionItem
		if err := rows.Scan(&x.CollectionID, &x.ItemID, &x.Position, &x.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		doc.CollectionItems = append(doc.CollectionItems, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return nil
}

func (s *Service) exportUserState(ctx context.Context, doc *ExportDocument) error {
	doc.Users = []ExportUser{}
	doc.UserItemStates = []ExportUserItemState{}
	doc.Playlists = []ExportPlaylist{}
	doc.PlaylistItems = []ExportPlaylistItem{}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, username, display_name, status FROM users ORDER BY username, id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportUser
		if err := rows.Scan(&x.ID, &x.Username, &x.DisplayName, &x.Status); err != nil {
			rows.Close()
			return err
		}
		doc.Users = append(doc.Users, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT user_id, item_id, is_favorite, is_watched, is_want_to_watch, playback_position_ticks, playback_runtime_ticks, COALESCE(last_played_at, ''), user_rating, COALESCE(note, ''), version, created_at, updated_at FROM user_item_states ORDER BY user_id, item_id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportUserItemState
		var favorite, watched, want int
		var last sql.NullString
		var rating sql.NullInt64
		if err := rows.Scan(&x.UserID, &x.ItemID, &favorite, &watched, &want, &x.PlaybackPositionTicks, &x.PlaybackRuntimeTicks, &last, &rating, &x.Note, &x.Version, &x.CreatedAt, &x.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		x.IsFavorite = favorite == 1
		x.IsWatched = watched == 1
		x.IsWantToWatch = want == 1
		x.LastPlayedAt = last.String
		if rating.Valid {
			value := int(rating.Int64)
			x.UserRating = &value
		}
		doc.UserItemStates = append(doc.UserItemStates, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, owner_user_id, name, COALESCE(overview, ''), is_public, created_at, updated_at FROM playlists ORDER BY owner_user_id, name, id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var playlist ExportPlaylist
		var public int
		if err := rows.Scan(&playlist.ID, &playlist.OwnerUserID, &playlist.Name, &playlist.Overview, &public, &playlist.CreatedAt, &playlist.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		playlist.IsPublic = public == 1
		doc.Playlists = append(doc.Playlists, playlist)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT playlist_id, item_id, position, added_by_user_id, created_at FROM playlist_items ORDER BY playlist_id, position, item_id"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var item ExportPlaylistItem
		if err := rows.Scan(&item.PlaylistID, &item.ItemID, &item.Position, &item.AddedByUserID, &item.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		doc.PlaylistItems = append(doc.PlaylistItems, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return nil
}

func (s *Service) previewImport(ctx context.Context, doc ExportDocument, options importOptions) (ImportPreview, error) {
	preview := ImportPreview{FormatVersion: doc.FormatVersion, Mode: options.Mode, IncludeUserState: options.IncludeUserState, Conflicts: []ImportConflict{}, Errors: []string{}}
	if err := validateDocument(doc, options); err != nil {
		preview.Errors = []string{err.Error()}
		return preview, err
	}
	if err := s.validateEntityIdentityConflicts(ctx, doc); err != nil {
		preview.Errors = []string{err.Error()}
		return preview, err
	}
	preview.Valid = true
	preview.Summary = ImportSummary{Items: len(doc.Items), People: len(doc.People), Studios: len(doc.Studios), Series: len(doc.Series), Tags: len(doc.Tags), Libraries: len(doc.Libraries), Collections: len(doc.Collections), Playlists: len(doc.Playlists), PlaylistItems: len(doc.PlaylistItems)}
	conflicts, err := s.findConflicts(ctx, doc, options)
	if err != nil {
		preview.Valid = false
		preview.Errors = []string{"读取现有数据失败"}
		return preview, err
	}
	preview.Conflicts = conflicts
	preview.Summary.Updated = len(conflicts)
	if options.IncludeUserState {
		preview.Summary.UserStates = len(doc.UserItemStates)
	}
	return preview, nil
}

func validateDocument(doc ExportDocument, options importOptions) error {
	if !supportedExportVersion(doc.FormatVersion) {
		return fmt.Errorf("不支持的导出格式版本: %s", doc.FormatVersion)
	}
	if options.Mode != "merge" && options.Mode != "overwrite" {
		return errors.New("导入策略只能是 merge 或 overwrite")
	}
	if len(doc.Items) > 100000 || len(doc.People) > 100000 || len(doc.Studios) > 100000 || len(doc.Series) > 100000 || len(doc.Tags) > 100000 || len(doc.Libraries) > 100000 || len(doc.Collections) > 100000 || len(doc.Users) > 100000 || len(doc.Playlists) > 100000 || len(doc.PlaylistItems) > 200000 {
		return errors.New("导入数据量超过限制")
	}
	itemIDs := map[string]bool{}
	codes := map[string]bool{}
	for _, item := range doc.Items {
		if item.ID == "" || strings.TrimSpace(item.Title) == "" {
			return errors.New("作品必须包含 id 和 title")
		}
		code, err := exportItemCode(item)
		if err != nil {
			return fmt.Errorf("作品 %s 的番号无效", item.ID)
		}
		if itemIDs[item.ID] {
			return fmt.Errorf("作品 ID 重复: %s", item.ID)
		}
		if codes[code] {
			return fmt.Errorf("作品番号重复: %s", code)
		}
		itemIDs[item.ID] = true
		codes[code] = true
		if hasSensitiveText(item.SourceURL) {
			return errors.New("作品来源 URL 含有敏感凭证")
		}
	}
	if err := validateExportMedia(doc); err != nil {
		return err
	}
	peopleIDs, err := uniqueEntityIDs("演员", len(doc.People), func(index int) string { return doc.People[index].ID })
	if err != nil {
		return err
	}
	for _, person := range doc.People {
		if hasSensitiveText(person.SourceURL) {
			return errors.New("演员来源 URL 含有敏感凭证")
		}
	}
	studioIDs, err := uniqueEntityIDs("片商", len(doc.Studios), func(index int) string { return doc.Studios[index].ID })
	if err != nil {
		return err
	}
	seriesIDs, err := uniqueEntityIDs("系列", len(doc.Series), func(index int) string { return doc.Series[index].ID })
	if err != nil {
		return err
	}
	tagIDs, err := uniqueEntityIDs("标签", len(doc.Tags), func(index int) string { return doc.Tags[index].ID })
	if err != nil {
		return err
	}
	libraryIDs, err := uniqueEntityIDs("媒体库", len(doc.Libraries), func(index int) string { return doc.Libraries[index].ID })
	if err != nil {
		return err
	}
	collectionIDs, err := uniqueEntityIDs("合集", len(doc.Collections), func(index int) string { return doc.Collections[index].ID })
	if err != nil {
		return err
	}
	for _, link := range doc.ItemPeople {
		if !itemIDs[link.ItemID] || !peopleIDs[link.PersonID] {
			return errors.New("作品演员关系引用不存在的作品或演员")
		}
		if !validEntityRole("people", defaultString(link.Role, "actor")) {
			return errors.New("人物关系角色无效")
		}
	}
	for _, link := range doc.ItemStudios {
		if !itemIDs[link.ItemID] || !studioIDs[link.StudioID] {
			return errors.New("作品片商关系引用不存在的作品或片商")
		}
	}
	for _, link := range doc.ItemSeries {
		if !itemIDs[link.ItemID] || !seriesIDs[link.SeriesID] {
			return errors.New("作品系列关系引用不存在的作品或系列")
		}
	}
	for _, link := range doc.ItemTags {
		if !itemIDs[link.ItemID] || !tagIDs[link.TagID] {
			return errors.New("作品标签关系引用不存在的作品或标签")
		}
	}
	for _, tag := range doc.Tags {
		if tag.ParentID != "" && !tagIDs[tag.ParentID] {
			return fmt.Errorf("标签引用不存在的父标签: %s", tag.ID)
		}
	}
	if err := validateEntityData(doc, itemIDs, map[string]map[string]bool{"people": peopleIDs, "studios": studioIDs, "series": seriesIDs, "tags": tagIDs}, options); err != nil {
		return err
	}
	for _, link := range doc.LibraryItems {
		if !itemIDs[link.ItemID] || !libraryIDs[link.LibraryID] {
			return errors.New("媒体库关系引用不存在的媒体库或作品")
		}
	}
	for _, link := range doc.CollectionItems {
		if !itemIDs[link.ItemID] || !collectionIDs[link.CollectionID] {
			return errors.New("合集关系引用不存在的合集或作品")
		}
	}
	if options.IncludeUserState {
		userIDs, err := uniqueEntityIDs("用户", len(doc.Users), func(index int) string { return doc.Users[index].ID })
		if err != nil {
			return err
		}
		for _, state := range doc.UserItemStates {
			if !itemIDs[state.ItemID] || !userIDs[state.UserID] {
				return errors.New("用户状态引用不存在的用户或作品")
			}
		}
		playlistIDs, err := uniqueEntityIDs("播放列表", len(doc.Playlists), func(index int) string { return doc.Playlists[index].ID })
		if err != nil {
			return err
		}
		for _, playlist := range doc.Playlists {
			if strings.TrimSpace(playlist.Name) == "" || strings.TrimSpace(playlist.OwnerUserID) == "" {
				return errors.New("播放列表必须包含 name 和 ownerUserId")
			}
			if !userIDs[playlist.OwnerUserID] {
				return errors.New("播放列表引用不存在的所有者")
			}
		}
		playlistItemKeys := map[string]bool{}
		playlistPositions := map[string]bool{}
		for _, item := range doc.PlaylistItems {
			if item.Position < 0 || strings.TrimSpace(item.AddedByUserID) == "" {
				return errors.New("播放列表项目必须包含非负 position 和 addedByUserId")
			}
			if !playlistIDs[item.PlaylistID] || !itemIDs[item.ItemID] || !userIDs[item.AddedByUserID] {
				return errors.New("播放列表项目引用不存在的列表、用户或作品")
			}
			itemKey := item.PlaylistID + "\x00" + item.ItemID
			if playlistItemKeys[itemKey] {
				return errors.New("播放列表项目重复")
			}
			playlistItemKeys[itemKey] = true
			positionKey := item.PlaylistID + "\x00" + strconv.Itoa(item.Position)
			if playlistPositions[positionKey] {
				return errors.New("播放列表项目 position 重复")
			}
			playlistPositions[positionKey] = true
		}
		for _, collection := range doc.Collections {
			if collection.OwnerUserID != "" && !userIDs[collection.OwnerUserID] {
				return errors.New("合集引用不存在的所有者")
			}
		}
	}
	return nil
}

func uniqueEntityIDs(label string, length int, getID func(int) string) (map[string]bool, error) {
	ids := make(map[string]bool, length)
	for index := 0; index < length; index++ {
		id := strings.TrimSpace(getID(index))
		if id == "" {
			return nil, fmt.Errorf("%s必须包含 id", label)
		}
		if ids[id] {
			return nil, fmt.Errorf("%s ID 重复: %s", label, id)
		}
		ids[id] = true
	}
	return ids, nil
}

func (s *Service) findConflicts(ctx context.Context, doc ExportDocument, options importOptions) ([]ImportConflict, error) {
	conflicts := []ImportConflict{}
	for _, item := range doc.Items {
		code, err := exportItemCode(item)
		if err != nil {
			return nil, err
		}
		var id, title string
		err = s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, title FROM media_items WHERE code = ? AND deleted_at IS NULL"), code).Scan(&id, &title)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if title != item.Title {
			resolution := "keep-existing"
			if options.Mode == "overwrite" {
				resolution = "overwrite"
			}
			conflicts = append(conflicts, ImportConflict{Entity: "item", Key: code, ID: id, Resolution: resolution, Fields: []ImportConflictField{{Field: "title", Existing: title, Incoming: item.Title, Resolution: resolution}}})
		}
	}
	if err := s.appendNameConflicts(ctx, &conflicts, "people", "person", personEntityNames(doc.People), options); err != nil {
		return nil, err
	}
	if err := s.appendNameConflicts(ctx, &conflicts, "studios", "studio", namedEntityNames(doc.Studios), options); err != nil {
		return nil, err
	}
	if err := s.appendNameConflicts(ctx, &conflicts, "series", "series", namedEntityNames(doc.Series), options); err != nil {
		return nil, err
	}
	if err := s.appendNameConflicts(ctx, &conflicts, "tags", "tag", tagEntityNames(doc.Tags), options); err != nil {
		return nil, err
	}
	for _, lib := range doc.Libraries {
		var id, name string
		err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, name FROM virtual_libraries WHERE id = ?"), lib.ID).Scan(&id, &name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if name != lib.Name {
			resolution := "keep-existing"
			if options.Mode == "overwrite" {
				resolution = "overwrite"
			}
			conflicts = append(conflicts, ImportConflict{Entity: "library", Key: lib.ID, ID: id, Resolution: resolution, Fields: []ImportConflictField{{Field: "name", Existing: name, Incoming: lib.Name, Resolution: resolution}}})
		}
	}
	if options.IncludeUserState {
		for _, playlist := range doc.Playlists {
			var id, name string
			err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, name FROM playlists WHERE id = ?"), playlist.ID).Scan(&id, &name)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if name != playlist.Name {
				resolution := "keep-existing"
				if options.Mode == "overwrite" {
					resolution = "overwrite"
				}
				conflicts = append(conflicts, ImportConflict{Entity: "playlist", Key: playlist.ID, ID: id, Resolution: resolution, Fields: []ImportConflictField{{Field: "name", Existing: name, Incoming: playlist.Name, Resolution: resolution}}})
			}
		}
	}
	return conflicts, nil
}

type nameConflictCandidate struct {
	ID   string
	Name string
}

func personEntityNames(values []ExportPerson) []nameConflictCandidate {
	result := make([]nameConflictCandidate, 0, len(values))
	for _, value := range values {
		result = append(result, nameConflictCandidate{ID: value.ID, Name: value.Name})
	}
	return result
}

func namedEntityNames(values []ExportNamedEntity) []nameConflictCandidate {
	result := make([]nameConflictCandidate, 0, len(values))
	for _, value := range values {
		result = append(result, nameConflictCandidate{ID: value.ID, Name: value.Name})
	}
	return result
}

func tagEntityNames(values []ExportTag) []nameConflictCandidate {
	result := make([]nameConflictCandidate, 0, len(values))
	for _, value := range values {
		result = append(result, nameConflictCandidate{ID: value.ID, Name: value.Name})
	}
	return result
}

func (s *Service) appendNameConflicts(ctx context.Context, conflicts *[]ImportConflict, table, entity string, values []nameConflictCandidate, options importOptions) error {
	for _, value := range values {
		var id, name string
		err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, name FROM "+table+" WHERE id = ?"), value.ID).Scan(&id, &name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if name == value.Name {
			continue
		}
		resolution := "keep-existing"
		if options.Mode == "overwrite" {
			resolution = "overwrite"
		}
		*conflicts = append(*conflicts, ImportConflict{Entity: entity, Key: value.ID, ID: id, Resolution: resolution, Fields: []ImportConflictField{{Field: "name", Existing: name, Incoming: value.Name, Resolution: resolution}}})
	}
	return nil
}

type importMapping struct {
	createdItems                                                                   map[string]bool
	items, people, studios, series, tags, libraries, collections, users, playlists map[string]string
}

func newImportMapping() importMapping {
	return importMapping{createdItems: map[string]bool{}, items: map[string]string{}, people: map[string]string{}, studios: map[string]string{}, series: map[string]string{}, tags: map[string]string{}, libraries: map[string]string{}, collections: map[string]string{}, users: map[string]string{}, playlists: map[string]string{}}
}

func (s *Service) applyImport(ctx context.Context, doc ExportDocument, options importOptions) (ImportSummary, error) {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return ImportSummary{}, err
	}
	defer tx.Rollback()
	summary, err := s.applyImportTx(ctx, tx, doc, options)
	if err != nil {
		return summary, err
	}
	return summary, tx.Commit()
}

func (s *Service) applyImportTx(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions) (ImportSummary, error) {
	existingEntities, err := entitySnapshot(ctx, tx)
	if err != nil {
		return ImportSummary{}, err
	}
	mapping := newImportMapping()
	summary := ImportSummary{Items: len(doc.Items), People: len(doc.People), Studios: len(doc.Studios), Series: len(doc.Series), Tags: len(doc.Tags), Libraries: len(doc.Libraries), Collections: len(doc.Collections), Playlists: len(doc.Playlists), PlaylistItems: len(doc.PlaylistItems)}
	if err := s.importPeople(ctx, tx, doc.People, options, mapping.people); err != nil {
		return summary, err
	}
	if err := s.importNamed(ctx, tx, "studios", doc.Studios, options, mapping.studios); err != nil {
		return summary, err
	}
	if err := s.importNamed(ctx, tx, "series", doc.Series, options, mapping.series); err != nil {
		return summary, err
	}
	if err := s.importTags(ctx, tx, doc.Tags, options, mapping.tags); err != nil {
		return summary, err
	}
	for _, item := range doc.Items {
		id, created, err := s.importItem(ctx, tx, item, options)
		if err != nil {
			return summary, err
		}
		mapping.items[item.ID] = id
		mapping.createdItems[item.ID] = created
		if created {
			summary.Created++
		} else {
			summary.Updated++
		}
	}
	if err := s.importMediaAttributes(ctx, tx, doc, options, mapping); err != nil {
		return summary, err
	}
	if err := s.importEntityData(ctx, tx, doc, options, mapping, existingEntities); err != nil {
		return summary, err
	}
	if err := s.importLinks(ctx, tx, doc, options, mapping); err != nil {
		return summary, err
	}
	if options.IncludeUserState {
		if err := s.importUserState(ctx, tx, doc, options, mapping, &summary); err != nil {
			return summary, err
		}
		if err := s.importEntityUserState(ctx, tx, doc, options, mapping); err != nil {
			return summary, err
		}
	}
	if err := s.importLibraries(ctx, tx, doc, options, mapping); err != nil {
		return summary, err
	}
	if err := s.importCollections(ctx, tx, doc, options, mapping); err != nil {
		return summary, err
	}
	return summary, nil
}

func (s *Service) importPeople(ctx context.Context, tx *sql.Tx, values []ExportPerson, options importOptions, mapped map[string]string) error {
	for _, x := range values {
		var id string
		err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM people WHERE id = ?"), x.ID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id = defaultID(x.ID, "person")
			_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO entities (entity_kind,id, name, aliases_json, gender, status, birthday, height_cm, cup_size, biography, source_url, is_excluded, created_at, updated_at) VALUES ('people',?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, x.Name, string(ensureJSON(x.Aliases, "[]")), nullable(x.Gender), defaultString(x.Status, "unknown"), nullable(x.Birthday), x.HeightCM, nullable(x.CupSize), nullable(x.Biography), nullable(x.SourceURL), boolInt(x.IsExcluded), timestamp(x.CreatedAt), timestamp(x.UpdatedAt))
		} else if err == nil && options.Mode == "overwrite" {
			_, err = tx.ExecContext(ctx, s.db.Rebind("UPDATE entities SET name = ?, aliases_json = ?, gender = ?, status = ?, birthday = ?, height_cm = ?, cup_size = ?, biography = ?, source_url = ?, is_excluded = ?, updated_at = ? WHERE entity_kind='people' AND id = ?"), x.Name, string(ensureJSON(x.Aliases, "[]")), nullable(x.Gender), defaultString(x.Status, "unknown"), nullable(x.Birthday), x.HeightCM, nullable(x.CupSize), nullable(x.Biography), nullable(x.SourceURL), boolInt(x.IsExcluded), timestamp(""), id)
		}
		if err != nil {
			return err
		}
		mapped[x.ID] = id
	}
	return nil
}

func (s *Service) importNamed(ctx context.Context, tx *sql.Tx, table string, values []ExportNamedEntity, options importOptions, mapped map[string]string) error {
	for _, x := range values {
		id, err := s.upsertNamed(ctx, tx, table, x.ID, x.Name, x.SourceName, x.Aliases, "", x.IsExcluded, x.SortOrder, options)
		if err != nil {
			return err
		}
		mapped[x.ID] = id
	}
	return nil
}
func (s *Service) importTags(ctx context.Context, tx *sql.Tx, values []ExportTag, options importOptions, mapped map[string]string) error {
	for _, x := range values {
		id, err := s.upsertNamed(ctx, tx, "tags", x.ID, x.Name, x.SourceName, x.Aliases, x.Kind, x.IsExcluded, x.SortOrder, options)
		if err != nil {
			return err
		}
		mapped[x.ID] = id
	}
	for _, x := range values {
		if x.ParentID != "" && mapped[x.ParentID] != "" {
			if _, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE entities SET parent_id = ? WHERE entity_kind='tags' AND id = ?"), mapped[x.ParentID], mapped[x.ID]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) upsertNamed(ctx context.Context, tx *sql.Tx, table, requestedID, name, sourceName string, aliases json.RawMessage, kind string, excluded bool, sortOrder int, options importOptions) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM "+table+" WHERE id = ?"), requestedID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = defaultID(requestedID, strings.TrimSuffix(table, "s"))
		if table == "tags" {
			_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO entities (entity_kind,id, name, source_name, aliases_json, kind, is_excluded, sort_order, created_at, updated_at) VALUES ('tags',?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, name, nullable(sourceName), string(ensureJSON(aliases, "[]")), defaultString(kind, "custom"), boolInt(excluded), sortOrder, timestamp(""), timestamp(""))
		} else {
			_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO entities (entity_kind,id, name, source_name, aliases_json, is_excluded, sort_order, created_at, updated_at) VALUES ('"+table+"',?, ?, ?, ?, ?, ?, ?, ?)"), id, name, nullable(sourceName), string(ensureJSON(aliases, "[]")), boolInt(excluded), sortOrder, timestamp(""), timestamp(""))
		}
		return id, err
	}
	if err != nil {
		return "", err
	}
	if options.Mode == "overwrite" {
		query := "UPDATE entities SET name = ?, source_name = ?, aliases_json = ?, is_excluded = ?, sort_order = ?, updated_at = ? WHERE entity_kind='" + table + "' AND id = ?"
		args := []any{name, nullable(sourceName), string(ensureJSON(aliases, "[]")), boolInt(excluded), sortOrder, timestamp(""), id}
		if table == "tags" {
			query = "UPDATE entities SET name = ?, source_name = ?, aliases_json = ?, kind = ?, is_excluded = ?, sort_order = ?, updated_at = ? WHERE entity_kind='tags' AND id = ?"
			args = []any{name, nullable(sourceName), string(ensureJSON(aliases, "[]")), defaultString(kind, "custom"), boolInt(excluded), sortOrder, timestamp(""), id}
		}
		_, err = tx.ExecContext(ctx, s.db.Rebind(query), args...)
	}
	return id, err
}

func (s *Service) importItem(ctx context.Context, tx *sql.Tx, x ExportItem, options importOptions) (string, bool, error) {
	code, err := exportItemCode(x)
	if err != nil {
		return "", false, err
	}
	var id string
	err = tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM media_items WHERE code = ? AND deleted_at IS NULL"), code).Scan(&id)
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return "", false, err
	}
	if created {
		id = defaultID(x.ID, "item")
		_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO media_items (id, source, source_id, code, raw_code, title, translated_title, original_title, overview, release_date, year, runtime_seconds, source_rating, rating_count, source_url, metadata_status, is_adult, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, defaultString(x.Source, "javdb"), nullable(x.SourceID), code, defaultString(x.RawCode, x.Code), x.Title, nullable(x.TranslatedTitle), nullable(x.OriginalTitle), nullable(x.Overview), nullable(x.ReleaseDate), x.Year, x.RuntimeSeconds, x.SourceRating, x.RatingCount, exportSourceURL(x, code), defaultString(x.MetadataStatus, "complete"), boolInt(x.IsAdult), timestamp(x.CreatedAt), timestamp(x.UpdatedAt))
		return id, true, err
	}
	if options.Mode == "overwrite" {
		_, err = tx.ExecContext(ctx, s.db.Rebind("UPDATE media_items SET source = ?, source_id = ?, raw_code = ?, title = ?, translated_title = ?, original_title = ?, overview = ?, release_date = ?, year = ?, runtime_seconds = ?, source_rating = ?, rating_count = ?, source_url = ?, metadata_status = ?, is_adult = ?, updated_at = ? WHERE id = ?"), defaultString(x.Source, "javdb"), nullable(x.SourceID), defaultString(x.RawCode, x.Code), x.Title, nullable(x.TranslatedTitle), nullable(x.OriginalTitle), nullable(x.Overview), nullable(x.ReleaseDate), x.Year, x.RuntimeSeconds, x.SourceRating, x.RatingCount, exportSourceURL(x, code), defaultString(x.MetadataStatus, "complete"), boolInt(x.IsAdult), timestamp(""), id)
	}
	return id, false, err
}

func (s *Service) importLinks(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions, m importMapping) error {
	for _, item := range doc.Items {
		if options.Mode == "overwrite" {
			if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM item_entities WHERE item_id=?"), m.items[item.ID]); err != nil {
				return err
			}
		}
	}
	if len(doc.EntityLinks) > 0 {
		return s.importEntityLinks(ctx, tx, doc, m)
	}
	for _, x := range doc.ItemPeople {
		if m.people[x.PersonID] == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO item_entities (item_id, entity_kind, entity_id, role, sort_order) VALUES (?, 'people', ?, ?, ?) ON CONFLICT(item_id, entity_kind, entity_id, role) DO UPDATE SET sort_order = excluded.sort_order"), m.items[x.ItemID], m.people[x.PersonID], defaultString(x.Role, "actor"), x.SortOrder); err != nil {
			return err
		}
	}
	for _, x := range doc.ItemStudios {
		if m.studios[x.StudioID] == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO item_entities (item_id, entity_kind, entity_id, role, sort_order) VALUES (?, 'studios', ?, 'studio', ?) ON CONFLICT(item_id, entity_kind, entity_id, role) DO UPDATE SET sort_order = excluded.sort_order"), m.items[x.ItemID], m.studios[x.StudioID], x.SortOrder); err != nil {
			return err
		}
	}
	for _, x := range doc.ItemSeries {
		if m.series[x.SeriesID] == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO item_entities (item_id, entity_kind, entity_id, role, sort_order) VALUES (?, 'series', ?, 'series', ?) ON CONFLICT(item_id, entity_kind, entity_id, role) DO UPDATE SET sort_order = excluded.sort_order"), m.items[x.ItemID], m.series[x.SeriesID], x.SortOrder); err != nil {
			return err
		}
	}
	for _, x := range doc.ItemTags {
		if m.tags[x.TagID] == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO item_entities (item_id, entity_kind, entity_id, role) VALUES (?, 'tags', ?, 'tag') ON CONFLICT(item_id, entity_kind, entity_id, role) DO NOTHING"), m.items[x.ItemID], m.tags[x.TagID]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) importLibraries(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions, m importMapping) error {
	for _, x := range doc.Libraries {
		var id string
		err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM virtual_libraries WHERE id = ?"), x.ID).Scan(&id)
		created := errors.Is(err, sql.ErrNoRows)
		if err != nil && !created {
			return err
		}
		if created {
			id = defaultID(x.ID, "library")
			_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO virtual_libraries (id, name, description, item_type, filter_json, sort_json, is_public, is_adult, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, x.Name, x.Description, defaultString(x.ItemType, "movie"), string(ensureJSON(x.Filter, "{}")), string(ensureJSON(x.Sort, "{}")), boolInt(x.IsPublic), boolInt(x.IsAdult), boolInt(x.Enabled), timestamp(x.CreatedAt), timestamp(x.UpdatedAt))
		} else if options.Mode == "overwrite" {
			_, err = tx.ExecContext(ctx, s.db.Rebind("UPDATE virtual_libraries SET name = ?, description = ?, item_type = ?, filter_json = ?, sort_json = ?, is_public = ?, is_adult = ?, enabled = ?, updated_at = ? WHERE id = ?"), x.Name, x.Description, defaultString(x.ItemType, "movie"), string(ensureJSON(x.Filter, "{}")), string(ensureJSON(x.Sort, "{}")), boolInt(x.IsPublic), boolInt(x.IsAdult), boolInt(x.Enabled), timestamp(""), id)
		}
		if err != nil {
			return err
		}
		m.libraries[x.ID] = id
		if options.Mode == "overwrite" {
			if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM library_items WHERE library_id = ?"), id); err != nil {
				return err
			}
		}
	}
	for _, link := range doc.LibraryItems {
		if m.libraries[link.LibraryID] == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO library_items (library_id, item_id, position, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(library_id, item_id) DO UPDATE SET position = excluded.position"), m.libraries[link.LibraryID], m.items[link.ItemID], link.Position, timestamp(link.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) importCollections(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions, m importMapping) error {
	for _, x := range doc.Collections {
		var id string
		err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM collections WHERE id = ?"), x.ID).Scan(&id)
		created := errors.Is(err, sql.ErrNoRows)
		if err != nil && !created {
			return err
		}
		if created {
			id = defaultID(x.ID, "collection")
			_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO collections (id, name, description, source, source_id, owner_user_id, is_public, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, x.Name, x.Description, defaultString(x.Source, "manual"), nullable(x.SourceID), nullable(m.users[x.OwnerUserID]), boolInt(x.IsPublic), timestamp(x.CreatedAt), timestamp(x.UpdatedAt))
		} else if options.Mode == "overwrite" {
			_, err = tx.ExecContext(ctx, s.db.Rebind("UPDATE collections SET name = ?, description = ?, source = ?, source_id = ?, owner_user_id = ?, is_public = ?, updated_at = ? WHERE id = ?"), x.Name, x.Description, defaultString(x.Source, "manual"), nullable(x.SourceID), nullable(m.users[x.OwnerUserID]), boolInt(x.IsPublic), timestamp(""), id)
		}
		if err != nil {
			return err
		}
		m.collections[x.ID] = id
		if options.Mode == "overwrite" {
			if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM collection_items WHERE collection_id = ?"), id); err != nil {
				return err
			}
		}
	}
	for _, link := range doc.CollectionItems {
		if m.collections[link.CollectionID] == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO collection_items (collection_id, item_id, position, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(collection_id, item_id) DO UPDATE SET position = excluded.position"), m.collections[link.CollectionID], m.items[link.ItemID], link.Position, timestamp(link.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) importUserState(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions, m importMapping, summary *ImportSummary) error {
	for _, x := range doc.Users {
		var id string
		err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM users WHERE id = ? OR LOWER(username) = LOWER(?) LIMIT 1"), x.ID, x.Username).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			summary.Skipped++
			continue
		}
		if err != nil {
			return err
		}
		m.users[x.ID] = id
	}
	for _, x := range doc.UserItemStates {
		userID, itemID := m.users[x.UserID], m.items[x.ItemID]
		if userID == "" || itemID == "" {
			summary.Skipped++
			continue
		}
		_, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_item_states (user_id, item_id, is_favorite, is_watched, is_want_to_watch, playback_position_ticks, playback_runtime_ticks, last_played_at, user_rating, note, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(user_id, item_id) DO UPDATE SET is_favorite = excluded.is_favorite, is_watched = excluded.is_watched, is_want_to_watch = excluded.is_want_to_watch, playback_position_ticks = excluded.playback_position_ticks, playback_runtime_ticks = excluded.playback_runtime_ticks, last_played_at = excluded.last_played_at, user_rating = excluded.user_rating, note = excluded.note, version = excluded.version, updated_at = excluded.updated_at"), userID, itemID, boolInt(x.IsFavorite), boolInt(x.IsWatched), boolInt(x.IsWantToWatch), x.PlaybackPositionTicks, x.PlaybackRuntimeTicks, nullable(x.LastPlayedAt), x.UserRating, nullable(x.Note), maxInt(x.Version, 1), timestamp(x.CreatedAt), timestamp(x.UpdatedAt))
		if err != nil {
			return err
		}
		summary.UserStates++
	}
	return s.importPlaylists(ctx, tx, doc.Playlists, doc.PlaylistItems, options, m, summary)
}

func (s *Service) importPlaylists(ctx context.Context, tx *sql.Tx, values []ExportPlaylist, items []ExportPlaylistItem, options importOptions, m importMapping, summary *ImportSummary) error {
	for _, x := range values {
		ownerID := m.users[x.OwnerUserID]
		if ownerID == "" {
			summary.Skipped++
			continue
		}
		var id string
		err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM playlists WHERE id = ? OR (owner_user_id = ? AND LOWER(name) = LOWER(?)) LIMIT 1"), x.ID, ownerID, x.Name).Scan(&id)
		created := errors.Is(err, sql.ErrNoRows)
		if err != nil && !created {
			return err
		}
		if created {
			id = defaultID(x.ID, "playlist")
			_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO playlists (id, owner_user_id, name, overview, is_public, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)"), id, ownerID, x.Name, nullable(x.Overview), boolInt(x.IsPublic), timestamp(x.CreatedAt), timestamp(x.UpdatedAt))
			summary.Created++
		} else if options.Mode == "overwrite" {
			_, err = tx.ExecContext(ctx, s.db.Rebind("UPDATE playlists SET owner_user_id = ?, name = ?, overview = ?, is_public = ?, updated_at = ? WHERE id = ?"), ownerID, x.Name, nullable(x.Overview), boolInt(x.IsPublic), timestamp(""), id)
			summary.Updated++
		}
		if err != nil {
			return err
		}
		m.playlists[x.ID] = id
		if options.Mode == "overwrite" {
			if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM playlist_items WHERE playlist_id = ?"), id); err != nil {
				return err
			}
		}
	}
	for _, x := range items {
		playlistID := m.playlists[x.PlaylistID]
		itemID := m.items[x.ItemID]
		addedByID := m.users[x.AddedByUserID]
		if playlistID == "" || itemID == "" || addedByID == "" {
			summary.Skipped++
			continue
		}
		if err := s.upsertPlaylistItem(ctx, tx, playlistID, itemID, x.Position, addedByID, x.CreatedAt, options.Mode == "overwrite"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertPlaylistItem(ctx context.Context, tx *sql.Tx, playlistID, itemID string, position int, addedByID, createdAt string, overwrite bool) error {
	if overwrite {
		_, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO playlist_items (playlist_id, item_id, position, added_by_user_id, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(playlist_id, item_id) DO UPDATE SET position = excluded.position, added_by_user_id = excluded.added_by_user_id, created_at = excluded.created_at"), playlistID, itemID, position, addedByID, timestamp(createdAt))
		return err
	}
	var existingItem string
	err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT item_id FROM playlist_items WHERE playlist_id = ? AND position = ?"), playlistID, position).Scan(&existingItem)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existingItem != itemID {
		var end int
		if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COALESCE(MAX(position), -1) + 1 FROM playlist_items WHERE playlist_id = ?"), playlistID).Scan(&end); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE playlist_items SET position = ? WHERE playlist_id = ? AND item_id = ?"), end, playlistID, existingItem); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO playlist_items (playlist_id, item_id, position, added_by_user_id, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(playlist_id, item_id) DO UPDATE SET position = excluded.position, added_by_user_id = excluded.added_by_user_id, created_at = excluded.created_at"), playlistID, itemID, position, addedByID, timestamp(createdAt))
	return err
}

func decodeImportPayload(r *http.Request) (ExportDocument, importOptions, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return ExportDocument{}, importOptions{}, errors.New("导入内容不能为空")
	}
	var envelope struct {
		Document         json.RawMessage `json:"document"`
		Mode             string          `json:"mode"`
		Strategy         string          `json:"strategy"`
		IncludeUserState *bool           `json:"includeUserState"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ExportDocument{}, importOptions{}, errors.New("导入内容必须是 JSON")
	}
	raw := data
	if len(envelope.Document) > 0 {
		raw = envelope.Document
	}
	var doc ExportDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, importOptions{}, errors.New("JSON 导出结构不正确")
	}
	mode := strings.ToLower(strings.TrimSpace(envelope.Mode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(envelope.Strategy))
	}
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	}
	if mode == "" {
		mode = "merge"
	}
	include := doc.IncludeUserState
	if envelope.IncludeUserState != nil {
		include = *envelope.IncludeUserState
	}
	return doc, importOptions{Mode: mode, IncludeUserState: include}, nil
}
func decodeBackupJSON(r *http.Request, target any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(target)
}
func previewError(err error, doc ExportDocument, options importOptions) ImportPreview {
	return ImportPreview{Valid: false, FormatVersion: doc.FormatVersion, Mode: options.Mode, IncludeUserState: options.IncludeUserState, Conflicts: []ImportConflict{}, Errors: []string{err.Error()}}
}
func nullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}
func nullableFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}
func ensureJSON(value []byte, fallback string) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return json.RawMessage(fallback)
	}
	return append(json.RawMessage(nil), value...)
}
func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func timestamp(value string) string {
	if strings.TrimSpace(value) == "" {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return value
}
func defaultID(value, prefix string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func hasSensitiveText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"authorization", "bearer ", "cookie=", "token=", "password=", "secret="} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
func (s *Service) writeAudit(ctx context.Context, actor, action, resource, resourceID, requestID, metadata string) string {
	id := defaultID("", "aud")
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, actor, action, resource, resourceID, requestID, "success", metadata, time.Now().UTC().Format(time.RFC3339Nano))
	return id
}
