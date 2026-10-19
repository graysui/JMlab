package moduleadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/securevalue"
)

func TestPlaybackScopeAPIPersistenceRestoreAndLoopPrevention(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "scope.db")), "../../migrations", nil)
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportsStreams: true})
	}))
	defer upstream.Close()
	dir := t.TempDir()
	manager := module.NewManager(dir, nil)
	t.Cleanup(func() { _ = manager.Stop(ctx) })
	if err := manager.SetStateStore(ctx, NewStore(db, "local-scope-test")); err != nil {
		t.Fatal(err)
	}
	manifest := &module.Manifest{ID: "custom-stream", Name: "第三方播放源", Version: "1", Type: module.TypeStream, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: upstream.URL}}
	if err := manager.Install(ctx, manifest, module.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	nativeManifest := &module.Manifest{ID: "native-stream", Name: "内置播放源", Version: "1", Type: module.TypeStream}
	if err := manager.RegisterInProcessModule(nativeManifest, nil, nil); err != nil {
		t.Fatal(err)
	}
	for id, filter := range map[string]string{"content": `{"years":[2024]}`, "playable": `{"hasPlayableSource":true}`, "favorite": `{"not":{"isFavorite":false}}`} {
		if _, err := db.DB().Exec(`INSERT INTO virtual_libraries(id,name,item_type,filter_json,sort_json,is_public,is_adult,enabled,created_at,updated_at) VALUES (?,?,'mixed',?,'{}',1,0,1,'now','now')`, id, id, filter); err != nil {
			t.Fatal(err)
		}
	}
	a := auth.NewService(db, config.Config{}, nil)
	c := catalog.NewService(db, a, nil)
	c.SetModuleRegistry(manager.Registry())
	s := NewService(db, a, manager)
	s.SetCatalog(c)
	router := chi.NewRouter()
	s.RegisterRoutes(router)
	c.RegisterRoutes(router)
	request := func(method, path, body string, status int, authorized bool) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if authorized {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	request("GET", "/admin/modules/playback-libraries", "", 401, false)
	body := request("GET", "/admin/modules/playback-libraries", "", 200, true)
	if !strings.Contains(body, `"selectable":false`) {
		t.Fatal(body)
	}
	for _, body := range []string{`{"libraryScope":{"mode":"selected","libraryIds":["playable"]}}`, `{"libraryScope":{"mode":"selected","libraryIds":["favorite"]}}`, `{"libraryScope":{"mode":"selected","libraryIds":["missing"]}}`, `{"libraryScope":{"mode":"all","libraryIds":["content"]}}`} {
		request("PATCH", "/admin/modules/custom-stream", body, 400, true)
	}
	request("PATCH", "/admin/modules/custom-stream", `{"libraryScope":{"mode":"selected","libraryIds":["content"]}}`, 200, true)
	request("PATCH", "/admin/modules/native-stream", `{"libraryScope":{"mode":"selected","libraryIds":["content"]}}`, 200, true)
	entry, _ := manager.Registry().Get(manifest.ID)
	if entry.LibraryScope == nil || len(entry.LibraryScope.LibraryIDs) != 1 {
		t.Fatal(entry.LibraryScope)
	}
	request("PATCH", "/libraries/content", `{"filter":{"streamModuleId":"custom-stream"}}`, 400, true)
	body = request("GET", "/admin/modules", "", 200, true)
	if !strings.Contains(body, `"libraryScopeNames":["content"]`) {
		t.Fatal(body)
	}
	var snapshot []module.RestoreRecord
	if err := manager.WithBackupSnapshot(func(records []module.RestoreRecord) error { snapshot = records; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 || snapshot[0].Settings.LibraryScope == nil || snapshot[1].Settings.LibraryScope == nil {
		t.Fatal("scope omitted from backup")
	}
	request("PATCH", "/admin/modules/custom-stream", `{"libraryScope":{"mode":"selected","libraryIds":[]}}`, 200, true)
	request("PATCH", "/admin/modules/custom-stream", `{"libraryScope":{"mode":"all","libraryIds":[]}}`, 200, true)
	request("PATCH", "/admin/modules/native-stream", `{"libraryScope":{"mode":"all","libraryIds":[]}}`, 200, true)
	_, err = manager.CommitRestore(ctx, func() error {
		for _, record := range snapshot {
			plain, err := json.Marshal(record)
			if err != nil {
				return err
			}
			encrypted, err := securevalue.Encrypt("local-scope-test:module-restore:"+record.ID, plain)
			if err != nil {
				return err
			}
			if _, err = db.DB().Exec("INSERT INTO module_restore_queue(module_id,record_encrypted,updated_at) VALUES (?,?,'now')", record.ID, encrypted); err != nil {
				return err
			}
			if err := NewStore(db, "local-scope-test").SaveRestore(ctx, record); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ = manager.Registry().Get(manifest.ID)
	if entry.LibraryScope == nil || entry.LibraryScope.Mode != "selected" || entry.LibraryScope.LibraryIDs[0] != "content" {
		t.Fatal("scope not restored")
	}
	nativeEntry, _ := manager.Registry().Get(nativeManifest.ID)
	if nativeEntry.LibraryScope == nil || nativeEntry.LibraryScope.Mode != "selected" || nativeEntry.LibraryScope.LibraryIDs[0] != "content" {
		t.Fatal("native scope not restored")
	}
	restarted := module.NewManager(dir, nil)
	defer restarted.Stop(ctx)
	if err := restarted.SetStateStore(ctx, NewStore(db, "local-scope-test")); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RegisterInProcessModule(nativeManifest, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := restarted.DiscoverAndLoad(ctx); err != nil {
		t.Fatal(err)
	}
	entry, _ = restarted.Registry().Get(manifest.ID)
	if entry == nil || entry.LibraryScope == nil || entry.LibraryScope.LibraryIDs[0] != "content" {
		t.Fatal("scope not persisted across restart")
	}
	nativeEntry, _ = restarted.Registry().Get(nativeManifest.ID)
	if nativeEntry.LibraryScope == nil || nativeEntry.LibraryScope.LibraryIDs[0] != "content" {
		t.Fatal("native scope not persisted")
	}
	request("DELETE", "/libraries/content", "", 200, true)
	request("PATCH", "/admin/modules/custom-stream", `{"enabled":false}`, 200, true)
}
