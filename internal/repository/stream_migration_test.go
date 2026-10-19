package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func migrationsThrough(t *testing.T, last string) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() > last {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../../migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func streamSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT id,item_id,label,protocol,quality,language,candidate_url_encrypted,resolved_url_encrypted,resolved_headers_encrypted,duration_seconds,size_bytes,is_preview,status,last_resolved_at,expires_at,last_error,sort_order,created_at,updated_at,enabled,last_probe_status,last_probe_at,last_probe_duration_ms,last_probe_error,probe_count,probe_success_count,probe_duration_ms_total FROM stream_sources ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var values [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestStreamIdentityMigrationPreservesAllCandidateData(t *testing.T) {
	ctx := context.Background()
	prior := migrationsThrough(t, "00024_module_restore_queue.sql")
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "upgrade.db"))
	db, err := Open(ctx, dsn, prior, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO media_items (id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('item','code','code','Title','https://example.test','now','now')`,
		`INSERT INTO online_providers (id,provider_type,display_name,url_template,priority,created_at,updated_at) VALUES ('legacy','trailer','Legacy','https://example.test',80,'now','now'),('module:external','external-online','External','',60,'now','now'),('module:reference-stream-builtin','external-online','Builtin','',50,'now','now')`,
		`INSERT INTO stream_sources (id,item_id,provider_id,module_id,label,protocol,quality,language,candidate_url_encrypted,resolved_url_encrypted,resolved_headers_encrypted,duration_seconds,size_bytes,is_preview,status,last_resolved_at,expires_at,last_error,sort_order,created_at,updated_at,enabled,last_probe_status,last_probe_at,last_probe_duration_ms,last_probe_error,probe_count,probe_success_count,probe_duration_ms_total) VALUES ('old','item','legacy','reference-stream-builtin','Preview','hls','1080p','zh','candidate-secret','resolved-secret','headers-secret',123,123456,1,'expired','resolved-at','expires-at','last-error',7,'created','updated',0,'unhealthy','probed-at',45,'probe-error',5,3,200)`,
		`INSERT INTO stream_sources (id,item_id,provider_id,module_id,label,protocol,candidate_url_encrypted,created_at,updated_at) VALUES ('external','item','module:external',NULL,'First','http','opaque-encrypted','now','now'),('builtin','item','module:reference-stream-builtin','reference-stream-builtin','Second','http','builtin-opaque-encrypted','now','now')`,
	} {
		if _, err := db.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	before := streamSnapshot(t, db.DB())
	db.Close()
	db, err = Open(ctx, dsn, "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if after := streamSnapshot(t, db.DB()); !reflect.DeepEqual(before, after) {
		t.Fatalf("candidate fields changed:\n%s\n%s", before, after)
	}
	for _, expected := range []struct {
		id, module, provider string
		priority             int
	}{
		{"old", "reference-stream-builtin", "legacy", 80},
		{"external", "external", "", 60},
		{"builtin", "reference-stream-builtin", "", 50},
	} {
		var module, provider string
		var priority int
		if err := db.DB().QueryRow(`SELECT module_id,COALESCE(provider_id,''),priority FROM stream_sources WHERE id=?`, expected.id).Scan(&module, &provider, &priority); err != nil {
			t.Fatal(err)
		}
		if module != expected.module || provider != expected.provider || priority != expected.priority {
			t.Fatalf("incorrect identity: %s %s %s %d", expected.id, module, provider, priority)
		}
	}
	var placeholders int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM online_providers WHERE id LIKE 'module:%'`).Scan(&placeholders); err != nil || placeholders != 0 {
		t.Fatal("placeholder providers remain", err)
	}
	rows, err := db.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key broken")
	}
	rows.Close()
	if _, err := db.DB().Exec(`INSERT INTO stream_sources (id,item_id,module_id,label,protocol,created_at,updated_at) VALUES ('new','item','new-module','New','http','now','now')`); err != nil {
		t.Fatal("module source still needs provider", err)
	}
	if _, err := db.DB().Exec(`DELETE FROM media_items WHERE id='item'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM stream_sources`).Scan(&count); err != nil || count != 0 {
		t.Fatal("source cascade lost", err)
	}
}

func TestStreamIdentityMigrationFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	prior := migrationsThrough(t, "00024_module_restore_queue.sql")
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "failure.db"))
	db, err := Open(ctx, dsn, prior, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO media_items (id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('item','code','code','Title','https://example.test','now','now')`,
		`INSERT INTO online_providers (id,provider_type,display_name,url_template,created_at,updated_at) VALUES ('module:','external-online','Malformed','','now','now')`,
		`INSERT INTO stream_sources (id,item_id,provider_id,label,protocol,candidate_url_encrypted,created_at,updated_at) VALUES ('source','item','module:','Name','http','secret','now','now')`,
	} {
		if _, err := db.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	before := streamSnapshot(t, db.DB())
	db.Close()
	if upgraded, err := Open(ctx, dsn, "../../migrations", nil); err == nil {
		upgraded.Close()
		t.Fatal("malformed identity accepted")
	}
	db, err = Open(ctx, dsn, prior, nil)
	if err != nil {
		t.Fatal("cannot reopen original schema after rollback", err)
	}
	defer db.Close()
	if streamSnapshot(t, db.DB()) != before {
		t.Fatal("failed upgrade changed source data")
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='stream_sources_next'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial rebuild committed", err)
	}
}
