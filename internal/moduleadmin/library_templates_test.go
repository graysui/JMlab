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
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

func TestLibraryTemplatesFollowModuleLifecycleAndPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "templates.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	tokens := map[string]string{}
	for _, role := range []string{"Admin", "Editor", "Viewer"} {
		user, err := store.CreateUser(ctx, role, role, "unused", role)
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := store.CreateSession(ctx, user, "test", "test", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		tokens[role] = token
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportedTypes: []module.ItemType{module.ItemMovie}, SupportsSearch: true})
	}))
	defer upstream.Close()
	manager := module.NewManager(t.TempDir(), nil)
	manifest := &module.Manifest{ID: "template-source", Name: "模板模块", Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: upstream.URL}, LibraryTemplates: []module.LibraryTemplate{{ID: "all-items", Name: "模块作品", Scope: "metadata", ItemType: "movie", Sort: module.LibraryTemplateSort{Field: "updatedAt", Direction: "desc"}}}}
	if err := manager.Install(ctx, manifest, module.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	NewService(db, auth.NewService(db, config.Config{}, nil), manager).RegisterRoutes(router)
	get := func(path, token string, status int) string {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	for _, path := range []string{"/admin/modules/library-templates", "/admin/modules/template-source/library-templates", "/admin/modules/playback-libraries"} {
		get(path, "", 401)
		get(path, tokens["Viewer"], 403)
		get(path, tokens["Editor"], 403)
	}
	if body := get("/admin/modules/library-templates", tokens["Admin"], 200); !strings.Contains(body, `"templateCount":1`) {
		t.Fatal(body)
	}
	path := "/admin/modules/template-source/library-templates"
	body := get(path, tokens["Admin"], 200)
	if !strings.Contains(body, `"metadataModuleId":"template-source"`) {
		t.Fatal(body)
	}
	var old struct{ Revision string }
	_ = json.Unmarshal([]byte(body), &old)
	manifest.Version = "2"
	manifest.LibraryTemplates[0].Name = "新模板"
	if err := manager.UpdateManifest(ctx, manifest.ID, manifest); err != nil {
		t.Fatal(err)
	}
	body = get(path, tokens["Admin"], 200)
	var updated struct{ Revision string }
	_ = json.Unmarshal([]byte(body), &updated)
	if !strings.Contains(body, "新模板") || old.Revision == updated.Revision {
		t.Fatal(body)
	}
	manifest.LibraryTemplates[0].Sort.Field = "invalid"
	if err := manager.UpdateManifest(ctx, manifest.ID, manifest); err == nil {
		t.Fatal("invalid template accepted")
	}
	if body = get(path, tokens["Admin"], 200); !strings.Contains(body, "新模板") {
		t.Fatal(body)
	}
	if err := manager.Registry().SetStatus(manifest.ID, module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	get(path, tokens["Admin"], 409)
	manager.Registry().Remove(manifest.ID)
	get(path, tokens["Admin"], 404)
}
