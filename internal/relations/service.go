package relations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/repository"
)

type Service struct {
	db          *repository.Database
	authService *auth.Service
}

type Entity struct {
	Identifiers      []Identifier      `json:"identifiers,omitempty"`
	OriginalName     string            `json:"originalName,omitempty"`
	ProviderIDs      map[string]string `json:"providerIds,omitempty"`
	SourceIDs        map[string]string `json:"sourceIds,omitempty"`
	AttributesLocked bool              `json:"attributesLocked"`
	ExtraAttributes  map[string]string `json:"extraAttributes,omitempty"`
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	Aliases          []string          `json:"aliases,omitempty"`
	SourceName       string            `json:"sourceName,omitempty"`
	SourceURL        string            `json:"sourceUrl,omitempty"`
	Gender           string            `json:"gender,omitempty"`
	Status           string            `json:"status,omitempty"`
	Birthday         string            `json:"birthday,omitempty"`
	HeightCM         *int              `json:"heightCm,omitempty"`
	CupSize          string            `json:"cupSize,omitempty"`
	Measurements     map[string]string `json:"measurements,omitempty"`
	Biography        string            `json:"biography,omitempty"`
	ParentID         string            `json:"parentId,omitempty"`
	TagKind          string            `json:"tagKind,omitempty"`
	IsExcluded       bool              `json:"isExcluded"`
	RelatedItem      []RelatedItem     `json:"relatedItems,omitempty"`
}

type Identifier struct {
	Namespace string `json:"namespace"`
	Value     string `json:"value"`
}

type RelatedItem struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Title string `json:"title"`
}

type spec struct {
	kind, table, relTable, relColumn, prefix string
}

var specs = map[string]spec{
	"people":  {kind: "people", table: "people", relTable: "item_people", relColumn: "person_id", prefix: "person"},
	"studios": {kind: "studios", table: "studios", relTable: "item_studios", relColumn: "studio_id", prefix: "studio"},
	"series":  {kind: "series", table: "series", relTable: "item_series", relColumn: "series_id", prefix: "series"},
	"tags":    {kind: "tags", table: "tags", relTable: "item_tags", relColumn: "tag_id", prefix: "tag"},
}

func NewService(db *repository.Database, authService *auth.Service) *Service {
	return &Service{db: db, authService: authService}
}

func (s *Service) RegisterRoutes(router chi.Router) {
	router.With(s.authService.RequireAuth).Get("/me/relations/{kind}/{entityID}/note", s.getNote)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Put("/me/relations/{kind}/{entityID}/note", s.putNote)
	for _, kind := range []string{"people", "studios", "series", "tags"} {
		sp := specs[kind]
		router.With(s.authService.RequireAuth).Get("/"+kind, s.list(sp))
		router.With(s.authService.RequireAuth).Get("/"+kind+"/{entityID}", s.detail(sp))
		router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/"+kind, s.create(sp))
		router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/"+kind+"/batch", s.batch(sp))
		router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Patch("/"+kind+"/{entityID}", s.update(sp))
		router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/"+kind+"/{entityID}/merge", s.merge(sp))
	}
}

