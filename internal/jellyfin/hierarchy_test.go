package jellyfin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/userdata"
)

func TestJellyfinHierarchyUsesVisibleParentsCountsAndNaturalEpisodeOrder(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "hierarchy.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	user, err := store.CreateUser(ctx, "viewer", "Viewer", "unused", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, user, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.NewService(db, config.Config{}, nil)
	c := catalog.NewService(db, a, nil)
	if err := c.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.DB().Exec(db.Rebind(q), args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, it := range []struct {
		id, kind, parent, title string
		index                   int
		visible                 bool
	}{
		{"show", "series", "", "Visible Show", 0, true},
		{"s0", "season", "show", "Specials", 0, true}, {"s1", "season", "show", "Z First Season", 1, true}, {"s10", "season", "show", "A Tenth Season", 10, true},
		{"e0", "episode", "s0", "Special", 1, true}, {"e1", "episode", "s1", "Z First Episode", 1, true}, {"e2", "episode", "s1", "A Second Episode", 2, true}, {"e10", "episode", "s10", "Tenth Season Episode", 1, true},
		{"hidden-episode", "episode", "s1", "Private Episode", 3, false},
		{"hidden-season", "season", "show", "Private Season", 2, false},
		{"separate-episode", "episode", "hidden-season", "Separately Visible Episode", 1, true},
		{"movie", "movie", "", "Movie", 0, true},
	} {
		var parent any
		if it.parent != "" {
			parent = it.parent
		}
		exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,item_type,parent_id,index_number,is_adult,created_at,updated_at) VALUES (?,?,'',?,'',?,?,?,0,'now','now')`, it.id, it.id, it.title, it.kind, parent, it.index)
		if it.visible {
			exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES ('library-default',?,'now')`, it.id)
		}
	}
	router := chi.NewRouter()
	NewService(db, config.Config{}, a, c, userdata.NewService(db, a)).RegisterProtocolRoutes(router)
	request := func(path string) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r.WithContext(rctx))
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Private Season") || strings.Contains(w.Body.String(), "Private Episode") {
			t.Fatal("private hierarchy leaked", w.Body.String())
		}
		return w.Body.Bytes()
	}
	readItem := func(path string) BaseItemDto {
		t.Helper()
		var dto BaseItemDto
		if err := json.Unmarshal(request(path), &dto); err != nil {
			t.Fatal(err)
		}
		return dto
	}
	readItems := func(path string) []BaseItemDto {
		t.Helper()
		var result struct{ Items []BaseItemDto }
		if err := json.Unmarshal(request(path), &result); err != nil {
			t.Fatal(err)
		}
		return result.Items
	}
	assertOrder := func(path string, want ...string) {
		t.Helper()
		items := readItems(path)
		if len(items) != len(want) {
			t.Fatal(path, items)
		}
		for i, item := range items {
			if item.Id != want[i] {
				t.Fatal(path, items)
			}
		}
	}
	assertOrder("/Shows/show/Seasons", "s0", "s1", "s10")
	assertOrder("/Shows/show/Episodes", "e0", "e1", "e2", "e10")
	assertOrder("/Shows/show/Episodes?SeasonId=s1", "e1", "e2")
	assertOrder("/Items?parentId=show&recursive=true&includeItemTypes=Episode", "e0", "e1", "e2", "e10")
	assertOrder("/Items?parentId=s1", "e1", "e2")
	if items := readItems("/Items?ParentId=library-default"); len(items) != 2 {
		t.Fatal("root mixed in episodes", items)
	}
	if items := readItems("/Items?Recursive=true&IncludeItemTypes=Episode"); len(items) != 5 {
		t.Fatal(items)
	}
	show := readItem("/Items/show")
	if show.ChildCount != 3 || show.RecursiveItemCount != 7 || !show.IsFolder || show.Container != "" || show.MediaType != "" {
		t.Fatal(show)
	}
	season := readItem("/Items/s1")
	if season.ChildCount != 2 || season.SeriesId != "show" || season.SeriesName != "Visible Show" {
		t.Fatal(season)
	}
	episode := readItem("/Items/e0")
	if episode.SeriesId != "show" || episode.SeasonId != "s0" || episode.ParentIndexNumber == nil || *episode.ParentIndexNumber != 0 {
		t.Fatal(episode)
	}
	listed := readItems("/Shows/show/Episodes")[0]
	if listed.SeriesId != episode.SeriesId || listed.SeasonId != episode.SeasonId || listed.ParentIndexNumber == nil || *listed.ParentIndexNumber != 0 {
		t.Fatal("batch and detail diverged", listed, episode)
	}
	separate := readItem("/Items/separate-episode")
	if separate.ParentId != "" || separate.SeasonId != "" || separate.SeriesId != "" || separate.SeasonName != "" {
		t.Fatal("hidden parent identity leaked", separate)
	}
	for _, path := range []string{"/Items/Latest", "/Items/Latest?ParentId=library-default"} {
		var items []BaseItemDto
		if err := json.Unmarshal(request(path), &items); err != nil || len(items) != 2 {
			t.Fatal(path, items, err)
		}
	}
	var children []BaseItemDto
	if err := json.Unmarshal(request("/Items/Latest?ParentId=s1"), &children); err != nil || len(children) != 2 {
		t.Fatal(children, err)
	}
}
