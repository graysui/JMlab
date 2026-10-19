package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/graysui/JMlab/internal/repository"
)

func TestDeleteUserCleansReferencesAndRollsBackFailure(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "users.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string) {
		t.Helper()
		if _, err := db.DB().Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO users(id,username,display_name,password_hash,created_at,updated_at) VALUES ('a','a','A','hash','now','now'),('b','b','B','hash','now','now')`,
		`INSERT INTO media_items(id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('m','m','m','Movie','https://example.test','now','now')`,
		`INSERT INTO sessions(id,token_hash,user_id,expires_at,created_at) VALUES ('session','test','a','later','now')`,
		`INSERT INTO user_preferences(user_id,created_at,updated_at) VALUES ('a','now','now')`,
		`INSERT INTO user_roles(user_id,role_id,created_at) VALUES ('a','role-viewer','now')`,
		`INSERT INTO user_item_states(user_id,item_id,created_at,updated_at) VALUES ('a','m','now','now'),('b','m','now','now')`,
		`INSERT INTO user_item_state_events(id,user_id,item_id,session_id,event_type,created_at) VALUES ('event','a','m','session','progress','now')`,
		`INSERT INTO playlists(id,owner_user_id,name,created_at,updated_at) VALUES ('owned','a','A list','now','now'),('shared','b','B list','now','now')`,
		`INSERT INTO playlist_items(playlist_id,item_id,position,added_by_user_id,created_at) VALUES ('owned','m',0,'b','now'),('shared','m',0,'a','now')`,
		`INSERT INTO collections(id,name,owner_user_id,created_at,updated_at) VALUES ('collection','Collection','a','now','now')`,
		`INSERT INTO audit_logs(id,actor_user_id,action,resource_type,result,created_at) VALUES ('audit','a','test','user','success','now')`,
		`INSERT INTO tasks(id,type,status,created_at,updated_at) VALUES ('task','backup','succeeded','now','now')`,
		`INSERT INTO backup_runs(id,task_id,requested_by,created_at) VALUES ('backup','task','a','now')`,
	} {
		exec(query)
	}
	// Simulate a failure after playlist cleanup; no earlier delete may persist.
	exec(`CREATE TRIGGER fail_user_delete BEFORE DELETE ON users BEGIN SELECT RAISE(ABORT, 'test failure'); END`)
	store := NewStore(db)
	if err := store.DeleteUser(ctx, "a"); err == nil {
		t.Fatal("delete failure ignored")
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM playlist_items`).Scan(&count); err != nil || count != 2 {
		t.Fatal("playlist cleanup not rolled back", count, err)
	}
	exec(`DROP TRIGGER fail_user_delete`)
	if err := store.DeleteUser(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM users WHERE id='a'`,
		`SELECT COUNT(*) FROM sessions WHERE user_id='a'`,
		`SELECT COUNT(*) FROM user_preferences WHERE user_id='a'`,
		`SELECT COUNT(*) FROM user_roles WHERE user_id='a'`,
		`SELECT COUNT(*) FROM user_item_states WHERE user_id='a'`,
		`SELECT COUNT(*) FROM user_item_state_events WHERE user_id='a'`,
		`SELECT COUNT(*) FROM playlists WHERE owner_user_id='a'`,
		`SELECT COUNT(*) FROM playlist_items`,
		`SELECT COUNT(*) FROM collections WHERE owner_user_id IS NOT NULL`,
		`SELECT COUNT(*) FROM audit_logs WHERE actor_user_id IS NOT NULL`,
		`SELECT COUNT(*) FROM backup_runs WHERE requested_by IS NOT NULL`,
	} {
		if err := db.DB().QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal(query, count, err)
		}
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM users WHERE id='b'`,
		`SELECT COUNT(*) FROM user_item_states WHERE user_id='b'`,
		`SELECT COUNT(*) FROM playlists WHERE id='shared'`,
		`SELECT COUNT(*) FROM media_items`,
		`SELECT COUNT(*) FROM collections`,
		`SELECT COUNT(*) FROM audit_logs`,
		`SELECT COUNT(*) FROM backup_runs`,
	} {
		if err := db.DB().QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatal("unrelated data lost", query, count, err)
		}
	}
	if err := store.DeleteUser(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing user", err)
	}
}
