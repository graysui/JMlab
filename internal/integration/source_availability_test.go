package integration_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/home"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

// These queries only inspect capabilities; any unexpected network call fails.
type recordedStream struct{ module.StreamProvider }

func TestRecordedSourcesAgreeAcrossCatalogLibrariesHomeAndStatistics(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "sources.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	admin, err := store.CreateUser(ctx, "admin", "Admin", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, admin, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.NewService(db, config.Config{}, nil)
	c := catalog.NewService(db, a, nil)
	registry := module.NewRegistry()
	c.SetModuleRegistry(registry)
	for _, id := range []string{"stream-test", "reference-stream-builtin"} {
		if err := registry.Register(&module.ModuleEntry{Manifest: &module.Manifest{ID: id, SupportedItemTypes: []module.ItemType{module.ItemMovie}}, Status: module.StatusActive, StreamInstance: &recordedStream{}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.DB().ExecContext(ctx, db.Rebind(query), args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO virtual_libraries(id,name,filter_json,is_public,is_adult,created_at,updated_at) VALUES ('filtered','Available','{"hasPlayableSource":true}',1,0,'now','now')`)
	exec(`INSERT INTO online_providers(id,provider_type,display_name,url_template,enabled,created_at,updated_at) VALUES ('legacy','trailer','Legacy','https://example.test',1,'now','now'),('off','trailer','Disabled','https://example.test',0,'now','now')`)
	for _, source := range []struct {
		id, moduleID, itemType, status string
		provider                       any
		enabled                        int
	}{
		{"http", "stream-test", "movie", "active", nil, 1},
		{"legacy", "reference-stream-builtin", "movie", "active", "legacy", 1},
		{"disabled", "stream-test", "movie", "active", nil, 0},
		{"failed", "stream-test", "movie", "unavailable", nil, 1},
		{"missing-module", "missing", "movie", "active", nil, 1},
		{"wrong-type", "stream-test", "series", "active", nil, 1},
		{"disabled-provider", "reference-stream-builtin", "movie", "active", "off", 1},
	} {
		exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,is_adult,item_type,created_at,updated_at) VALUES (?,?,'',?,'',0,?,'now','now')`, source.id, source.id, source.id, source.itemType)
		exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES ('library-default',?,'now')`, source.id)
		exec(`INSERT INTO stream_sources(id,item_id,module_id,provider_id,label,protocol,status,enabled,created_at,updated_at) VALUES (?,?,?,?,?,'http',?,?,'now','now')`, "src-"+source.id, source.id, source.moduleID, source.provider, source.id, source.status, source.enabled)
	}
	// A second visible membership must not duplicate the source count.
	exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES ('filtered','http','now')`)
	exec(`INSERT INTO user_item_states(user_id,item_id,last_played_at,created_at,updated_at) VALUES (?,'http','2026-10-18','now','now'),(?,'legacy','2026-10-17','now','now')`, admin.ID, admin.ID)
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		a.RegisterRoutes(r)
		c.RegisterRoutes(r)
		home.NewService(db, a, c, nil).RegisterRoutes(r)
	})
	get := func(path string, output any) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), output) != nil {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	assert := func(want []string) {
		t.Helper()
		for _, path := range []string{"/api/v1/items?hasPlayableSource=true", "/api/v1/libraries/filtered/items"} {
			var list struct{ Items []catalog.Item }
			get(path, &list)
			ids := []string{}
			for _, item := range list.Items {
				ids = append(ids, item.ID)
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, want) {
				t.Fatal(path, ids, want)
			}
		}
		var dashboard home.Dashboard
		get("/api/v1/home", &dashboard)
		if dashboard.Stats.PlayableItems != len(want) || len(dashboard.Modules["playable"].Items) != len(want) {
			t.Fatalf("home mismatch: %+v", dashboard)
		}
		var stats home.StatisticsOverview
		get("/api/v1/statistics/overview", &stats)
		if stats.Stats.PlayableItems != len(want) {
			t.Fatal("statistics mismatch", stats.Stats)
		}
		var resources home.AdminDashboard
		get("/api/v1/dashboard", &resources)
		if resources.Resources.PlaybackSources != len(want) {
			t.Fatal("source count mismatch", resources.Resources)
		}
	}
	assert([]string{"http", "legacy"})
	var dashboard home.Dashboard
	get("/api/v1/home", &dashboard)
	recent := dashboard.Modules["recentlyPlayed"].Items
	if len(recent) != 2 || recent[0].ID != "http" || recent[1].ID != "legacy" {
		t.Fatal("recent play order", recent)
	}
	if err := registry.SetStatus("stream-test", module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	assert([]string{"legacy"})
	if err := registry.SetStatus("stream-test", module.StatusActive, ""); err != nil {
		t.Fatal(err)
	}
	assert([]string{"http", "legacy"})
	registry.Remove("stream-test")
	assert([]string{"legacy"})
}
