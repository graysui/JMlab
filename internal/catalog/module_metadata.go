package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/entities"
	"github.com/graysui/JMlab/internal/metadata"
	"github.com/graysui/JMlab/internal/module"
)

func (s *Service) SetModuleRegistry(registry *module.Registry) { s.modules = registry }

func (s *Service) registerMetadataRoutes(router chi.Router) {
	read := router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"))
	read.Get("/metadata/modules", s.metadataModules)
	read.Get("/metadata/{moduleID}/search", s.searchMetadata)
	read.With(s.authService.RequireCSRF).Post("/metadata/{moduleID}/import", s.importMetadata)
}

func (s *Service) metadataModule(id string) (*module.ModuleEntry, error) {
	if s.modules == nil {
		return nil, errors.New("元数据模块尚未配置")
	}
	entry, ok := s.modules.Get(id)
	if !ok || entry.Status != module.StatusActive || entry.MetadataInstance == nil {
		return nil, errors.New("元数据来源已停用或不可用")
	}
	return entry, nil
}

func (s *Service) metadataModules(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	if s.modules != nil {
		for _, entry := range s.modules.List() {
			if entry.Status == module.StatusActive && entry.MetadataInstance != nil && (entry.Capabilities == nil || entry.Capabilities.SupportsSearch) {
				items = append(items, map[string]any{"id": entry.Manifest.ID, "name": entry.Manifest.Name, "supportedItemTypes": entry.SupportedTypes()})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) searchMetadata(w http.ResponseWriter, r *http.Request) {
	entry, err := s.metadataModule(chi.URLParam(r, "moduleID"))
	if err != nil {
		writeError(w, r, 404, "METADATA_UNAVAILABLE", err.Error())
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if entry.Capabilities != nil && !entry.Capabilities.SupportsSearch {
		writeError(w, r, 400, "SEARCH_UNSUPPORTED", "这个来源不支持搜索，请选择其他来源")
		return
	}
	kind := module.ItemType(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("itemType"))))
	if kind == "" {
		kind = module.ItemMovie
	}
	if query == "" || len(query) > 200 || !supportsItemType(entry, kind) {
		writeError(w, r, 400, "INVALID_SEARCH", "请输入搜索内容并选择来源支持的媒体类型")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 500 {
		page = 500
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items, total, err := entry.MetadataInstance.Search(ctx, query, kind, page, 20)
	if err != nil {
		writeError(w, r, 502, "METADATA_SEARCH_FAILED", "搜索来源暂时不可用，请稍后重试")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page})
}

func supportsItemType(entry *module.ModuleEntry, kind module.ItemType) bool {
	for _, supported := range entry.SupportedTypes() {
		if kind == supported {
			return true
		}
	}
	return false
}

func (s *Service) importMetadata(w http.ResponseWriter, r *http.Request) {
	entry, err := s.metadataModule(chi.URLParam(r, "moduleID"))
	if err != nil {
		writeError(w, r, 404, "METADATA_UNAVAILABLE", err.Error())
		return
	}
	var input struct {
		SourceID    string            `json:"sourceId"`
		ProviderIDs map[string]string `json:"providerIds"`
		ParentID    string            `json:"parentId"`
		IndexNumber *int              `json:"indexNumber"`
	}
	if decodeJSON(r, &input) != nil || strings.TrimSpace(input.SourceID) == "" || len(input.SourceID) > 256 {
		writeError(w, r, 400, "INVALID_IMPORT", "请选择要添加的作品")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	detail, err := entry.MetadataInstance.GetDetail(ctx, input.SourceID, input.ProviderIDs)
	if err != nil {
		writeError(w, r, 502, "METADATA_DETAIL_FAILED", "暂时无法读取作品详情，请稍后重试")
		return
	}
	if detail.SourceID != input.SourceID || !supportsItemType(entry, detail.ItemType) {
		writeError(w, r, 502, "INVALID_METADATA", "来源返回的媒体标识或类型无效")
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	item, created, err := s.saveModuleMedia(ctx, entry.Manifest.ID, detail, input.ParentID, input.IndexNumber)
	if err != nil {
		writeError(w, r, 400, "MEDIA_SAVE_FAILED", err.Error())
		return
	}
	s.audit(ctx, principal.User.ID, "import_module_media", "media_item", item.ID, "", "{}")
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"item": item, "created": created})
}

// saveModuleMedia keeps source identity separate from optional catalogue codes.
func (s *Service) saveModuleMedia(ctx context.Context, moduleID string, detail module.MediaDetail, parentID string, index *int) (Item, bool, error) {
	if strings.TrimSpace(detail.Title) == "" || detail.SourceID == "" {
		return Item{}, false, errors.New("媒体标题和来源标识不能为空")
	}
	kind := string(detail.ItemType)
	if err := validateHierarchy(kind, parentID, index); err != nil {
		return Item{}, false, err
	}
	if parentID != "" {
		parent, err := s.getByID(ctx, parentID, "", true)
		if err != nil {
			return Item{}, false, errors.New("请先添加正确的父级媒体")
		}
		if detail.ExtraAttributes == nil {
			detail.ExtraAttributes = map[string]any{}
		}
		if parent.ItemType == "series" {
			detail.ExtraAttributes["seriesTitle"] = parent.Title
		} else {
			detail.ExtraAttributes["seriesTitle"] = parent.ExtraAttributes["seriesTitle"]
			if parent.IndexNumber != nil {
				detail.ExtraAttributes["seasonNumber"] = *parent.IndexNumber
			}
		}
	}
	digest := sha256.Sum256([]byte(moduleID + "\x00" + detail.SourceID))
	id := "media_" + hex.EncodeToString(digest[:16])
	var existingID string
	lookupErr := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id FROM media_items WHERE metadata_module_id=? AND source_id=?"), moduleID, detail.SourceID).Scan(&existingID)
	if lookupErr == nil {
		id = existingID
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return Item{}, false, lookupErr
	}

	ids, _ := json.Marshal(detail.ProviderIDs)
	extra, err := json.Marshal(detail.ExtraAttributes)
	if err != nil || len(extra) > 256<<10 || len(ids) > 16<<10 {
		return Item{}, false, errors.New("来源扩展信息无效或过大")
	}
	if string(ids) == "null" {
		ids = []byte("{}")
	}
	if string(extra) == "null" {
		extra = []byte("{}")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return Item{}, false, err
	}
	defer tx.Rollback()
	if parentID != "" {
		var parentType string
		if tx.QueryRowContext(ctx, s.db.Rebind("SELECT item_type FROM media_items WHERE id = ? AND deleted_at IS NULL"), parentID).Scan(&parentType) != nil || !validParentType(kind, parentType) {
			return Item{}, false, errors.New("请先添加正确的剧集或季")
		}
	}
	var existingType string
	err = tx.QueryRowContext(ctx, s.db.Rebind("SELECT item_type FROM media_items WHERE id = ?"), id).Scan(&existingType)
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return Item{}, false, err
	}
	if !created && existingType != kind {
		return Item{}, false, errors.New("来源不能改变已有媒体的类型")
	}
	if created {
		_, err = tx.ExecContext(ctx, s.db.Rebind(`INSERT INTO media_items (id,source,source_id,code,raw_code,title,source_url,is_adult,item_type,parent_id,index_number,provider_ids_json,extra_attributes_json,metadata_module_id,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`), id, moduleID, detail.SourceID, "media:"+id, "", detail.Title, detail.SourceURL, boolInt(detail.IsAdult), kind, nullable(parentID), index, string(ids), string(extra), moduleID, now, now)
		if err != nil {
			return Item{}, false, err
		}
	}
	fields := []struct {
		name  string
		value any
	}{{"title", detail.Title}, {"original_title", nullable(detail.OriginalTitle)}, {"overview", nullable(detail.Overview)}, {"release_date", nullable(detail.ReleaseDate)}, {"year", detail.ProductionYear}, {"runtime_seconds", detail.RuntimeSeconds}, {"source_rating", detail.Rating}, {"rating_count", detail.RatingCount}, {"source_url", detail.SourceURL}, {"is_adult", boolInt(detail.IsAdult)}, {"provider_ids_json", string(ids)}, {"extra_attributes_json", string(extra)}}
	for _, field := range fields {
		_, err = tx.ExecContext(ctx, s.db.Rebind("UPDATE media_items SET "+field.name+" = ? WHERE id = ? AND NOT EXISTS (SELECT 1 FROM metadata_field_states WHERE item_id = ? AND field_name = ? AND locked = 1)"), field.value, id, id, field.name)
		if err != nil {
			return Item{}, false, err
		}
	}
	_, err = tx.ExecContext(ctx, s.db.Rebind("UPDATE media_items SET metadata_status='complete',last_synced_at=?,updated_at=?,deleted_at=NULL WHERE id=?"), now, now, id)
	if err != nil {
		return Item{}, false, err
	}
	for order, relation := range detail.Entities {
		if strings.TrimSpace(relation.Name) == "" {
			continue
		}
		kind, tagKind := "", "custom"
		switch relation.Role {
		case module.RoleActor, module.RoleDirector, module.RoleWriter:
			kind = "people"
		case module.RoleStudio:
			kind = "studios"
		case module.RoleGenre:
			kind, tagKind = "tags", "genre"
		case module.RoleTag:
			kind = "tags"
		default:
			return Item{}, false, errors.New("来源提供了不支持的实体角色")
		}
		keys := []entities.Identity{}
		if relation.SourceID != "" {
			keys = append(keys, entities.Identity{Namespace: "module:" + moduleID, Value: relation.SourceID})
		}
		for namespace, value := range relation.ProviderIDs {
			keys = append(keys, entities.Identity{Namespace: "provider:" + namespace, Value: value})
		}
		entityID, entityErr := entities.Ensure(ctx, tx, s.db, entities.Input{Kind: kind, Name: relation.Name, TagKind: tagKind, OriginalName: relation.OriginalName, AvatarURL: relation.AvatarURL, Extra: relation.Extra, Identities: keys})
		if entityErr != nil {
			return Item{}, false, entityErr
		}
		if err := entities.Link(ctx, tx, s.db, id, kind, entityID, string(relation.Role), order); err != nil {
			return Item{}, false, err
		}
	}

	_, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO library_items (library_id,item_id,position,created_at) SELECT id,?,0,? FROM virtual_libraries WHERE id='library-default' ON CONFLICT(library_id,item_id) DO NOTHING"), id, now)
	if err != nil {
		return Item{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Item{}, false, err
	}
	if err := s.SyncLibraryMemberships(ctx, "", id); err != nil {
		return Item{}, false, err
	}
	s.emitMedia(ctx, id, created)
	images := []metadata.SourceImage{}
	for _, image := range detail.Images {
		images = append(images, metadata.SourceImage{Type: image.Type, URL: image.URL, Label: image.Label, Priority: image.Priority})
	}
	s.importSourceImages(ctx, id, images)
	item, err := s.getByID(ctx, id, "", true)
	return item, created, err
}

func validateHierarchy(kind, parent string, index *int) error {
	switch kind {
	case "movie", "video", "series":
		if parent != "" {
			return errors.New("电影、视频或剧集不能设置父级")
		}
	case "season", "episode":
		if parent == "" || index == nil || *index < 0 {
			return errors.New("季或单集需要父级及有效序号")
		}
	default:
		return errors.New("不支持的媒体类型")
	}
	return nil
}
func validParentType(kind, parent string) bool {
	return kind == "season" && parent == "series" || kind == "episode" && parent == "season"
}