func (s *Service) list(sp spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, _ := auth.PrincipalFromContext(r.Context())
		query := fmt.Sprintf("SELECT DISTINCT %s.id, %s.name, %s.aliases_json, %s.is_excluded", sp.table, sp.table, sp.table, sp.table)
		args := make([]any, 0, 3)
		if sp.kind == "people" {
			query += ", COALESCE(people.gender, ''), COALESCE(people.status, ''), COALESCE(people.birthday, ''), people.height_cm, COALESCE(people.cup_size, ''), COALESCE(people.measurements_json, '{}'), COALESCE(people.biography, ''), COALESCE(people.source_url, '')"
		} else if sp.kind == "tags" {
			query += ", COALESCE(tags.parent_id, ''), COALESCE(tags.kind, ''), COALESCE(tags.source_name, '')"
		} else {
			query += ", COALESCE(" + sp.table + ".source_name, '')"
		}
		query += ", " + sp.table + ".original_name, " + sp.table + ".extra_attributes_json, " + sp.table + ".attributes_locked"
		query += " FROM " + sp.table
		if !elevated(principal.User) {
			visibility, accessArgs := auth.MediaVisibilitySQL("linked_media", principal.User.ID, false, false)
			query += " JOIN " + sp.relTable + " rel ON rel." + sp.relColumn + "=" + sp.table + ".id JOIN media_items linked_media ON linked_media.id=rel.item_id AND (" + visibility + ")"
			args = append(args, accessArgs...)
		}
		where := make([]string, 0, 2)
		includeExcluded := elevated(principal.User) && strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("includeExcluded")), "1")
		if !includeExcluded {
			where = append(where, sp.table+".is_excluded = 0")
		}
		if search := strings.TrimSpace(r.URL.Query().Get("q")); search != "" {
			where = append(where, "(LOWER("+sp.table+".name) LIKE LOWER(?) OR LOWER("+sp.table+".aliases_json) LIKE LOWER(?))")
			pattern := "%" + search + "%"
			args = append(args, pattern, pattern)
		}
		if len(where) > 0 {
			query += " WHERE " + strings.Join(where, " AND ")
		}
		query += " ORDER BY " + sp.table + ".name, " + sp.table + ".id LIMIT ? OFFSET ?"
		limit, offset := boundedPage(r)
		args = append(args, limit, offset)
		rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), args...)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "RELATIONS_READ_FAILED", "读取关系实体失败")
			return
		}
		defer rows.Close()
		entities := make([]Entity, 0, limit)
		for rows.Next() {
			entity, err := scanEntity(rows, sp)
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, "RELATIONS_READ_FAILED", "读取关系实体失败")
				return
			}
			entities = append(entities, entity)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": entities, "limit": limit, "offset": offset})
	}
}

