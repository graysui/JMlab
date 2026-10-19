// Package entities owns typed entity identities and media relationships.
package entities

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/graysui/JMlab/internal/repository"
)

type Identity struct{ Namespace, Value string }
type Input struct {
	Kind, Name, TagKind, OriginalName, AvatarURL string
	Identities                                   []Identity
	Extra                                        map[string]string
}

func ValidKind(kind string) bool {
	return kind == "people" || kind == "studios" || kind == "series" || kind == "tags"
}

// Ensure resolves external identities before names. Names only adopt a unique,
// unclaimed legacy entity; different source IDs with the same name stay separate.
func Ensure(ctx context.Context, tx *sql.Tx, db *repository.Database, input Input) (string, error) {
	input.Name = strings.TrimSpace(input.Name)
	extra, err := json.Marshal(input.Extra)
	if !ValidKind(input.Kind) || input.Name == "" || len(input.Name) > 2048 || len(input.Identities) > 33 || err != nil || len(extra) > 256<<10 {
		return "", errors.New("实体信息无效或过大")
	}
	if string(extra) == "null" {
		extra = []byte("{}")
	}
	keys := make([]Identity, 0, len(input.Identities))
	seen := map[Identity]bool{}
	for _, key := range input.Identities {
		key.Namespace, key.Value = strings.TrimSpace(key.Namespace), strings.TrimSpace(key.Value)
		if key.Namespace == "" || key.Value == "" || len(key.Namespace) > 128 || len(key.Value) > 2048 {
			return "", errors.New("实体来源标识无效")
		}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Namespace == keys[j].Namespace {
			return keys[i].Value < keys[j].Value
		}
		return keys[i].Namespace < keys[j].Namespace
	})
	id := ""
	for _, key := range keys {
		var found string
		err := tx.QueryRowContext(ctx, db.Rebind(`SELECT entity_id FROM entity_identifiers WHERE entity_kind=? AND namespace=? AND external_id=?`), input.Kind, key.Namespace, key.Value).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		if id != "" && id != found {
			return "", errors.New("来源提供的实体标识相互冲突，请先核对或合并实体")
		}
		id = found
	}
	if id == "" {
		query := `SELECT e.id FROM entities e WHERE e.entity_kind=? AND LOWER(e.name)=LOWER(?)`
		if len(keys) > 0 {
			query += ` AND NOT EXISTS (SELECT 1 FROM entity_identifiers i WHERE i.entity_kind=e.entity_kind AND i.entity_id=e.id)`
		}
		rows, err := tx.QueryContext(ctx, db.Rebind(query+` ORDER BY e.id LIMIT 2`), input.Kind, input.Name)
		if err != nil {
			return "", err
		}
		var matches []string
		for rows.Next() {
			var found string
			if err := rows.Scan(&found); err != nil {
				rows.Close()
				return "", err
			}
			matches = append(matches, found)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		if len(matches) == 1 {
			id = matches[0]
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if id == "" {
		prefix := map[string]string{"people": "person_", "studios": "studio_", "series": "series_", "tags": "tag_"}[input.Kind]
		id = prefix + rand.Text()
		if len(keys) > 0 {
			digest := sha256.Sum256([]byte(input.Kind + "\x00" + keys[0].Namespace + "\x00" + keys[0].Value))
			id = prefix + hex.EncodeToString(digest[:16])
		}
		tagKind := input.TagKind
		if tagKind == "" {
			tagKind = "custom"
		}
		_, err := tx.ExecContext(ctx, db.Rebind(`INSERT INTO entities (entity_kind,id,name,kind,original_name,avatar_url,extra_attributes_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_kind,id) DO NOTHING`), input.Kind, id, input.Name, tagKind, input.OriginalName, input.AvatarURL, string(extra), now, now)
		if err != nil {
			return "", err
		}
	}
	// Source sync cannot overwrite fields explicitly edited in the manager.
	if len(keys) > 0 {
		_, err := tx.ExecContext(ctx, db.Rebind(`UPDATE entities SET name=?, original_name=CASE WHEN ? <> '' THEN ? ELSE original_name END, avatar_url=CASE WHEN ? <> '' THEN ? ELSE avatar_url END, extra_attributes_json=CASE WHEN ? <> '{}' THEN ? ELSE extra_attributes_json END, updated_at=? WHERE entity_kind=? AND id=? AND attributes_locked=0`), input.Name, input.OriginalName, input.OriginalName, input.AvatarURL, input.AvatarURL, string(extra), string(extra), now, input.Kind, id)
		if err != nil {
			return "", err
		}
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, db.Rebind(`INSERT INTO entity_identifiers (entity_kind,entity_id,namespace,external_id) VALUES (?,?,?,?) ON CONFLICT(entity_kind,namespace,external_id) DO NOTHING`), input.Kind, id, key.Namespace, key.Value); err != nil {
			return "", err
		}
		var actual string
		if err := tx.QueryRowContext(ctx, db.Rebind(`SELECT entity_id FROM entity_identifiers WHERE entity_kind=? AND namespace=? AND external_id=?`), input.Kind, key.Namespace, key.Value).Scan(&actual); err != nil {
			return "", err
		}
		if actual != id {
			return "", errors.New("实体来源标识已经关联其他实体")
		}
	}
	return id, nil
}

func Link(ctx context.Context, tx *sql.Tx, db *repository.Database, itemID, kind, entityID, role string, order int) error {
	_, err := tx.ExecContext(ctx, db.Rebind(`INSERT INTO item_entities (item_id,entity_kind,entity_id,role,sort_order) VALUES (?,?,?,?,?) ON CONFLICT(item_id,entity_kind,entity_id,role) DO UPDATE SET sort_order=excluded.sort_order`), itemID, kind, entityID, role, order)
	return err
}
