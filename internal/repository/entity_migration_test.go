package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func querySnapshot(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var data [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		data = append(data, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestUnifiedEntityMigrationPreservesTypedIDsAndEveryLegacyField(t *testing.T) {
	ctx := context.Background()
	prior := migrationsThrough(t, "00025_module_stream_identity.sql")
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "entities.db"))
	db, err := Open(ctx, dsn, prior, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO media_items (id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('movie','code','code','Movie','https://example.test','created','updated')`,
		`INSERT INTO users (id,username,display_name,password_hash,created_at,updated_at) VALUES ('user','user','User','hash','created','updated')`,
		`INSERT INTO people (id,name,aliases_json,gender,status,birthday,height_cm,cup_size,measurements_json,biography,avatar_image_id,source_url,is_excluded,created_at,updated_at) VALUES ('shared','Person','["Alias"]','female','retired','2000-01-01',168,'B','{"waist":"60"}','Biography','avatar','https://example.test/person',1,'created','updated')`,
		`INSERT INTO studios (id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at) VALUES ('shared','Studio','source','["Other"]',1,4,'created','updated')`,
		`INSERT INTO series (id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at) VALUES ('shared','Legacy series','source','["Series alias"]',0,5,'created','updated')`,
		`INSERT INTO tags (id,name,source_name,aliases_json,kind,parent_id,is_excluded,sort_order,created_at,updated_at) VALUES ('root','Parent','source','[]','custom',NULL,0,1,'created','updated'),('shared','Child','source','["Child alias"]','genre','root',1,6,'created','updated')`,
		`INSERT INTO item_people (item_id,person_id,role,sort_order) VALUES ('movie','shared','actor',1),('movie','shared','director',2)`,
		`INSERT INTO item_studios VALUES ('movie','shared',3)`,
		`INSERT INTO item_series VALUES ('movie','shared',4)`,
		`INSERT INTO item_tags VALUES ('movie','shared')`,
		`INSERT INTO user_person_states VALUES ('user','shared',1,'created','updated')`,
		`INSERT INTO user_entity_notes VALUES ('user','person','shared','私人备注','created','updated'),('user','studio','shared','片商备注','created','updated')`,
	} {
		if _, err := db.DB().Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	queries := []string{
		`SELECT id,name,aliases_json,gender,status,birthday,height_cm,cup_size,measurements_json,biography,avatar_image_id,source_url,is_excluded,created_at,updated_at FROM people ORDER BY id`,
		`SELECT id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at FROM studios ORDER BY id`,
		`SELECT id,name,source_name,aliases_json,is_excluded,sort_order,created_at,updated_at FROM series ORDER BY id`,
		`SELECT id,name,source_name,aliases_json,kind,parent_id,is_excluded,sort_order,created_at,updated_at FROM tags ORDER BY id`,
		`SELECT item_id,person_id,role,sort_order FROM item_people ORDER BY role`,
		`SELECT item_id,studio_id,sort_order FROM item_studios`,
		`SELECT item_id,series_id,sort_order FROM item_series`,
		`SELECT item_id,tag_id FROM item_tags`,
		`SELECT user_id,person_id,is_favorite,created_at,updated_at FROM user_person_states`,
		`SELECT user_id,entity_type,entity_id,note,created_at,updated_at FROM user_entity_notes ORDER BY entity_type`,
	}
	snapshots := make([]string, len(queries))
	for i, q := range queries {
		snapshots[i] = querySnapshot(t, db.DB(), q)
	}
	db.Close()
	db, err = Open(ctx, dsn, "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, q := range queries {
		if after := querySnapshot(t, db.DB(), q); after != snapshots[i] {
			t.Fatalf("migration changed query %s\nbefore %s\nafter %s", q, snapshots[i], after)
		}
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM entities WHERE id='shared'`).Scan(&count); err != nil || count != 4 {
		t.Fatal("IDs from different kinds collided", err)
	}
	rows, err := db.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key broken")
	}
	rows.Close()
	if _, err := db.DB().Exec(`INSERT INTO item_entities VALUES ('movie','people','shared','writer',7)`); err != nil {
		t.Fatal("writer role unavailable", err)
	}
	if _, err := db.DB().Exec(`DELETE FROM media_items WHERE id='movie'`); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM item_entities`).Scan(&count); err != nil || count != 0 {
		t.Fatal("media relationship cascade lost", err)
	}
	if _, err := db.DB().Exec(`DELETE FROM entities WHERE entity_kind='people' AND id='shared'`); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM user_person_states`).Scan(&count); err != nil || count != 0 {
		t.Fatal("person favorite cascade lost", err)
	}
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM entities WHERE id='shared'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("deleting person affected another entity kind", err)
	}
}

func TestUnifiedEntityMigrationFailureRollsBackLegacyTables(t *testing.T) {
	ctx := context.Background()
	prior := migrationsThrough(t, "00025_module_stream_identity.sql")
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "rollback.db"))
	db, err := Open(ctx, dsn, prior, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`INSERT INTO tags(id,name,kind,parent_id,created_at,updated_at) VALUES ('cycle','Legacy cycle','custom','cycle','now','now')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if upgraded, err := Open(ctx, dsn, "../../migrations", nil); err == nil {
		upgraded.Close()
		t.Fatal("invalid legacy hierarchy accepted")
	}
	db, err = Open(ctx, dsn, prior, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var parent, kind string
	if err := db.DB().QueryRow(`SELECT parent_id FROM tags WHERE id='cycle'`).Scan(&parent); err != nil || parent != "cycle" {
		t.Fatal("legacy data lost", parent, err)
	}
	if err := db.DB().QueryRow(`SELECT type FROM sqlite_master WHERE name='tags'`).Scan(&kind); err != nil || kind != "table" {
		t.Fatal(kind, err)
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('entities','entity_identifiers','item_entities')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial migration persisted", count, err)
	}
}
