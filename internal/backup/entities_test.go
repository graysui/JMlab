package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/graysui/JMlab/internal/repository"
)

func TestEntityBackupRestoresIdentitiesRolesPrivateStateAndRollsBackConflicts(t *testing.T) {
	ctx := context.Background()
	open := func() *Service {
		t.Helper()
		db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "backup.db")), "../../migrations", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if _, err := db.DB().Exec(`INSERT INTO users (id,username,display_name,password_hash,created_at,updated_at) VALUES ('u','user','User','hash','now','now')`); err != nil {
			t.Fatal(err)
		}
		return NewService(db, nil, "unused", "", t.TempDir())
	}
	source := open()
	for _, q := range []string{
		`INSERT INTO media_items (id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('movie','AUD-001','AUD-001','Original','https://example.test','now','now')`,
		`INSERT INTO entities (entity_kind,id,name,original_name,avatar_url,extra_attributes_json,measurements_json,attributes_locked,created_at,updated_at) VALUES ('people','p1','Same name','Original name','https://example.test/avatar.png','{"country":"CN"}','{"waist":"60"}',1,'now','now'),('people','p2','Same name','','','{}','{}',0,'now','now'),('studios','p1','Studio','','','{}','{}',0,'now','now'),('tags','genre','Genre','','','{}','{}',0,'now','now')`,
		`INSERT INTO entity_identifiers VALUES ('people','p1','module:tmdb','person:1'),('people','p1','provider:tmdb','1'),('people','p2','module:tmdb','person:2')`,
		`INSERT INTO item_entities VALUES ('movie','people','p1','actor',0),('movie','people','p1','writer',1),('movie','people','p2','director',2),('movie','studios','p1','studio',3),('movie','tags','genre','genre',4)`,
		`INSERT INTO user_person_states (user_id,person_id,is_favorite,created_at,updated_at) VALUES ('u','p1',1,'now','now')`,
		`INSERT INTO user_entity_notes VALUES ('u','person','p1','个人备注','now','now')`,
		`INSERT INTO user_entity_notes VALUES ('u','person','deleted-legacy-person','旧孤立备注','now','now')`,
	} {
		if _, err := source.db.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	public, err := source.buildExport(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(public.UserPersonStates) != 0 || len(public.UserEntityNotes) != 0 {
		t.Fatal("private entity state in public export")
	}
	doc, err := source.buildExport(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.EntityData) != 4 || len(doc.EntityIdentifiers) != 3 || len(doc.EntityLinks) != 5 || len(doc.UserPersonStates) != 1 || len(doc.UserEntityNotes) != 1 {
		t.Fatal("entity export incomplete")
	}
	options := importOptions{Mode: "merge", IncludeUserState: true}
	if err := validateDocument(doc, options); err != nil {
		t.Fatal(err)
	}
	restored := open()
	if _, err := restored.applyImport(ctx, doc, options); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := restored.db.DB().QueryRow(`SELECT COUNT(*) FROM entities WHERE name='Same name'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("same-name entities collapsed on restore", err)
	}
	if err := restored.db.DB().QueryRow(`SELECT COUNT(*) FROM item_entities`).Scan(&count); err != nil || count != 5 {
		t.Fatal("typed roles lost or duplicated", err)
	}
	var original, avatar, extra, measurements string
	var locked int
	if err := restored.db.DB().QueryRow(`SELECT original_name,avatar_url,extra_attributes_json,measurements_json,attributes_locked FROM entities WHERE entity_kind='people' AND id='p1'`).Scan(&original, &avatar, &extra, &measurements, &locked); err != nil || original != "Original name" || avatar != "https://example.test/avatar.png" || locked != 1 {
		t.Fatal("entity attributes lost", err)
	}
	if !json.Valid([]byte(extra)) || !json.Valid([]byte(measurements)) {
		t.Fatal("entity JSON lost")
	}
	var note string
	if err := restored.db.DB().QueryRow(`SELECT note FROM user_entity_notes WHERE entity_type='person' AND entity_id='p1'`).Scan(&note); err != nil || note != "个人备注" {
		t.Fatal("private note lost", err)
	}
	if err := restored.db.DB().QueryRow(`SELECT is_favorite FROM user_person_states WHERE person_id='p1'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("person favorite lost", err)
	}
	// Merge keeps current user edits; overwrite applies the archive deliberately.
	if _, err := restored.db.DB().Exec(`UPDATE entities SET original_name='Changed' WHERE entity_kind='people' AND id='p1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.applyImport(ctx, doc, options); err != nil {
		t.Fatal(err)
	}
	if err := restored.db.DB().QueryRow(`SELECT original_name FROM entities WHERE entity_kind='people' AND id='p1'`).Scan(&original); err != nil || original != "Changed" {
		t.Fatal("merge overwrote current attributes", err)
	}
	options.Mode = "overwrite"
	if _, err := restored.applyImport(ctx, doc, options); err != nil {
		t.Fatal(err)
	}
	if err := restored.db.DB().QueryRow(`SELECT original_name FROM entities WHERE entity_kind='people' AND id='p1'`).Scan(&original); err != nil || original != "Original name" {
		t.Fatal("overwrite did not apply attributes", err)
	}
	doc.EntityIdentifiers[0].EntityID = "p2"
	if preview, err := restored.previewImport(ctx, doc, options); err == nil || preview.Valid {
		t.Fatal("identity conflict not caught by preview")
	}
	doc.Items[0].Title = "Must roll back"
	if _, err := restored.applyImport(ctx, doc, options); err == nil {
		t.Fatal("conflicting identity restored")
	}
	var title string
	if err := restored.db.DB().QueryRow(`SELECT title FROM media_items WHERE id='movie'`).Scan(&title); err != nil || title != "Original" {
		t.Fatal("conflicting restore partially committed", err)
	}
	if _, err := restored.clearCatalog(ctx, ""); err != nil {
		t.Fatal("catalog reset still writes compatibility views", err)
	}
	if err := restored.db.DB().QueryRow(`SELECT COUNT(*) FROM entities`).Scan(&count); err != nil || count != 0 {
		t.Fatal("catalog reset left entity data", err)
	}
}
