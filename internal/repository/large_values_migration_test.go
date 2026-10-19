package repository

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLargeValuesMigrationPreservesRowsAndWidensColumns(t *testing.T) {
	ctx := context.Background()
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "large-values.db"))
	db, err := Open(ctx, dsn, migrationsThrough(t, "00026_unified_entities.sql"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO users(id,username,display_name,password_hash,created_at,updated_at) VALUES ('u','u','User','hash','now','now')`,
		`INSERT INTO media_items(id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('m','m','m','Movie','https://example.test','now','now')`,
		`INSERT INTO online_providers(id,provider_type,display_name,url_template,probe_duration_ms_total,created_at,updated_at) VALUES ('p','trailer','Provider','https://example.test',3000000000,'now','now')`,
		`INSERT INTO stream_sources(id,item_id,label,protocol,size_bytes,candidate_url_encrypted,attributes_locked,probe_duration_ms_total,created_at,updated_at) VALUES ('s','m','Video','http',8589934592,'ciphertext',1,3000000000,'now','now'),('null-size','m','Unknown size','http',NULL,'ciphertext',0,0,'now','now')`,
		`INSERT INTO user_item_states(user_id,item_id,playback_position_ticks,playback_runtime_ticks,is_favorite,user_rating,note,version,playback_state,playback_percent,created_at,updated_at) VALUES ('u','m',36000000000,72000000000,1,8,'Private note',7,'paused',50,'now','now')`,
		`INSERT INTO user_item_state_events(id,user_id,item_id,event_type,position_ticks,runtime_ticks,version,created_at) VALUES ('e','u','m','progress',36000000000,72000000000,7,'now')`,
		`INSERT INTO tasks(id,type,status,created_at,updated_at) VALUES ('t','backup','succeeded','now','now')`,
		`INSERT INTO backup_runs(id,task_id,requested_by,artifact_size,created_at) VALUES ('b','t','u',8589934592,'now')`,
		`INSERT INTO backup_run_targets(run_id,target_id,target_type,display_name,status,bytes_written) VALUES ('b','target','local','Local','succeeded',8589934592)`,
	} {
		if _, err := db.DB().Exec(query); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = Open(ctx, dsn, "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, column := range []struct {
		table, name, where string
		value              int64
	}{
		{"user_item_states", "playback_position_ticks", "user_id='u'", 36000000000},
		{"user_item_states", "playback_runtime_ticks", "user_id='u'", 72000000000},
		{"user_item_state_events", "position_ticks", "id='e'", 36000000000},
		{"user_item_state_events", "runtime_ticks", "id='e'", 72000000000},
		{"stream_sources", "size_bytes", "id='s'", 8589934592},
		{"stream_sources", "probe_duration_ms_total", "id='s'", 3000000000},
		{"online_providers", "probe_duration_ms_total", "id='p'", 3000000000},
		{"backup_runs", "artifact_size", "id='b'", 8589934592},
		{"backup_run_targets", "bytes_written", "run_id='b'", 8589934592},
	} {
		var kind string
		if err := db.DB().QueryRow(`SELECT type FROM pragma_table_info(?) WHERE name=?`, column.table, column.name).Scan(&kind); err != nil || kind != "BIGINT" {
			t.Fatalf("%s.%s declaration: %s %v", column.table, column.name, kind, err)
		}
		var value int64
		query := "SELECT " + column.name + " FROM " + column.table + " WHERE " + column.where
		if err := db.DB().QueryRow(query).Scan(&value); err != nil || value != column.value {
			t.Fatal("value lost", query, value, err)
		}
		// New writes must retain the same 64-bit range after upgrading.
		if _, err := db.DB().Exec("UPDATE "+column.table+" SET "+column.name+"=? WHERE "+column.where, column.value+1); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM user_item_states WHERE is_favorite=1 AND user_rating=8 AND note='Private note' AND version=7 AND playback_state='paused' AND playback_percent=50`,
		`SELECT COUNT(*) FROM stream_sources WHERE id='s' AND candidate_url_encrypted='ciphertext' AND attributes_locked=1`,
		`SELECT COUNT(*) FROM stream_sources WHERE id='null-size' AND size_bytes IS NULL`,
		`SELECT COUNT(*) FROM backup_runs b JOIN backup_run_targets t ON t.run_id=b.id WHERE b.requested_by='u' AND t.status='succeeded'`,
	} {
		var count int
		if err := db.DB().QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatal("unrelated data changed", query, count, err)
		}
	}
	if _, err := db.DB().Exec(`UPDATE user_item_states SET playback_position_ticks=-1`); err == nil {
		t.Fatal("negative progress accepted")
	}
	var broken int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&broken); err != nil || broken != 0 {
		t.Fatal("foreign keys changed", broken, err)
	}
}
