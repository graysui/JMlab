package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/home"
	imagecache "github.com/graysui/JMlab/internal/image"
	"github.com/graysui/JMlab/internal/jellyfin"
	"github.com/graysui/JMlab/internal/relations"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/userdata"
)

func TestPermissionsAgreeAcrossWebHomeImagesRelationsAndJellyfin(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(root, "access.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	viewer, err := store.CreateUser(ctx, "viewer", "Viewer", "unused", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	pref, err := store.GetPreferences(ctx, viewer.ID)
	if err != nil || pref.AdultConfirmedAt != nil {
		t.Fatal("user automatically confirmed adult content", pref, err)
	}
	_, token, err := store.CreateSession(ctx, viewer, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: root, MasterKey: strings.Repeat("test", 16)}
	a := auth.NewService(db, cfg, nil)
	c := catalog.NewService(db, a, nil)
	if err := c.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	u := userdata.NewService(db, a)
	im := imagecache.NewService(db, a, cfg, nil)
	j := jellyfin.NewService(db, cfg, a, c, u)
	j.SetImageService(im)
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		a.RegisterRoutes(r)
		c.RegisterRoutes(r)
		home.NewService(db, a, c, nil).RegisterRoutes(r)
		relations.NewService(db, a).RegisterRoutes(r)
		im.RegisterRoutes(r)
		u.RegisterRoutes(r)
	})
	j.RegisterProtocolRoutes(router)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.DB().ExecContext(ctx, db.Rebind(query), args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE virtual_libraries SET is_public=0 WHERE id='library-default'`)
	exec(`INSERT INTO role_library_permissions(role_id,library_id,can_view,can_play,created_at,updated_at) SELECT id,'library-default',1,0,'now','now' FROM roles WHERE name='Viewer'`)
	for _, lib := range []struct {
		id            string
		adult, public int
	}{{"lib-hidden", 0, 0}, {"lib-adult", 1, 1}} {
		exec(`INSERT INTO virtual_libraries(id,name,is_adult,is_public,created_at,updated_at) VALUES (?,?,?,?,'now','now')`, lib.id, lib.id, lib.adult, lib.public)
	}
	for _, it := range []struct {
		id, lib string
		adult   int
	}{{"visible", "library-default", 0}, {"adult", "library-default", 1}, {"hidden", "lib-hidden", 0}, {"libraryadult", "lib-adult", 0}, {"removed", "library-default", 0}} {
		exec(`INSERT INTO media_items(id,source,code,raw_code,title,source_url,is_adult,created_at,updated_at) VALUES (?,'tmdb',?,'',?,'',?,'2026-10-17','2026-10-17')`, it.id, "media:"+it.id, "Fixture "+it.id, it.adult)
		exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES (?,?,'now')`, it.lib, it.id)
		exec(`INSERT INTO entities(entity_kind,id,name,created_at,updated_at) VALUES ('people',?,?,'now','now')`, "people_"+it.id, "Fixture person "+it.id)
		exec(`INSERT INTO item_entities(item_id,entity_kind,entity_id,role,sort_order) VALUES (?,'people',?,'actor',0)`, it.id, "people_"+it.id)
		exec(`INSERT INTO item_images(id,item_id,image_type,source_url,cache_key,status,created_at,updated_at) VALUES (?,?,'poster','',?,'ready','now','now')`, "img-"+it.id, it.id, "cache-"+it.id)
		exec(`INSERT INTO user_person_states(user_id,person_id,is_favorite,created_at,updated_at) VALUES (?,?,1,'now','now')`, viewer.ID, "people_"+it.id)
		exec(`INSERT INTO user_image_states(user_id,image_id,is_favorite,created_at,updated_at) VALUES (?,?,1,'now','now')`, viewer.ID, "img-"+it.id)
	}
	exec(`UPDATE media_items SET deleted_at='now' WHERE id='removed'`)
	exec(`INSERT INTO collections(id,name,source,is_public,poster_image_id,created_at,updated_at) VALUES ('public','Shared','manual',1,'img-hidden','now','now')`)
	exec(`INSERT INTO collections(id,name,source,is_public,created_at,updated_at) VALUES ('private','Private','manual',0,'now','now')`)
	exec(`INSERT INTO collection_items(collection_id,item_id,position,created_at) SELECT 'public',id,0,'now' FROM media_items`)
	request := func(method, path, body string, want int, anonymous bool) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		requestCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		r = r.WithContext(requestCtx)
		if !anonymous {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	listPaths := []string{"/api/v1/items", "/api/v1/search/hints?q=Fixture", "/api/v1/libraries/library-default/items", "/api/v1/home", "/api/v1/people", "/api/v1/me/people/favorites", "/api/v1/collections/public/items", "/Items", "/Items/Latest", "/Search/Hints?SearchTerm=Fixture", "/Persons?SearchTerm=Fixture"}
	assertLists := func(adult bool) {
		t.Helper()
		for _, path := range listPaths {
			body := request("GET", path, "", 200, false)
			for _, name := range []string{"Fixture hidden", "Fixture person hidden", "Fixture removed", "Fixture person removed"} {
				if strings.Contains(body, name) {
					t.Fatal("hidden media leaked", path, body)
				}
			}
			if !adult {
				for _, name := range []string{"Fixture adult", "Fixture person adult", "Fixture libraryadult", "Fixture person libraryadult"} {
					if strings.Contains(body, name) {
						t.Fatal("adult media leaked", path, body)
					}
				}
			}
			if !strings.Contains(body, "Fixture visible") && !strings.Contains(body, "Fixture person visible") {
				t.Fatal("role-authorized media missing", path, body)
			}
		}
	}
	assertLists(false)
	assertCollectionCounts := func(want int) {
		t.Helper()
		for _, path := range []string{"/api/v1/collections", "/api/v1/collections/public", "/api/v1/collections/public/items"} {
			body := request("GET", path, "", 200, false)
			if !strings.Contains(body, fmt.Sprintf(`"itemCount":%d`, want)) || strings.Contains(body, "img-hidden") || strings.Contains(body, "Private") {
				t.Fatal("collection leaked cover/count/private metadata", path, body)
			}
		}
		body := request("GET", "/api/v1/me/images/favorites", "", 200, false)
		var favorites struct {
			Items []map[string]any `json:"items"`
		}
		if json.Unmarshal([]byte(body), &favorites) != nil || len(favorites.Items) != want || strings.Contains(body, "img-hidden") || strings.Contains(body, "img-removed") {
			t.Fatal("favorite images leaked or lost authorized content", body)
		}
	}
	assertCollectionCounts(1)
	request("GET", "/api/v1/collections/private", "", 404, false)
	request("POST", "/api/v1/me/people/people_hidden/favorite", "", 404, false)
	request("POST", "/api/v1/me/people/people_adult/favorite", "", 404, false)
	request("POST", "/api/v1/me/images/img-hidden/favorite", "", 404, false)
	request("DELETE", "/api/v1/me/people/people_hidden/favorite", "", 200, false)
	for _, path := range []string{"/api/v1/items/adult/images/poster", "/Items/adult/Images/Primary", "/Items/people_adult/Images/Primary", "/Items/lib-hidden/Images/Primary", "/api/v1/people/people_adult", "/api/v1/libraries/lib-adult"} {
		request("GET", path, "", 404, false)
	}
	for _, path := range []string{"/api/v1/items/visible/images/poster", "/Items/visible/Images/Primary", "/Items/library-default/Images/Primary"} {
		request("GET", path, "", 401, true)
		request("GET", path, "", 200, false)
	}
	if auth.CanAccessMedia(ctx, db, viewer, "visible", true) {
		t.Fatal("view-only role could play")
	}
	request("PATCH", "/api/v1/me/preferences", `{"adultConfirmed":true}`, 200, false)
	assertLists(true)
	assertCollectionCounts(3)
	request("GET", "/Items/adult/Images/Primary", "", 200, false)
	request("GET", "/Items/people_adult/Images/Primary", "", 200, false)
	request("PATCH", "/api/v1/me/preferences", `{"theme":{"mode":"light"}}`, 200, false)
	pref, _ = store.GetPreferences(ctx, viewer.ID)
	if pref.AdultConfirmedAt == nil {
		t.Fatal("unrelated preferences cleared confirmation")
	}
	request("PATCH", "/api/v1/me/preferences", `{"adultConfirmed":false}`, 200, false)
	if err := a.EnsureBootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	assertLists(false)
	assertCollectionCounts(1)
	// A restart may add missing defaults, never reset administrator choices.
	exec(`UPDATE virtual_libraries SET name='Custom',is_public=0,enabled=0,is_adult=1,sort_json='{"field":"title","direction":"asc"}' WHERE id IN ('library-default','library-ranking-daily')`)
	if err := c.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM virtual_libraries WHERE id IN ('library-default','library-ranking-daily') AND name='Custom' AND is_public=0 AND enabled=0 AND is_adult=1`).Scan(&count); err != nil || count != 2 {
		t.Fatal("startup overwrote library choices", count, err)
	}
	request("GET", "/api/v1/items/visible/images/poster", "", 404, false)
	request("GET", "/Items/library-default/Images/Primary", "", 404, false)
	assertCollectionCounts(0)
	if body := request("GET", "/api/v1/me/people/favorites", "", 200, false); strings.Contains(body, "Fixture") {
		t.Fatal("favorite people remained visible after library disabled", body)
	}
}
