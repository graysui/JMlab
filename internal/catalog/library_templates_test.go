package catalog

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

type templateStream struct{ module.StreamProvider }

func TestLibraryTemplateScopesMatchActualModuleAndRankingRows(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "templates.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := module.NewRegistry()
	for _, id := range []string{"stream-one", "stream-two"} {
		if err := registry.Register(&module.ModuleEntry{Manifest: &module.Manifest{ID: id, SupportedItemTypes: []module.ItemType{module.ItemMovie}}, Status: module.StatusActive, StreamInstance: &templateStream{}}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(db, nil, nil)
	s.SetModuleRegistry(registry)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.DB().ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"daily-hit", "cross-snapshot", "other-module"} {
		moduleID := "reference-provider-javdb"
		if id == "other-module" {
			moduleID = "tmdb-source"
		}
		exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,is_adult,item_type,metadata_module_id,created_at,updated_at) VALUES (?,?,'',?,'',0,'movie',?,'now','now')`, id, id, id, moduleID)
	}
	for _, entry := range []struct{ id, kind, period string }{{"daily-hit", "ranking", "daily"}, {"cross-snapshot", "ranking", "weekly"}, {"cross-snapshot", "playback", "daily"}, {"other-module", "ranking", "daily"}} {
		exec(`INSERT INTO item_rankings(item_id,ranking_type,period,rank,observed_at,is_current,created_at) VALUES (?,?,?,1,'now',1,'now')`, entry.id, entry.kind, entry.period)
	}
	exec(`INSERT INTO stream_sources(id,item_id,module_id,label,protocol,status,enabled,created_at,updated_at) VALUES ('good','daily-hit','stream-one','good','http','active',1,'now','now'),('bad','cross-snapshot','stream-one','bad','http','unavailable',1,'now','now'),('other-good','cross-snapshot','stream-two','other','http','active',1,'now','now')`)
	assertFilter := func(raw string, want []string) {
		t.Helper()
		var filter any
		if err := json.Unmarshal([]byte(raw), &filter); err != nil {
			t.Fatal(err)
		}
		clause, args, err := s.libraryFilterWhere(filter, "user")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.DB().Query("SELECT vi.id FROM media_items vi WHERE "+clause+" ORDER BY vi.id", args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v want %v", raw, got, want)
		}
	}
	assertFilter(`{"metadataModuleId":"reference-provider-javdb","rankingType":"ranking","rankingPeriod":"daily"}`, []string{"daily-hit"})
	assertFilter(`{"all":[{"field":"rankingType","value":"ranking"},{"field":"rankingPeriod","value":"daily"}]}`, []string{"daily-hit", "other-module"})
	assertFilter(`{"streamModuleId":"stream-one"}`, []string{"daily-hit"})
	assertFilter(`{"hasPlayableSource":true}`, []string{"cross-snapshot", "daily-hit"})
	if err := registry.SetStatus("stream-one", module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	assertFilter(`{"streamModuleId":"stream-one"}`, []string{})
	assertFilter(`{"metadataModuleId":"reference-provider-javdb"}`, []string{"cross-snapshot", "daily-hit"})
}

func TestCreateLibraryRejectsStaleTemplateAndKeepsLegacyAPI(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "creation.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	user, err := store.CreateUser(ctx, "admin", "Admin", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, user, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.NewService(db, config.Config{}, nil)
	s := NewService(db, a, nil)
	// No library is created by service initialization or importing media.
	item, _, err := s.saveModuleMedia(ctx, "template-source", module.MediaDetail{SourceID: "one", Title: "作品", ItemType: module.ItemMovie}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var initialCount int
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM virtual_libraries").Scan(&initialCount); err != nil || initialCount != 0 {
		t.Fatal(initialCount, err)
	}
	registry := module.NewRegistry()
	s.SetModuleRegistry(registry)
	manifest := &module.Manifest{ID: "template-source", LibraryTemplates: []module.LibraryTemplate{{ID: "all-items"}}}
	if err := registry.Register(&module.ModuleEntry{Manifest: manifest, Status: module.StatusActive}); err != nil {
		t.Fatal(err)
	}
	entry, _ := registry.Get(manifest.ID)
	router := chi.NewRouter()
	s.RegisterRoutes(router)
	post := func(selection any, status int) {
		t.Helper()
		payload := map[string]any{"name": "测试虚拟库", "filter": map[string]any{"metadataModuleId": "template-source"}}
		if selection != nil {
			payload["template"] = selection
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/libraries", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
	}
	selection := libraryTemplateSelection{ModuleID: manifest.ID, TemplateID: "all-items", Revision: entry.Revision}
	post(selection, 201)
	var members int
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM library_items WHERE item_id=?", item.ID).Scan(&members); err != nil || members != 1 {
		t.Fatal(members, err)
	}
	if err := registry.SetStatus(manifest.ID, module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	post(selection, 409)
	if err := registry.SetStatus(manifest.ID, module.StatusActive, ""); err != nil {
		t.Fatal(err)
	}
	post(selection, 409)
	post(nil, 201) // Existing integrations and saved libraries do not require a template.
	var count int
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM virtual_libraries").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if _, err := db.DB().Exec("DELETE FROM virtual_libraries"); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(db, a, nil)
	if err := restarted.SyncLibraryMemberships(ctx, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM virtual_libraries").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
