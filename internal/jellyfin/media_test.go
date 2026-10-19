package jellyfin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/userdata"
)

func TestGenericJellyfinHierarchyAndProviderMapping(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := auth.NewService(db, config.Config{}, nil)
	store := auth.NewStore(db)
	user, err := store.CreateUser(ctx, "admin", "Admin", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, user, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []struct{ id, kind, parent string }{{"series", "series", ""}, {"season", "season", "series"}, {"episode", "episode", "season"}, {"other-episode", "episode", "season-other"}, {"season-other", "season", "series-other"}, {"series-other", "series", ""}} {
		// Insert identity first; hierarchy is set after all identities exist.
		_, err = db.DB().Exec(`INSERT INTO media_items (id,source,code,raw_code,title,source_url,item_type,index_number,provider_ids_json,created_at,updated_at) VALUES (?,'tmdb',?,'',?,'',?,1,'{"tmdb":"42"}','now','now')`, it.id, "media:"+it.id, it.id, it.kind)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"season", "series"}, {"episode", "season"}, {"season-other", "series-other"}, {"other-episode", "season-other"}} {
		if _, err = db.DB().Exec(`UPDATE media_items SET parent_id=? WHERE id=?`, pair[1], pair[0]); err != nil {
			t.Fatal(err)
		}
	}
	c := catalog.NewService(db, a, nil)
	s := NewService(db, config.Config{}, a, c, userdata.NewService(db, a))
	router := chi.NewRouter()
	s.RegisterProtocolRoutes(router)
	for _, tc := range []struct{ path, wantID, wantType string }{{"/Shows/series/Seasons", "season", "Season"}, {"/Shows/series/Episodes", "episode", "Episode"}, {"/Items?ParentId=season&IncludeItemTypes=Episode", "episode", "Episode"}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)
		var result struct{ Items []BaseItemDto }
		if json.Unmarshal(rec.Body.Bytes(), &result) != nil || rec.Code != 200 || len(result.Items) != 1 {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body.String())
		}
		it := result.Items[0]
		if it.Id != tc.wantID || it.Type != tc.wantType || it.ProviderIds["tmdb"] != "42" || it.ProviderIds["JavDB"] != "" || it.ParentId == "" {
			t.Fatalf("mapping: %+v", it)
		}
	}
}
