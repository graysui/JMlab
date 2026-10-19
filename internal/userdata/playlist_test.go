package userdata

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
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/repository"
)

func TestPlaylistSharingRespectsMediaPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	owner, err := store.CreateUser(ctx, "owner", "Owner", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateUser(ctx, "viewer", "Viewer", "unused", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, ownerToken, err := store.CreateSession(ctx, owner, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, viewerToken, err := store.CreateSession(ctx, viewer, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.DB().ExecContext(ctx, db.Rebind(q), args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"allowed", "adult", "hidden", "removed"} {
		adult := 0
		if id == "adult" {
			adult = 1
		}
		exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,is_adult,created_at,updated_at) VALUES (?,?,?,?,'',?,'now','now')`, id, id, id, id, adult)
	}
	exec(`UPDATE media_items SET deleted_at='now' WHERE id='removed'`)
	exec(`INSERT INTO virtual_libraries(id,name,is_public,is_adult,created_at,updated_at) VALUES ('role-library','Role library',0,0,'now','now')`)
	exec(`INSERT INTO role_library_permissions(role_id,library_id,can_view,can_play,created_at,updated_at) SELECT id,'role-library',1,0,'now','now' FROM roles WHERE name='Viewer'`)
	for _, id := range []string{"allowed", "adult", "removed"} {
		exec(`INSERT INTO library_items(library_id,item_id,created_at) VALUES ('role-library',?,'now')`, id)
	}
	exec(`UPDATE user_preferences SET adult_confirmed_at=NULL WHERE user_id=?`, viewer.ID)
	root := chi.NewRouter()
	NewService(db, auth.NewService(db, config.Config{}, nil)).RegisterRoutes(root)
	request := func(method, path, token, body string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s = %d: %s", method, path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	var public, private, mine Playlist
	_ = json.Unmarshal([]byte(request("POST", "/playlists", ownerToken, `{"name":"Shared","isPublic":true}`, 201)), &public)
	_ = json.Unmarshal([]byte(request("POST", "/playlists", ownerToken, `{"name":"Private"}`, 201)), &private)
	_ = json.Unmarshal([]byte(request("POST", "/playlists", viewerToken, `{"name":"Mine"}`, 201)), &mine)
	for _, id := range []string{"allowed", "adult", "hidden"} {
		request("POST", "/playlists/"+public.ID+"/items", ownerToken, `{"itemId":"`+id+`"}`, 201)
	}
	request("POST", "/playlists/"+mine.ID+"/items", viewerToken, `{"itemId":"allowed"}`, 201)
	for _, id := range []string{"adult", "hidden", "removed", "missing"} {
		request("POST", "/playlists/"+mine.ID+"/items", viewerToken, `{"itemId":"`+id+`"}`, 404)
	}
	request("GET", "/playlists/"+private.ID+"/items", viewerToken, "", 404)
	request("POST", "/playlists/"+public.ID+"/items", viewerToken, `{"itemId":"allowed"}`, 404)
	request("DELETE", "/playlists/"+public.ID, viewerToken, "", 404)
	request("PATCH", "/playlists/"+mine.ID, viewerToken, `{"name":" "}`, 400)
	request("GET", "/me/items/hidden/state", viewerToken, "", 404)
	request("PUT", "/me/items/hidden/state", viewerToken, `{"favorite":true}`, 404)
	request("PUT", "/me/items/allowed/state", viewerToken, `{"note":"hello"}`, 200)
	request("PUT", "/me/items/allowed/state", viewerToken, `{"note":""}`, 200)
	if response := request("GET", "/me/items/allowed/state", viewerToken, "", 200); strings.Contains(response, "hello") {
		t.Fatal("empty note was not saved")
	}
	var result struct {
		Items []PlaylistItem `json:"items"`
	}
	_ = json.Unmarshal([]byte(request("GET", "/playlists/"+public.ID+"/items", viewerToken, "", 200)), &result)
	if len(result.Items) != 1 || result.Items[0].ItemID != "allowed" || result.Items[0].Title != "allowed" {
		t.Fatalf("leaked items: %+v", result.Items)
	}
	if auth.CanAccessMedia(ctx, db, viewer, "allowed", true) {
		t.Fatal("view-only permission allowed playback")
	}
	exec(`UPDATE role_library_permissions SET can_view=0 WHERE library_id='role-library'`)
	_ = json.Unmarshal([]byte(request("GET", "/playlists/"+mine.ID+"/items", viewerToken, "", 200)), &result)
	if len(result.Items) != 0 {
		t.Fatal("revoked library remains visible")
	}
}
