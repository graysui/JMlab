package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenericMigrationPreservesLegacyReferences(t *testing.T) {
	ctx := context.Background()
	prior := t.TempDir()
	files, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() > "00021_installed_modules.sql" {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../../migrations", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(prior, file.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "upgrade.db"))
	db, err := Open(ctx, dsn, prior, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO virtual_items (id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('javdb:AUD-001','AUD-001','AUD-001','Legacy','https://example.test','now','now')`,
		`INSERT INTO users (id,username,display_name,password_hash,created_at,updated_at) VALUES ('u','u','u','hash','now','now')`,
		`INSERT INTO user_item_states (user_id,item_id,is_favorite,playback_position_ticks,created_at,updated_at) VALUES ('u','javdb:AUD-001',1,123,'now','now')`,
		`INSERT INTO people (id,name,created_at,updated_at) VALUES ('p','Actor','now','now')`,
		`INSERT INTO item_people (item_id,person_id) VALUES ('javdb:AUD-001','p')`,
		`INSERT INTO item_images (id,item_id,image_type,source_url,cache_key,created_at,updated_at) VALUES ('i','javdb:AUD-001','poster','https://example.test/image','cache','now','now')`,
		`INSERT INTO playlists (id,owner_user_id,name,created_at,updated_at) VALUES ('list','u','List','now','now')`,
		`INSERT INTO playlist_items (playlist_id,item_id,position,added_by_user_id,created_at) VALUES ('list','javdb:AUD-001',0,'u','now')`,
		`INSERT INTO online_providers (id,provider_type,display_name,url_template,created_at,updated_at) VALUES ('legacy','trailer','Legacy','https://example.test','now','now')`,
		`INSERT INTO playback_sources (id,item_id,provider_id,label,protocol,candidate_url_encrypted,created_at,updated_at) VALUES ('src','javdb:AUD-001','legacy','Preview','http','encrypted','now','now')`,
		`INSERT INTO studios (id,name,created_at,updated_at) VALUES ('studio','Studio','now','now')`,
		`INSERT INTO series (id,name,created_at,updated_at) VALUES ('series','Legacy Series','now','now')`,
		`INSERT INTO tags (id,name,created_at,updated_at) VALUES ('tag','Tag','now','now')`,
		`INSERT INTO item_studios (item_id,studio_id) VALUES ('javdb:AUD-001','studio')`,
		`INSERT INTO item_series (item_id,series_id) VALUES ('javdb:AUD-001','series')`,
		`INSERT INTO item_tags (item_id,tag_id) VALUES ('javdb:AUD-001','tag')`,
		`INSERT INTO metadata_field_states (item_id,field_name,value_json,source,locked,created_at,updated_at) VALUES ('javdb:AUD-001','title','"Manual title"','manual',1,'now','now')`,
		`INSERT INTO item_rankings (item_id,ranking_type,period,rank,observed_at,is_current,created_at) VALUES ('javdb:AUD-001','ranking','daily',7,'now',1,'now')`,
		`INSERT INTO image_source_attempts (id,item_id,image_id,image_type,source_url,status,duration_ms,attempted_at) VALUES ('attempt','javdb:AUD-001','i','poster','https://example.test/image','ready',17,'now')`,
		`INSERT INTO user_image_states (user_id,image_id,is_favorite,created_at,updated_at) VALUES ('u','i',1,'now','now')`,
		`INSERT INTO user_person_states (user_id,person_id,is_favorite,created_at,updated_at) VALUES ('u','p',1,'now','now')`,
		`INSERT INTO user_entity_notes (user_id,entity_type,entity_id,note,created_at,updated_at) VALUES ('u','person','p','Private person note','now','now')`,
		`UPDATE user_item_states SET user_rating=8,note='Private media note',is_want_to_watch=1`,
		`INSERT INTO virtual_libraries (id,name,poster_image_id,is_public,enabled,created_at,updated_at) VALUES ('lib','Private library','i',0,0,'now','now')`,
		`INSERT INTO library_items (library_id,item_id,position,created_at) VALUES ('lib','javdb:AUD-001',3,'now')`,
		`INSERT INTO user_library_permissions (user_id,library_id,can_view,can_play,created_at,updated_at) VALUES ('u','lib',1,0,'now','now')`,
		`INSERT INTO role_library_permissions (role_id,library_id,can_view,can_play,created_at,updated_at) VALUES ('role-viewer','lib',1,0,'now','now')`,
		`INSERT INTO collections (id,name,poster_image_id,owner_user_id,is_public,created_at,updated_at) VALUES ('collection','Collection','i','u',1,'now','now')`,
		`INSERT INTO collection_items (collection_id,item_id,position,created_at) VALUES ('collection','javdb:AUD-001',2,'now')`,
	} {
		if _, err := db.DB().Exec(statement); err != nil {
			t.Fatalf("fixture: %s: %v", statement, err)
		}
	}
	db.Close()
	db, err = Open(ctx, dsn, "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id, kind, cipher string
	if err := db.DB().QueryRow(`SELECT mi.id,mi.item_type,ss.candidate_url_encrypted FROM media_items mi JOIN stream_sources ss ON ss.item_id=mi.id`).Scan(&id, &kind, &cipher); err != nil {
		t.Fatal(err)
	}
	if id != "javdb:AUD-001" || kind != "movie" || cipher != "encrypted" {
		t.Fatal("identity or source changed")
	}
	var favorite, position int
	if err := db.DB().QueryRow(`SELECT is_favorite,playback_position_ticks FROM user_item_states`).Scan(&favorite, &position); err != nil || favorite != 1 || position != 123 {
		t.Fatal("user state lost", err)
	}
	rows, err := db.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign keys broken by upgrade")
	}
	rows.Close()
	for _, table := range []string{"item_people", "item_studios", "item_series", "item_tags", "item_images", "playlist_items", "library_items", "collection_items", "metadata_field_states", "item_rankings", "image_source_attempts", "user_item_states"} {
		var count int
		if err := db.DB().QueryRow("SELECT COUNT(*) FROM " + table + " WHERE item_id = 'javdb:AUD-001'").Scan(&count); err != nil || count != 1 {
			t.Fatal(table, "reference lost", err)
		}
		var schema string
		if err := db.DB().QueryRow(`SELECT sql FROM sqlite_master WHERE name=?`, table).Scan(&schema); err != nil || strings.Contains(schema, "virtual_items") {
			t.Fatal(table, "foreign key not migrated", err)
		}
	}
	// Non-membership records carry user decisions and must retain exact values,
	// not merely survive an empty-table foreign-key check.
	for _, test := range []struct {
		name, query string
		want        string
	}{
		{"library choices", `SELECT CAST(enabled AS TEXT)||':'||CAST(is_public AS TEXT)||':'||poster_image_id FROM virtual_libraries WHERE id='lib'`, "0:0:i"},
		{"user permission", `SELECT CAST(can_view AS TEXT)||':'||CAST(can_play AS TEXT) FROM user_library_permissions WHERE user_id='u' AND library_id='lib'`, "1:0"},
		{"role permission", `SELECT CAST(can_view AS TEXT)||':'||CAST(can_play AS TEXT) FROM role_library_permissions WHERE role_id='role-viewer' AND library_id='lib'`, "1:0"},
		{"collection ownership", `SELECT owner_user_id||':'||CAST(is_public AS TEXT)||':'||poster_image_id FROM collections WHERE id='collection'`, "u:1:i"},
		{"personal state", `SELECT CAST(user_rating AS TEXT)||':'||CAST(is_want_to_watch AS TEXT)||':'||note FROM user_item_states WHERE user_id='u' AND item_id='javdb:AUD-001'`, "8:1:Private media note"},
		{"field lock", `SELECT CAST(locked AS TEXT)||':'||source||':'||value_json FROM metadata_field_states WHERE item_id='javdb:AUD-001' AND field_name='title'`, `1:manual:"Manual title"`},
		{"image cache", `SELECT cache_key FROM item_images WHERE id='i'`, "cache"},
		{"image favorite", `SELECT CAST(is_favorite AS TEXT) FROM user_image_states WHERE user_id='u' AND image_id='i'`, "1"},
		{"person favorite", `SELECT CAST(is_favorite AS TEXT) FROM user_person_states WHERE user_id='u' AND person_id='p'`, "1"},
		{"person note", `SELECT note FROM user_entity_notes WHERE user_id='u' AND entity_type='person' AND entity_id='p'`, "Private person note"},
		{"ranking position", `SELECT CAST(rank AS TEXT)||':'||period FROM item_rankings WHERE item_id='javdb:AUD-001'`, "7:daily"},
		{"library order", `SELECT CAST(position AS TEXT) FROM library_items WHERE item_id='javdb:AUD-001'`, "3"},
		{"collection order", `SELECT CAST(position AS TEXT) FROM collection_items WHERE item_id='javdb:AUD-001'`, "2"},
	} {
		var got string
		if err := db.DB().QueryRow(test.query).Scan(&got); err != nil || got != test.want {
			t.Fatalf("%s: got %q want %q: %v", test.name, got, test.want, err)
		}
	}
}
