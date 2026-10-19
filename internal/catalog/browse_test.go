package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/repository"
)

func TestLibraryBrowsingPagesTopLevelContentWithPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "browse.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	u, err := store.CreateUser(ctx, "viewer", "Viewer", "unused", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, u, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.NewService(db, config.Config{}, nil)
	s := NewService(db, a, nil)
	if err := s.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.DB().Exec(db.Rebind(q), args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 29; i++ {
		kind, parent, year, adult := "movie", "", 2024, 0
		if i == 26 {
			kind = "series"
		}
		if i == 27 {
			kind, parent = "season", "media26"
		}
		if i == 28 {
			kind, parent = "episode", "media27"
		}
		exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,item_type,parent_id,year,is_adult,created_at,updated_at) VALUES (?,?,?,?,'',?,?,?,?, 'now','now')`, fmt.Sprint("media", i), fmt.Sprint("code", i), "", fmt.Sprintf("Content %02d", i), kind, nullable(parent), year, adult)
		exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES ('library-default',?,'now')`, fmt.Sprint("media", i))
	}
	exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,is_adult,created_at,updated_at) VALUES ('hidden','hidden','','Hidden','',0,'now','now'),('adult','adult','','Adult','',1,'now','now')`)
	exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES ('library-default','adult','now')`)
	exec(`INSERT INTO virtual_libraries(id,name,filter_json,is_public,is_adult,created_at,updated_at) VALUES ('dynamic','Dynamic','{"years":[2024]}',1,0,'now','now')`)
	router := chi.NewRouter()
	s.RegisterRoutes(router)
	request := func(path string, want int) (items []Item, cursor string) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if want != 200 {
			return nil, ""
		}
		var body struct {
			Items      []Item
			NextCursor string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, item := range body.Items {
			if item.ID == "hidden" || item.ID == "adult" {
				t.Fatal("permission bypass", item)
			}
		}
		return body.Items, body.NextCursor
	}
	for _, base := range []string{"/items", "/libraries/library-default/items", "/libraries/dynamic/items"} {
		path := base + "?topLevel=true&sort=title&sortOrder=asc&limit=5"
		seen := map[string]bool{}
		cursor := ""
		for page := 0; page < 8; page++ {
			items, next := request(path+"&cursor="+url.QueryEscape(cursor), 200)
			for _, item := range items {
				if item.ParentID != "" || seen[item.ID] {
					t.Fatal("child or duplicate in root", item)
				}
				seen[item.ID] = true
			}
			cursor = next
			if cursor == "" {
				break
			}
		}
		if len(seen) != 27 || cursor != "" {
			t.Fatal(base, len(seen), cursor)
		}
		items, cursor := request(path, 200)
		if len(items) != 5 || cursor == "" {
			t.Fatal(items, cursor)
		}
		request(strings.Replace(path, "sort=title", "sort=year", 1)+"&cursor="+url.QueryEscape(cursor), 400)
		request(base+"?topLevel=true&parentId=media26", 400)
		request(base+"?itemType=invalid", 400)
		items, _ = request(base+"?topLevel=1&itemType=series", 200)
		if len(items) != 1 || items[0].ID != "media26" {
			t.Fatal(items)
		}
	}
	_, cursor := request("/libraries/library-default/items?limit=5", 200)
	request("/libraries/dynamic/items?limit=5&cursor="+url.QueryEscape(cursor), 400)
	items, _ := request("/items?parentId=media27", 200)
	if len(items) != 1 || items[0].ID != "media28" {
		t.Fatal(items)
	}
	exec(`UPDATE media_items SET index_number=2 WHERE id='media28'`)
	exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,item_type,parent_id,index_number,is_adult,created_at,updated_at) VALUES ('first-episode','first','','Z First','','episode','media27',1,0,'now','now')`)
	exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES ('library-default','first-episode','now')`)
	items, _ = request("/items?parentId=media27", 200)
	if len(items) != 2 || items[0].ID != "first-episode" || items[1].ID != "media28" {
		t.Fatal("episodes not ordered by number", items)
	}
}
