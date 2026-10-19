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

func TestRatingNullClearsOmittedPreservesAndOtherUsersStayIsolated(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "rating.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB().Exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('movie','code','','Movie','','now','now')`); err != nil {
		t.Fatal(err)
	}
	store := auth.NewStore(db)
	tokens := []string{}
	for _, name := range []string{"one", "two"} {
		u, err := store.CreateUser(ctx, name, name, "unused", "Admin")
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := store.CreateSession(ctx, u, "test", "test", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	router := chi.NewRouter()
	NewService(db, auth.NewService(db, config.Config{}, nil)).RegisterRoutes(router)
	request := func(token, body string, want int) ItemState {
		t.Helper()
		r := httptest.NewRequest("PUT", "/me/items/movie/state", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		var state ItemState
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
		}
		return state
	}
	s := request(tokens[0], `{"rating":8,"note":"保留备注"}`, 200)
	if s.UserRating == nil || *s.UserRating != 8 {
		t.Fatal(s)
	}
	s = request(tokens[0], `{"favorite":true}`, 200)
	if s.UserRating == nil || *s.UserRating != 8 || !s.IsFavorite {
		t.Fatal(s)
	}
	request(tokens[1], `{"rating":7}`, 200)
	s = request(tokens[0], `{"rating":null}`, 200)
	if s.UserRating != nil || s.Note != "保留备注" || !s.IsFavorite {
		t.Fatal(s)
	}
	s = request(tokens[1], `{"watched":true}`, 200)
	if s.UserRating == nil || *s.UserRating != 7 {
		t.Fatal("another user changed", s)
	}
	request(tokens[0], `{"rating":11}`, 400)
	request(tokens[0], `{"rating":2.5}`, 400)
	request(tokens[0], `{"unknown":1}`, 400)
	s = request(tokens[0], `{"rating":0}`, 200)
	if s.UserRating == nil || *s.UserRating != 0 {
		t.Fatal(s)
	}
}