func (s *Service) batch(sp spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, _ := auth.PrincipalFromContext(r.Context())
		var input struct {
			IDs    []string `json:"ids"`
			Action string   `json:"action"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || len(input.IDs) == 0 || len(input.IDs) > 500 {
			writeError(w, r, http.StatusBadRequest, "INVALID_BATCH", "ids 必须包含 1 到 500 个关系实体")
			return
		}
		action := strings.ToLower(strings.TrimSpace(input.Action))
		if action != "exclude" && action != "include" {
			writeError(w, r, http.StatusBadRequest, "INVALID_BATCH_ACTION", "action 只能是 exclude 或 include")
			return
		}
		value := 0
		if action == "exclude" {
			value = 1
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		tx, err := s.db.DB().BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "RELATION_BATCH_FAILED", "批量更新关系实体失败")
			return
		}
		defer tx.Rollback()
		updated := 0
		for _, id := range input.IDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			result, execErr := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE entities SET is_excluded = ?, updated_at = ? WHERE entity_kind = '"+sp.kind+"' AND id = ?"), value, now, id)
			if execErr != nil {
				writeError(w, r, http.StatusBadRequest, "RELATION_BATCH_FAILED", "批量更新关系实体失败")
				return
			}
			count, _ := result.RowsAffected()
			updated += int(count)
		}
		if err := tx.Commit(); err != nil {
			writeError(w, r, http.StatusInternalServerError, "RELATION_BATCH_FAILED", "批量更新关系实体失败")
			return
		}
		s.audit(r.Context(), principal.User.ID, "batch_"+action+"_relation", sp.kind, strings.Join(input.IDs, ","), middleware.GetReqID(r.Context()))
		writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "action": action, "updated": updated})
	}
}

func (s *Service) detail(sp spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, _ := auth.PrincipalFromContext(r.Context())
		entity, err := s.loadEntity(r.Context(), sp, chi.URLParam(r, "entityID"), principal.User)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "关系实体不存在")
			return
		}
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "RELATION_READ_FAILED", "读取关系实体失败")
			return
		}
		entity.RelatedItem = s.relatedItems(r.Context(), sp, entity.ID, principal.User)
		writeJSON(w, http.StatusOK, entity)
	}
}

func relationEntityType(kind string) (string, bool) {
	entityType, ok := map[string]string{"people": "person", "studios": "studio", "series": "series", "tags": "tag"}[kind]
	return entityType, ok
}

func (s *Service) getNote(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	kind := chi.URLParam(r, "kind")
	entityType, ok := relationEntityType(kind)
	if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "关系实体不存在")
		return
	}
	if _, err := s.loadEntity(r.Context(), specs[kind], chi.URLParam(r, "entityID"), principal.User); err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "关系实体不存在或不可见")
		return
	}
	var note string
	err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT note FROM user_entity_notes WHERE user_id = ? AND entity_type = ? AND entity_id = ?"), principal.User.ID, entityType, chi.URLParam(r, "entityID")).Scan(&note)
	if errors.Is(err, sql.ErrNoRows) {
		note = ""
	} else if err != nil {
		writeError(w, r, http.StatusInternalServerError, "NOTE_READ_FAILED", "读取个人备注失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"entityType": entityType, "entityId": chi.URLParam(r, "entityID"), "note": note})
}

func (s *Service) putNote(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	kind := chi.URLParam(r, "kind")
	entityType, ok := relationEntityType(kind)
	if !ok {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "关系实体不存在")
		return
	}
	if _, err := s.loadEntity(r.Context(), specs[kind], chi.URLParam(r, "entityID"), principal.User); err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "关系实体不存在或不可见")
		return
	}
	var input struct {
		Note string `json:"note"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len([]rune(input.Note)) > 2000 {
		writeError(w, r, http.StatusBadRequest, "INVALID_NOTE", "个人备注格式不正确或超过 2000 个字符")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO user_entity_notes (user_id, entity_type, entity_id, note, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(user_id, entity_type, entity_id) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at"), principal.User.ID, entityType, chi.URLParam(r, "entityID"), input.Note, now, now)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "NOTE_WRITE_FAILED", "保存个人备注失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "update_relation_note", entityType, chi.URLParam(r, "entityID"), middleware.GetReqID(r.Context()))
	writeJSON(w, http.StatusOK, map[string]string{"entityType": entityType, "entityId": chi.URLParam(r, "entityID"), "note": input.Note})
}

func (s *Service) create(sp spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, _ := auth.PrincipalFromContext(r.Context())
		input, err := decodeInput(r)
		if err != nil || strings.TrimSpace(input.Name) == "" {
			writeError(w, r, http.StatusBadRequest, "INVALID_RELATION", "名称不能为空")
			return
		}
		id := newID(sp.prefix)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		aliases, _ := json.Marshal(cleanAliases(input.Aliases))
		var execErr error
		switch sp.kind {
		case "people":
			measurements, _ := json.Marshal(cleanMeasurements(input.Measurements))
			_, execErr = s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO entities (entity_kind,id, name, aliases_json, gender, status, birthday, height_cm, cup_size, measurements_json, biography, source_url, created_at, updated_at) VALUES ('people',?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), id, strings.TrimSpace(input.Name), string(aliases), nullable(input.Gender), defaultValue(input.Status, "unknown"), nullable(input.Birthday), input.HeightCM, nullable(input.CupSize), string(measurements), nullable(input.Biography), nullable(input.SourceURL), now, now)
		case "tags":
			if err := s.validateTagParent(r.Context(), "", input.ParentID); err != nil {
				writeError(w, r, http.StatusBadRequest, "TAG_PARENT_INVALID", err.Error())
				return
			}
			_, execErr = s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO entities (entity_kind,id, name, aliases_json, source_name, kind, parent_id, created_at, updated_at) VALUES ('tags',?, ?, ?, ?, ?, ?, ?, ?)"), id, strings.TrimSpace(input.Name), string(aliases), nullable(input.SourceName), defaultValue(input.TagKind, "custom"), nullable(input.ParentID), now, now)
		case "studios", "series":
			_, execErr = s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO entities (entity_kind,id, name, aliases_json, source_name, created_at, updated_at) VALUES ('"+sp.kind+"',?, ?, ?, ?, ?, ?)"), id, strings.TrimSpace(input.Name), string(aliases), nullable(input.SourceName), now, now)
		}
		if execErr != nil {
			writeError(w, r, http.StatusConflict, "RELATION_CREATE_FAILED", "关系实体创建失败")
			return
		}
		s.audit(r.Context(), principal.User.ID, "create_relation", sp.kind, id, middleware.GetReqID(r.Context()))
		entity, _ := s.loadEntity(r.Context(), sp, id, principal.User)
		writeJSON(w, http.StatusCreated, entity)
	}
}

