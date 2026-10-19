package entities

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/graysui/JMlab/internal/repository"
)

func TestExternalIdentitySeparatesNamesAndPreservesManualFields(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "entities.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO entities (entity_kind,id,name,created_at,updated_at) VALUES ('people','legacy','同名人物','now','now'),('studios','legacy','同名公司','now','now')`); err != nil {
		t.Fatal(err)
	}
	ensure := func(kind, name, namespace, value string) string {
		t.Helper()
		id, err := Ensure(ctx, tx, db, Input{Kind: kind, Name: name, Identities: []Identity{{Namespace: namespace, Value: value}}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := ensure("people", "同名人物", "module:tmdb", "person:1")
	if first != "legacy" {
		t.Fatal("old unclaimed identity was lost")
	}
	second := ensure("people", "同名人物", "module:tmdb", "person:2")
	if first == second {
		t.Fatal("different source people merged by name")
	}
	if again := ensure("people", "新名字", "module:tmdb", "person:1"); again != first {
		t.Fatal("title update changed entity identity")
	}
	company := ensure("studios", "同名公司", "module:tmdb", "company:1")
	if company != "legacy" {
		t.Fatal("cross-kind legacy identity lost")
	}
	if _, err := tx.Exec(`UPDATE entities SET name='手工姓名',attributes_locked=1,is_excluded=1 WHERE entity_kind='people' AND id=?`, first); err != nil {
		t.Fatal(err)
	}
	ensure("people", "上游姓名", "module:tmdb", "person:1")
	var name string
	var excluded int
	if err := tx.QueryRow(`SELECT name,is_excluded FROM entities WHERE entity_kind='people' AND id=?`, first).Scan(&name, &excluded); err != nil || name != "手工姓名" || excluded != 1 {
		t.Fatal("source overwrote administrator fields", err)
	}
	merged, err := Ensure(ctx, tx, db, Input{Kind: "people", Name: "Person", Identities: []Identity{{Namespace: "module:tmdb", Value: "person:1"}, {Namespace: "provider:imdb", Value: "nm1"}}})
	if err != nil || merged != first {
		t.Fatal("provider identity not attached", err)
	}
	if linked := ensure("people", "Person", "provider:imdb", "nm1"); linked != first {
		t.Fatal("provider identity did not deduplicate across modules")
	}
	_, err = Ensure(ctx, tx, db, Input{Kind: "people", Name: "Conflict", Identities: []Identity{{Namespace: "module:tmdb", Value: "person:2"}, {Namespace: "provider:imdb", Value: "nm1"}}})
	if err == nil {
		t.Fatal("conflicting external IDs silently merged")
	}
	if _, err := tx.Exec(`INSERT INTO media_items (id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('movie','code','code','Movie','https://example.test','now','now')`); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"actor", "director", "writer"} {
		if err := Link(ctx, tx, db, "movie", "people", first, role, 0); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM item_people WHERE item_id='movie' AND person_id=?`, first).Scan(&count); err != nil || count != 3 {
		t.Fatal("multiple person roles not preserved", err)
	}
	if err := Link(ctx, tx, db, "movie", "studios", company, "writer", 0); err == nil {
		t.Fatal("role accepted for wrong entity kind")
	}
}
