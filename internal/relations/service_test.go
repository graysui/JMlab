package relations

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

func TestUnifiedEntityManagementPreservesIdentityAndUserData(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "relations.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	u, err := store.CreateUser(ctx, "admin", "Admin", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, u, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.NewService(db, config.Config{}, nil)
	s := NewService(db, a)
	router := chi.NewRouter()
	s.RegisterRoutes(router)
	request := func(method, path, body string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		requestCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r.WithContext(requestCtx))
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.DB().Exec(db.Rebind(q), args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"people", "studios", "series", "tags"} {
		var created Entity
		if err := json.Unmarshal([]byte(request("POST", "/"+kind, `{"name":"手工条目"}`, 201)), &created); err != nil || created.ID == "" {
			t.Fatal(created, err)
		}
		request("PATCH", "/"+kind+"/"+created.ID, `{"name":"修改后的条目"}`, 200)
		body := request("GET", "/"+kind+"/"+created.ID, "", 200)
		if !strings.Contains(body, `"attributesLocked":true`) {
			t.Fatal(body)
		}
		request("POST", "/"+kind+"/batch", `{"ids":["`+created.ID+`"],"action":"exclude"}`, 200)
		body = request("GET", "/"+kind+"?includeExcluded=1", "", 200)
		if !strings.Contains(body, created.ID) || !strings.Contains(body, `"isExcluded":true`) {
			t.Fatal(body)
		}
	}
	for _, kind := range []string{"people", "studios"} {
		exec(`INSERT INTO entities(entity_kind,id,name,created_at,updated_at) VALUES (?,'source','Source','now','now'),(?,'target','Target','now','now')`, kind, kind)
	}
	exec(`INSERT INTO media_items(id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('movie','code','code','Movie','','now','now')`)
	exec(`INSERT INTO item_entities VALUES ('movie','people','source','actor',0),('movie','people','source','writer',1),('movie','people','target','director',2),('movie','studios','source','studio',0)`)
	exec(`INSERT INTO entity_identifiers VALUES ('people','source','module:test','person:1'),('people','target','module:test','person:2')`)
	exec(`INSERT INTO user_person_states(user_id,person_id,is_favorite,created_at,updated_at) VALUES (?,'source',1,'now','now'),(?,'target',0,'now','now')`, u.ID, u.ID)
	exec(`INSERT INTO user_entity_notes VALUES (?,'person','source','来源备注','now','now'),(?,'person','target','目标备注','now','now'),(?,'studio','source','公司备注','now','now')`, u.ID, u.ID, u.ID)
	request("POST", "/people/source/merge", `{"targetId":"target"}`, 200)
	var entity Entity
	if err := json.Unmarshal([]byte(request("GET", "/people/target", "", 200)), &entity); err != nil || len(entity.Identifiers) != 2 {
		t.Fatal(entity, err)
	}
	var count int
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM item_entities WHERE entity_kind='people' AND entity_id='target'`:  3,
		`SELECT COUNT(*) FROM item_entities WHERE entity_kind='studios' AND entity_id='source'`: 1,
		`SELECT COUNT(*) FROM entities WHERE entity_kind='people' AND id='source'`:              0,
		`SELECT COUNT(*) FROM entities WHERE entity_kind='studios' AND id='source'`:             1,
		`SELECT COUNT(*) FROM user_person_states WHERE person_id='target' AND is_favorite=1`:    1,
		`SELECT COUNT(*) FROM user_entity_notes WHERE entity_type='studio' AND note='公司备注'`:     1,
	} {
		if err := db.DB().QueryRow(q).Scan(&count); err != nil || count != want {
			t.Fatal(q, count, err)
		}
	}
	var note string
	if err := db.DB().QueryRow(`SELECT note FROM user_entity_notes WHERE entity_type='person' AND entity_id='target'`).Scan(&note); err != nil || note != "目标备注\n\n来源备注" {
		t.Fatal(note, err)
	}
	request("GET", "/people/source", "", 404)
}
