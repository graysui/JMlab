package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestJellyfinPagesCountDeduplicatedMediaAndPreserveUserData(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "pages.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	tokens := []string{}
	users := []auth.User{}
	for _, name := range []string{"one", "two"} {
		u, err := store.CreateUser(ctx, name, name, "unused", "Viewer")
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := store.CreateSession(ctx, u, "test", "test", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
		users = append(users, u)
	}
	a := auth.NewService(db, config.Config{}, nil)
	c := catalog.NewService(db, a, nil)
	if err := c.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	tx, err := db.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 1203; i++ {
		id := fmt.Sprintf("m%04d", i)
		if _, err := tx.Exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,is_adult,created_at,updated_at) VALUES (?,?,?,?,'',0,'now','now')`, id, id, id, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO virtual_libraries(id,name,is_public,is_adult,created_at,updated_at) VALUES ('second','Second',1,0,'now','now')`,
		`INSERT INTO library_items(library_id,item_id,created_at) SELECT 'library-default',id,'now' FROM media_items`,
		`INSERT INTO library_items(library_id,item_id,created_at) SELECT 'second',id,'now' FROM media_items`,
	} {
		if _, err := tx.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	u := userdata.NewService(db, a)
	fav, watched, rating, pos := true, true, 9, int64(123456789)
	if _, err := u.UpsertItemState(ctx, users[0].ID, "", "m0700", userdata.StateInput{Favorite: &fav, Watched: &watched, Rating: &rating, PositionTicks: &pos}); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	NewService(db, config.Config{}, a, c, u).RegisterProtocolRoutes(router)
	request := func(path, token string) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		requestCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r.WithContext(requestCtx))
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var page struct {
		Items                        []BaseItemDto
		TotalRecordCount, StartIndex int
	}
	if err := json.Unmarshal(request("/Items?SortBy=Name&StartIndex=700&Limit=3", tokens[0]), &page); err != nil {
		t.Fatal(err)
	}
	if page.TotalRecordCount != 1203 || page.StartIndex != 700 || len(page.Items) != 3 || page.Items[0].Id != "m0700" || page.Items[2].Id != "m0702" {
		t.Fatal(page)
	}
	var detail BaseItemDto
	if err := json.Unmarshal(request("/Items/m0700", tokens[0]), &detail); err != nil {
		t.Fatal(err)
	}
	listState, detailState := page.Items[0].UserData, detail.UserData
	if listState == nil || detailState == nil || listState.PlaybackPositionTicks != pos || listState.Rating == nil || *listState.Rating != 9 || !listState.IsFavorite || !listState.Played || listState.PlayCount != 1 || listState.PlaybackPositionTicks != detailState.PlaybackPositionTicks {
		t.Fatal("list lost state", listState, detailState)
	}
	page.Items = nil // Do not reuse nested pointers when decoding an independent response.
	if err := json.Unmarshal(request("/Items?SortBy=Name&StartIndex=700&Limit=1", tokens[1]), &page); err != nil {
		t.Fatal(err)
	}
	if state := page.Items[0].UserData; state == nil || state.Played || state.IsFavorite || state.Rating != nil || state.PlaybackPositionTicks != 0 {
		t.Fatal("state crossed users", state)
	}
	for _, path := range []string{"/Items?Filters=IsFavorite", "/Items?Filters=IsPlayed", "/Items?SearchTerm=m0700"} {
		page.Items = nil
		if err := json.Unmarshal(request(path, tokens[0]), &page); err != nil {
			t.Fatal(err)
		}
		if page.TotalRecordCount != 1 || len(page.Items) != 1 || page.Items[0].Id != "m0700" {
			t.Fatal(path, page)
		}
	}
	if err := json.Unmarshal(request("/Items?StartIndex=9000&Limit=5", tokens[0]), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.TotalRecordCount != 1203 || page.StartIndex != 9000 {
		t.Fatal(page)
	}
}