func (s *Service) update(sp spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, _ := auth.PrincipalFromContext(r.Context())
		entityID := chi.URLParam(r, "entityID")
		input, err := decodeInput(r)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_RELATION", "关系实体格式不正确")
			return
		}
		updates := make([]string, 0, 8)
		args := make([]any, 0, 10)
		if input.Name != "" {
			updates = append(updates, "name = ?")
			args = append(args, strings.TrimSpace(input.Name))
		}
		if input.Aliases != nil {
			value, _ := json.Marshal(cleanAliases(input.Aliases))
			updates = append(updates, "aliases_json = ?")
			args = append(args, string(value))
		}
		if sp.kind == "people" {
			appendOptional(&updates, &args, "gender", input.Gender)
			appendOptional(&updates, &args, "status", input.Status)
			appendOptional(&updates, &args, "birthday", input.Birthday)
			if input.HeightCM != nil {
				updates = append(updates, "height_cm = ?")
				args = append(args, input.HeightCM)
			}
			appendOptional(&updates, &args, "cup_size", input.CupSize)
			if input.Measurements != nil {
				measurements, _ := json.Marshal(cleanMeasurements(input.Measurements))
				updates = append(updates, "measurements_json = ?")
				args = append(args, string(measurements))
			}
			appendOptional(&updates, &args, "biography", input.Biography)
			appendOptional(&updates, &args, "source_url", input.SourceURL)
		}
		if sp.kind == "tags" {
			if input.ParentID != "" || input.ClearParent {
				parent := input.ParentID
				if input.ClearParent {
					parent = ""
				}
				if err := s.validateTagParent(r.Context(), entityID, parent); err != nil {
					writeError(w, r, http.StatusBadRequest, "TAG_PARENT_INVALID", err.Error())
					return
				}
				updates = append(updates, "parent_id = ?")
				args = append(args, nullable(parent))
			}
			appendOptional(&updates, &args, "source_name", input.SourceName)
			appendOptional(&updates, &args, "kind", input.TagKind)
		}
		if sp.kind == "studios" || sp.kind == "series" {
			appendOptional(&updates, &args, "source_name", input.SourceName)
		}
		if input.IsExcluded != nil {
			updates = append(updates, "is_excluded = ?")
			args = append(args, boolInt(*input.IsExcluded))
		}
		if len(updates) == 0 {
			writeError(w, r, http.StatusBadRequest, "NO_CHANGES", "没有要更新的字段")
			return
		}
		updates = append(updates, "updated_at = ?", "attributes_locked = 1")
		args = append(args, time.Now().UTC().Format(time.RFC3339Nano), entityID)
		result, execErr := s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE entities SET "+strings.Join(updates, ", ")+" WHERE entity_kind = '"+sp.kind+"' AND id = ?"), args...)
		if execErr != nil {
			writeError(w, r, http.StatusBadRequest, "RELATION_UPDATE_FAILED", "关系实体更新失败")
			return
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "关系实体不存在")
			return
		}
		s.audit(r.Context(), principal.User.ID, "update_relation", sp.kind, entityID, middleware.GetReqID(r.Context()))
		entity, _ := s.loadEntity(r.Context(), sp, entityID, principal.User)
		writeJSON(w, http.StatusOK, entity)
	}
}

