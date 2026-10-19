package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/graysui/JMlab/internal/entities"
)

type ExportEntityData struct {
	Kind             string          `json:"kind"`
	ID               string          `json:"id"`
	OriginalName     string          `json:"originalName,omitempty"`
	AvatarURL        string          `json:"avatarUrl,omitempty"`
	ExtraAttributes  json.RawMessage `json:"extraAttributes,omitempty"`
	Measurements     json.RawMessage `json:"measurements,omitempty"`
	AvatarImageID    string          `json:"avatarImageId,omitempty"`
	AttributesLocked bool            `json:"attributesLocked"`
}
type ExportEntityIdentifier struct {
	Kind       string `json:"kind"`
	EntityID   string `json:"entityId"`
	Namespace  string `json:"namespace"`
	ExternalID string `json:"externalId"`
}
type ExportEntityLink struct {
	ItemID    string `json:"itemId"`
	Kind      string `json:"kind"`
	EntityID  string `json:"entityId"`
	Role      string `json:"role"`
	SortOrder int    `json:"sortOrder"`
}
type ExportUserPersonState struct {
	UserID     string `json:"userId"`
	PersonID   string `json:"personId"`
	IsFavorite bool   `json:"isFavorite"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}
type ExportUserEntityNote struct {
	UserID     string `json:"userId"`
	EntityType string `json:"entityType"`
	EntityID   string `json:"entityId"`
	Note       string `json:"note"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

func noteKind(value string) string {
	return map[string]string{"person": "people", "studio": "studios", "series": "series", "tag": "tags"}[value]
}
func typedKey(kind, id string) string { return kind + "\x00" + id }

func (s *Service) exportEntities(ctx context.Context, doc *ExportDocument, users bool) error {
	rows, err := s.db.DB().QueryContext(ctx, `SELECT entity_kind,id,original_name,avatar_url,extra_attributes_json,COALESCE(measurements_json,'{}'),COALESCE(avatar_image_id,''),attributes_locked FROM entities ORDER BY entity_kind,id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportEntityData
		var extra, measurements string
		var locked int
		if err := rows.Scan(&x.Kind, &x.ID, &x.OriginalName, &x.AvatarURL, &extra, &measurements, &x.AvatarImageID, &locked); err != nil {
			rows.Close()
			return err
		}
		x.ExtraAttributes, x.Measurements, x.AttributesLocked = json.RawMessage(extra), json.RawMessage(measurements), locked == 1
		doc.EntityData = append(doc.EntityData, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.db.DB().QueryContext(ctx, `SELECT entity_kind,entity_id,namespace,external_id FROM entity_identifiers ORDER BY entity_kind,namespace,external_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportEntityIdentifier
		if err := rows.Scan(&x.Kind, &x.EntityID, &x.Namespace, &x.ExternalID); err != nil {
			rows.Close()
			return err
		}
		doc.EntityIdentifiers = append(doc.EntityIdentifiers, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.db.DB().QueryContext(ctx, `SELECT ie.item_id,ie.entity_kind,ie.entity_id,ie.role,ie.sort_order FROM item_entities ie JOIN media_items mi ON mi.id=ie.item_id WHERE mi.deleted_at IS NULL ORDER BY ie.item_id,ie.entity_kind,ie.entity_id,ie.role`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportEntityLink
		if err := rows.Scan(&x.ItemID, &x.Kind, &x.EntityID, &x.Role, &x.SortOrder); err != nil {
			rows.Close()
			return err
		}
		doc.EntityLinks = append(doc.EntityLinks, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !users {
		return nil
	}
	rows, err = s.db.DB().QueryContext(ctx, `SELECT user_id,person_id,is_favorite,created_at,updated_at FROM user_person_states ORDER BY user_id,person_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportUserPersonState
		var favorite int
		if err := rows.Scan(&x.UserID, &x.PersonID, &favorite, &x.CreatedAt, &x.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		x.IsFavorite = favorite == 1
		doc.UserPersonStates = append(doc.UserPersonStates, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.db.DB().QueryContext(ctx, `SELECT n.user_id,n.entity_type,n.entity_id,n.note,n.created_at,n.updated_at FROM user_entity_notes n JOIN entities e ON e.id=n.entity_id AND e.entity_kind=CASE n.entity_type WHEN 'person' THEN 'people' WHEN 'studio' THEN 'studios' WHEN 'series' THEN 'series' WHEN 'tag' THEN 'tags' END ORDER BY n.user_id,n.entity_type,n.entity_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x ExportUserEntityNote
		if err := rows.Scan(&x.UserID, &x.EntityType, &x.EntityID, &x.Note, &x.CreatedAt, &x.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		doc.UserEntityNotes = append(doc.UserEntityNotes, x)
	}
	err = rows.Err()
	rows.Close()
	return err
}

func validateEntityData(doc ExportDocument, items map[string]bool, kinds map[string]map[string]bool, options importOptions) error {
	if len(doc.EntityData) > 400000 || len(doc.EntityIdentifiers) > 1000000 || len(doc.EntityLinks) > 1000000 || len(doc.UserPersonStates) > 200000 || len(doc.UserEntityNotes) > 200000 {
		return errors.New("实体数据量超过限制")
	}
	valid := func(kind, id string) bool { return kinds[kind] != nil && kinds[kind][id] }
	seen := map[string]bool{}
	for _, x := range doc.EntityData {
		key := typedKey(x.Kind, x.ID)
		if !valid(x.Kind, x.ID) || seen[key] || hasSensitiveText(x.AvatarURL) || (len(x.ExtraAttributes) > 0 && (!json.Valid(x.ExtraAttributes) || len(x.ExtraAttributes) > 256<<10)) || (len(x.Measurements) > 0 && !json.Valid(x.Measurements)) {
			return errors.New("实体属性或引用无效")
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, x := range doc.EntityIdentifiers {
		key := typedKey(x.Kind, x.Namespace) + "\x00" + x.ExternalID
		if !valid(x.Kind, x.EntityID) || strings.TrimSpace(x.Namespace) == "" || len(x.Namespace) > 128 || strings.TrimSpace(x.ExternalID) == "" || len(x.ExternalID) > 2048 || seen[key] {
			return errors.New("实体来源标识重复或无效")
		}
		seen[key] = true
	}
	for _, x := range doc.EntityLinks {
		if !items[x.ItemID] || !valid(x.Kind, x.EntityID) || !validEntityRole(x.Kind, x.Role) {
			return errors.New("实体关系或角色无效")
		}
	}
	if !options.IncludeUserState {
		return nil
	}
	users := map[string]bool{}
	for _, x := range doc.Users {
		users[x.ID] = true
	}
	for _, x := range doc.UserPersonStates {
		if !users[x.UserID] || !valid("people", x.PersonID) {
			return errors.New("人物收藏引用无效")
		}
	}
	for _, x := range doc.UserEntityNotes {
		if !users[x.UserID] || !valid(noteKind(x.EntityType), x.EntityID) || len(x.Note) > 16384 {
			return errors.New("实体备注引用无效")
		}
	}
	return nil
}
func validEntityRole(kind, role string) bool {
	switch kind {
	case "people":
		return role == "actor" || role == "director" || role == "writer" || role == "other"
	case "studios":
		return role == "studio"
	case "series":
		return role == "series"
	case "tags":
		return role == "genre" || role == "tag"
	}
	return false
}
func entitySnapshot(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT entity_kind,id FROM entities`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			return nil, err
		}
		found[typedKey(kind, id)] = true
	}
	return found, rows.Err()
}
func entityMapping(m importMapping, kind string) map[string]string {
	return map[string]map[string]string{"people": m.people, "studios": m.studios, "series": m.series, "tags": m.tags}[kind]
}

func (s *Service) validateEntityIdentityConflicts(ctx context.Context, doc ExportDocument) error {
	for _, x := range doc.EntityIdentifiers {
		var id string
		err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(`SELECT entity_id FROM entity_identifiers WHERE entity_kind=? AND namespace=? AND external_id=?`), x.Kind, x.Namespace, x.ExternalID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if id != x.EntityID {
			return errors.New("实体来源标识与现有数据冲突，请先合并对应实体")
		}
	}
	return nil
}
func (s *Service) importEntityData(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions, m importMapping, existed map[string]bool) error {
	for _, x := range doc.EntityData {
		id := entityMapping(m, x.Kind)[x.ID]
		if id == "" {
			return errors.New("实体属性引用不存在")
		}
		if options.Mode == "merge" && existed[typedKey(x.Kind, id)] {
			continue
		}
		_, err := tx.ExecContext(ctx, s.db.Rebind(`UPDATE entities SET original_name=?,avatar_url=?,extra_attributes_json=?,measurements_json=?,avatar_image_id=?,attributes_locked=? WHERE entity_kind=? AND id=?`), x.OriginalName, x.AvatarURL, string(ensureJSON(x.ExtraAttributes, "{}")), string(ensureJSON(x.Measurements, "{}")), nullable(x.AvatarImageID), boolInt(x.AttributesLocked), x.Kind, id)
		if err != nil {
			return err
		}
	}
	for _, x := range doc.EntityIdentifiers {
		id := entityMapping(m, x.Kind)[x.EntityID]
		if id == "" {
			return errors.New("实体标识引用不存在")
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind(`INSERT INTO entity_identifiers (entity_kind,entity_id,namespace,external_id) VALUES (?,?,?,?) ON CONFLICT(entity_kind,namespace,external_id) DO NOTHING`), x.Kind, id, x.Namespace, x.ExternalID); err != nil {
			return err
		}
		var actual string
		if err := tx.QueryRowContext(ctx, s.db.Rebind(`SELECT entity_id FROM entity_identifiers WHERE entity_kind=? AND namespace=? AND external_id=?`), x.Kind, x.Namespace, x.ExternalID).Scan(&actual); err != nil {
			return err
		}
		if actual != id {
			return errors.New("实体来源标识与当前数据冲突，恢复已回滚")
		}
	}
	return nil
}
func (s *Service) importEntityLinks(ctx context.Context, tx *sql.Tx, doc ExportDocument, m importMapping) error {
	for _, x := range doc.EntityLinks {
		if err := entities.Link(ctx, tx, s.db, m.items[x.ItemID], x.Kind, entityMapping(m, x.Kind)[x.EntityID], x.Role, x.SortOrder); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) importEntityUserState(ctx context.Context, tx *sql.Tx, doc ExportDocument, options importOptions, m importMapping) error {
	for _, x := range doc.UserPersonStates {
		user, id := m.users[x.UserID], m.people[x.PersonID]
		if user == "" || id == "" {
			continue
		}
		query := `INSERT INTO user_person_states (user_id,person_id,is_favorite,created_at,updated_at) VALUES (?,?,?,?,?) ON CONFLICT(user_id,person_id) DO NOTHING`
		if options.Mode == "overwrite" {
			query = strings.Replace(query, "DO NOTHING", "DO UPDATE SET is_favorite=excluded.is_favorite,updated_at=excluded.updated_at", 1)
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind(query), user, id, boolInt(x.IsFavorite), timestamp(x.CreatedAt), timestamp(x.UpdatedAt)); err != nil {
			return err
		}
	}
	for _, x := range doc.UserEntityNotes {
		user, id := m.users[x.UserID], entityMapping(m, noteKind(x.EntityType))[x.EntityID]
		if user == "" || id == "" {
			continue
		}
		query := `INSERT INTO user_entity_notes (user_id,entity_type,entity_id,note,created_at,updated_at) VALUES (?,?,?,?,?,?) ON CONFLICT(user_id,entity_type,entity_id) DO NOTHING`
		if options.Mode == "overwrite" {
			query = strings.Replace(query, "DO NOTHING", "DO UPDATE SET note=excluded.note,updated_at=excluded.updated_at", 1)
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind(query), user, x.EntityType, id, x.Note, timestamp(x.CreatedAt), timestamp(x.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}