func (s *Service) merge(sp spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, _ := auth.PrincipalFromContext(r.Context())
		sourceID := chi.URLParam(r, "entityID")
		var input struct {
			TargetID string `json:"targetId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.TargetID) == "" || input.TargetID == sourceID {
			writeError(w, r, http.StatusBadRequest, "INVALID_MERGE", "targetId 必须是其他实体")
			return
		}
		if err := s.mergeEntity(r.Context(), sp, sourceID, input.TargetID); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeError(w, r, status, "RELATION_MERGE_FAILED", err.Error())
			return
		}
		s.audit(r.Context(), principal.User.ID, "merge_relation", sp.kind, sourceID, middleware.GetReqID(r.Context()))
		writeJSON(w, http.StatusOK, map[string]string{"status": "merged", "sourceId": sourceID, "targetId": input.TargetID})
	}
}

type input struct {
	Name         string            `json:"name"`
	Aliases      []string          `json:"aliases"`
	SourceName   string            `json:"sourceName"`
	SourceURL    string            `json:"sourceUrl"`
	Gender       string            `json:"gender"`
	Measurements map[string]string `json:"measurements"`
	Status       string            `json:"status"`
	Birthday     string            `json:"birthday"`
	HeightCM     *int              `json:"heightCm"`
	CupSize      string            `json:"cupSize"`
	Biography    string            `json:"biography"`
	ParentID     string            `json:"parentId"`
	ClearParent  bool              `json:"clearParent"`
	TagKind      string            `json:"tagKind"`
	IsExcluded   *bool             `json:"isExcluded"`
}

func decodeInput(r *http.Request) (input, error) {
	var value input
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&value)
	return value, err
}

func (s *Service) loadEntity(ctx context.Context, sp spec, id string, user auth.User) (Entity, error) {
	query := "SELECT " + sp.table + ".id, " + sp.table + ".name, " + sp.table + ".aliases_json, " + sp.table + ".is_excluded"
	if sp.kind == "people" {
		query += ", COALESCE(people.gender, ''), COALESCE(people.status, ''), COALESCE(people.birthday, ''), people.height_cm, COALESCE(people.cup_size, ''), COALESCE(people.measurements_json, '{}'), COALESCE(people.biography, ''), COALESCE(people.source_url, '')"
	} else if sp.kind == "tags" {
		query += ", COALESCE(tags.parent_id, ''), COALESCE(tags.kind, ''), COALESCE(tags.source_name, '')"
	} else {
		query += ", COALESCE(" + sp.table + ".source_name, '')"
	}
	query += ", " + sp.table + ".original_name, " + sp.table + ".extra_attributes_json, " + sp.table + ".attributes_locked"
	query += " FROM " + sp.table + " WHERE " + sp.table + ".id = ?"
	args := []any{id}
	if !elevated(user) {
		visibility, accessArgs := auth.MediaVisibilitySQL("linked_media", user.ID, false, false)
		query = strings.Replace(query, " FROM "+sp.table+" ", " FROM "+sp.table+" JOIN "+sp.relTable+" rel ON rel."+sp.relColumn+"="+sp.table+".id JOIN media_items linked_media ON linked_media.id=rel.item_id AND ("+visibility+") ", 1)
		args = append(accessArgs, args...)
	}
	row := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...)
	entity, err := scanEntity(row, sp)
	if err != nil {
		return entity, err
	}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT namespace,external_id FROM entity_identifiers WHERE entity_kind=? AND entity_id=? ORDER BY namespace,external_id"), sp.kind, id)
	if err != nil {
		return entity, err
	}
	defer rows.Close()
	entity.ProviderIDs = map[string]string{}
	entity.SourceIDs = map[string]string{}
	for rows.Next() {
		var namespace, value string
		if err := rows.Scan(&namespace, &value); err != nil {
			return entity, err
		}
		entity.Identifiers = append(entity.Identifiers, Identifier{Namespace: namespace, Value: value})
		if strings.HasPrefix(namespace, "provider:") {
			entity.ProviderIDs[strings.TrimPrefix(namespace, "provider:")] = value
		} else {
			entity.SourceIDs[strings.TrimPrefix(namespace, "module:")] = value
		}
	}
	return entity, rows.Err()
}

func scanEntity(scanner interface{ Scan(...any) error }, sp spec) (Entity, error) {
	var entity Entity
	var excluded, locked int
	var extra string
	var aliasesJSON string
	entity.Kind = sp.kind
	args := []any{&entity.ID, &entity.Name, &aliasesJSON, &excluded}
	var gender, status, birthday, cup, measurementsJSON, biography, source, parent, tagKind string
	var height sql.NullInt64
	if sp.kind == "people" {
		args = append(args, &gender, &status, &birthday, &height, &cup, &measurementsJSON, &biography, &source)
	} else if sp.kind == "tags" {
		args = append(args, &parent, &tagKind, &source)
	} else {
		args = append(args, &source)
	}
	args = append(args, &entity.OriginalName, &extra, &locked)
	if err := scanner.Scan(args...); err != nil {
		return Entity{}, err
	}
	_ = json.Unmarshal([]byte(aliasesJSON), &entity.Aliases)
	if entity.Aliases == nil {
		entity.Aliases = []string{}
	}
	entity.AttributesLocked = locked == 1
	_ = json.Unmarshal([]byte(extra), &entity.ExtraAttributes)
	entity.IsExcluded, entity.Gender, entity.Status, entity.Birthday, entity.CupSize, entity.Biography = excluded == 1, gender, status, birthday, cup, biography
	if sp.kind == "people" {
		entity.SourceURL = source
		_ = json.Unmarshal([]byte(measurementsJSON), &entity.Measurements)
		if entity.Measurements == nil {
			entity.Measurements = map[string]string{}
		}
	} else {
		entity.SourceName = source
	}
	if height.Valid {
		value := int(height.Int64)
		entity.HeightCM = &value
	}
	entity.ParentID, entity.TagKind = parent, tagKind
	return entity, nil
}

func (s *Service) relatedItems(ctx context.Context, sp spec, entityID string, user auth.User) []RelatedItem {
	query := "SELECT DISTINCT vi.id, vi.code, vi.title FROM media_items vi JOIN " + sp.relTable + " rel ON rel.item_id = vi.id JOIN library_items li ON li.item_id = vi.id"
	args := []any{entityID}
	if !elevated(user) {
		visibility, accessArgs := auth.MediaInLibrarySQL("vi", "vl", user.ID, false, false)
		query += " JOIN virtual_libraries vl ON vl.id=li.library_id AND (" + visibility + ")"
		args = append(accessArgs, args...)
	}
	query += " WHERE rel." + sp.relColumn + " = ? AND vi.deleted_at IS NULL ORDER BY vi.code LIMIT 500"
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	items := make([]RelatedItem, 0)
	for rows.Next() {
		var item RelatedItem
		if rows.Scan(&item.ID, &item.Code, &item.Title) == nil {
			items = append(items, item)
		}
	}
	return items
}

func (s *Service) validateTagParent(ctx context.Context, tagID, parentID string) error {
	if parentID == "" {
		return nil
	}
	if tagID != "" && parentID == tagID {
		return errors.New("标签不能以自身为父级")
	}
	seen := map[string]bool{tagID: true}
	current := parentID
	for step := 0; current != "" && step < 100; step++ {
		if seen[current] {
			return errors.New("标签父级会形成循环")
		}
		seen[current] = true
		var next sql.NullString
		if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT parent_id FROM tags WHERE id = ?"), current).Scan(&next); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("父级标签不存在")
			}
			return err
		}
		current = next.String
	}
	if current != "" {
		return errors.New("标签层级过深")
	}
	return nil
}

func (s *Service) mergeEntity(ctx context.Context, sp spec, sourceID, targetID string) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sourceName, targetName string
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT name FROM "+sp.table+" WHERE id = ?"), sourceID).Scan(&sourceName); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT name FROM "+sp.table+" WHERE id = ?"), targetID).Scan(&targetName); err != nil {
		return err
	}
	_ = sourceName
	_ = targetName
	var sourceAliases, targetAliases string
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT aliases_json FROM "+sp.table+" WHERE id = ?"), sourceID).Scan(&sourceAliases); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT aliases_json FROM "+sp.table+" WHERE id = ?"), targetID).Scan(&targetAliases); err != nil {
		return err
	}
	var mergedAliases []string
	_ = json.Unmarshal([]byte(targetAliases), &mergedAliases)
	var sourceAliasValues []string
	_ = json.Unmarshal([]byte(sourceAliases), &sourceAliasValues)
	mergedAliases = cleanAliases(append(mergedAliases, sourceAliasValues...))
	mergedAliasJSON, _ := json.Marshal(mergedAliases)
	if _, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE entities SET aliases_json = ?, updated_at = ? WHERE entity_kind = '"+sp.kind+"' AND id = ?"), string(mergedAliasJSON), time.Now().UTC().Format(time.RFC3339Nano), targetID); err != nil {
		return err
	}
	query := "INSERT INTO item_entities (item_id,entity_kind,entity_id,role,sort_order) SELECT item_id,entity_kind,?,role,sort_order FROM item_entities WHERE entity_kind=? AND entity_id=? ON CONFLICT DO NOTHING"
	if _, err := tx.ExecContext(ctx, s.db.Rebind(query), targetID, sp.kind, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM item_entities WHERE entity_kind=? AND entity_id=?"), sp.kind, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE entity_identifiers SET entity_id=? WHERE entity_kind=? AND entity_id=?"), targetID, sp.kind, sourceID); err != nil {
		return err
	}

	if sp.kind == "people" {
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_person_states (user_id, person_id, is_favorite, created_at, updated_at) SELECT user_id, ?, is_favorite, created_at, updated_at FROM user_person_states WHERE person_id = ? ON CONFLICT(user_id, person_id) DO UPDATE SET is_favorite = CASE WHEN user_person_states.is_favorite = 1 OR excluded.is_favorite = 1 THEN 1 ELSE 0 END, updated_at = excluded.updated_at"), targetID, sourceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM user_person_states WHERE person_id = ?"), sourceID); err != nil {
			return err
		}
	}
	entityType := map[string]string{"people": "person", "studios": "studio", "series": "series", "tags": "tag"}[sp.kind]
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_entity_notes (user_id, entity_type, entity_id, note, created_at, updated_at) SELECT user_id, entity_type, ?, note, created_at, updated_at FROM user_entity_notes WHERE entity_type = ? AND entity_id = ? ON CONFLICT(user_id, entity_type, entity_id) DO UPDATE SET note = CASE WHEN user_entity_notes.note = excluded.note OR excluded.note = '' THEN user_entity_notes.note WHEN user_entity_notes.note = '' THEN excluded.note ELSE user_entity_notes.note || ? || excluded.note END, updated_at = excluded.updated_at"), targetID, entityType, sourceID, "\n\n"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM user_entity_notes WHERE entity_type = ? AND entity_id = ?"), entityType, sourceID); err != nil {
		return err
	}
	if sp.kind == "tags" {
		if _, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE entities SET parent_id = ? WHERE entity_kind='tags' AND parent_id = ?"), targetID, sourceID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM entities WHERE entity_kind = '"+sp.kind+"' AND id = ?"), sourceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) audit(ctx context.Context, actor, action, resource, resourceID, requestID string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, 'success', '{}', ?)"), newID("aud"), actor, action, resource, resourceID, requestID, time.Now().UTC().Format(time.RFC3339Nano))
}

func boundedPage(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
func elevated(user auth.User) bool {
	for _, role := range user.Roles {
		if role == "Admin" || role == "Editor" {
			return true
		}
	}
	return false
}
func cleanAliases(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func cleanMeasurements(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			result[key] = value
		}
	}
	return result
}
func appendOptional(updates *[]string, args *[]any, column, value string) {
	if value != "" {
		*updates = append(*updates, column+" = ?")
		*args = append(*args, value)
	}
}
func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func newID(prefix string) string { return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36) }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": middleware.GetReqID(r.Context())}})
}
