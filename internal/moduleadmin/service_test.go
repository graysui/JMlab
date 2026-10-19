package moduleadmin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestModuleAdministrationPersistenceAndPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authService := auth.NewService(db, config.Config{}, nil)
	authStore := auth.NewStore(db)
	newToken := func(role string) string {
		t.Helper()
		user, err := authStore.CreateUser(ctx, strings.ToLower(role), role, "unused", role)
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := authStore.CreateSession(ctx, user, "test", "test", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	adminToken, viewerToken := newToken("Admin"), newToken("Viewer")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer module-secret" {
			http.Error(w, "unauthorized", 401)
			return
		}
		encoded, err := base64.StdEncoding.DecodeString(r.Header.Get("X-JMlab-Module-Config"))
		var cfg map[string]any
		if err != nil || json.Unmarshal(encoded, &cfg) != nil || cfg["apiKey"] != "api-secret" {
			http.Error(w, "private-upstream-message", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportedTypes: []module.ItemType{module.ItemMovie}, SupportsSearch: true})
	}))
	defer upstream.Close()
	root := t.TempDir()
	masterKey := strings.Repeat("test-key", 8)
	mgr := module.NewManager(root, nil)
	store := NewStore(db, masterKey)
	if err := mgr.SetStateStore(ctx, store); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	NewService(db, authService, mgr).RegisterRoutes(router)
	request := func(method, path, token, body string, cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie {
			req.AddCookie(&http.Cookie{Name: "jmlab_session", Value: token})
		} else if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {viewerToken, 403}} {
		if rec := request("GET", "/admin/modules", tc.token, "", false); rec.Code != tc.want {
			t.Fatalf("ACL: %d %s", rec.Code, rec.Body.String())
		}
	}
	manifest := module.Manifest{ID: "test-provider", Name: "Test provider", Version: "1", Type: module.TypeMetadata,
		Transport:    module.TransportConfig{Type: module.TransportHTTP, URL: upstream.URL},
		ConfigSchema: map[string]module.ConfigField{"apiKey": {Type: "password", Secret: true, Required: true, Label: "API Key"}, "language": {Type: "string", Default: "zh-CN"}}}
	encoded, _ := json.Marshal(map[string]any{"manifest": manifest, "enabled": true, "config": map[string]any{"apiKey": "api-secret"}, "token": "module-secret"})
	if rec := request("POST", "/admin/modules", viewerToken, string(encoded), false); rec.Code != 403 {
		t.Fatalf("viewer install: %d", rec.Code)
	}
	if rec := request("POST", "/admin/modules", adminToken, string(encoded), true); rec.Code != 403 {
		t.Fatalf("missing CSRF accepted: %d", rec.Code)
	}
	if rec := request("POST", "/admin/modules", adminToken, string(encoded), false); rec.Code != 201 {
		t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
	}
	checkSecrets := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != 200 {
			t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
		}
		for _, secret := range []string{"api-secret", "module-secret", "private-upstream-message"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("secret leaked: %s", rec.Body.String())
			}
		}
	}
	checkSecrets(request("GET", "/admin/modules", adminToken, "", false))
	if rec := request("GET", "/admin/modules/templates", adminToken, "", false); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("templates: %d %s", rec.Code, rec.Body.String())
	}
	if rec := request("GET", "/admin/modules/templates", viewerToken, "", false); rec.Code != 403 {
		t.Fatal("viewer accessed templates")
	}
	// Connection and media authorization are replaced together with configuration.
	connection, _ := json.Marshal(map[string]any{"transport": manifest.Transport, "mediaAccess": module.MediaAccess{AllowedOrigins: []string{"http://127.0.0.1:5245"}, AllowPrivate: true}, "config": map[string]any{"language": "en-US"}})
	checkSecrets(request("PATCH", "/admin/modules/test-provider", adminToken, string(connection), false))
	entryAfterUpdate, _ := mgr.Registry().Get(manifest.ID)
	revision := entryAfterUpdate.Revision
	badConnection := `{"transport":{"type":"http","url":"http://127.0.0.1:1"},"config":{"language":"bad"}}`
	if rec := request("PATCH", "/admin/modules/test-provider", adminToken, badConnection, false); rec.Code != 400 {
		t.Fatal("bad connection accepted")
	}
	entryAfterFailure, _ := mgr.Registry().Get(manifest.ID)
	if entryAfterFailure.Revision != revision || mgr.Settings(manifest.ID).Config["language"] != "en-US" {
		t.Fatal("failed replacement changed live module")
	}
	report, err := mgr.Reload(ctx)
	if err != nil || len(report.Updated) != 0 || len(report.Errors) != 0 {
		t.Fatalf("watcher replaced saved update: %+v %v", report, err)
	}
	var encrypted string
	if err := db.DB().QueryRow("SELECT config_encrypted FROM installed_modules WHERE module_id = ?", manifest.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "api-secret") || strings.Contains(encrypted, "module-secret") {
		t.Fatal("configuration stored as plaintext")
	}
	file, err := os.ReadFile(filepath.Join(root, manifest.ID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "api-secret") || strings.Contains(string(file), "module-secret") {
		t.Fatal("manifest stores credentials")
	}
	// Blank password inputs preserve existing secrets. Bad replacements leave live and saved settings intact.
	checkSecrets(request("PATCH", "/admin/modules/test-provider", adminToken, `{"config":{"apiKey":""},"token":""}`, false))
	if rec := request("PATCH", "/admin/modules/test-provider", adminToken, `{"config":{"apiKey":"bad-value"}}`, false); rec.Code != 400 || strings.Contains(rec.Body.String(), "private-upstream-message") {
		t.Fatalf("bad update: %d %s", rec.Code, rec.Body.String())
	}
	if state := mgr.Settings(manifest.ID); state.Config["apiKey"] != "api-secret" {
		t.Fatal("failed update changed runtime")
	}
	loaded, err := store.Load(ctx)
	if err != nil || loaded[manifest.ID].Config["apiKey"] != "api-secret" {
		t.Fatal("failed update changed persisted config")
	}
	checkSecrets(request("PATCH", "/admin/modules/test-provider", adminToken, `{"enabled":false}`, false))
	// A fresh manager reconstructs installed modules and disabled state without contacting the stopped service.
	restarted := module.NewManager(root, nil)
	if err := restarted.SetStateStore(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := restarted.DiscoverAndLoad(ctx); err != nil {
		t.Fatal(err)
	}
	entry, ok := restarted.Registry().Get(manifest.ID)
	if !ok || entry.Status != module.StatusDisabled {
		t.Fatal("disabled state lost on restart")
	}
	if err := restarted.EnableModule(manifest.ID); err != nil {
		t.Fatal(err)
	}
	if state := restarted.Settings(manifest.ID); state.Token != "module-secret" || state.Config["apiKey"] != "api-secret" {
		t.Fatal("credentials lost on restart")
	}
	if _, err := NewStore(db, "incorrect-key").Load(ctx); err == nil {
		t.Fatal("wrong encryption key accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "test-provider", "manifest.json")); err != nil {
		t.Fatal("installed manifest missing")
	}
	checkSecrets(request("PATCH", "/admin/modules/test-provider", adminToken, `{"enabled":true}`, false))
	manifest.Version = "2"
	definition, _ := json.Marshal(map[string]any{"manifest": manifest})
	for _, check := range []struct {
		token  string
		cookie bool
	}{{viewerToken, false}, {adminToken, true}} {
		if rec := request("PUT", "/admin/modules/test-provider/manifest", check.token, string(definition), check.cookie); rec.Code != 403 {
			t.Fatalf("manifest update ACL: %d", rec.Code)
		}
		if rec := request("DELETE", "/admin/modules/test-provider", check.token, "", check.cookie); rec.Code != 403 {
			t.Fatalf("uninstall ACL: %d", rec.Code)
		}
	}
	checkSecrets(request("PUT", "/admin/modules/test-provider/manifest", adminToken, string(definition), false))
	updated, _ := mgr.Registry().Get(manifest.ID)
	if updated.Manifest.Version != "2" || mgr.Settings(manifest.ID).Config["apiKey"] != "api-secret" {
		t.Fatal("manifest update lost version or credentials")
	}
	manifest.Transport.URL = "http://127.0.0.1:1"
	broken, _ := json.Marshal(map[string]any{"manifest": manifest})
	if rec := request("PUT", "/admin/modules/test-provider/manifest", adminToken, string(broken), false); rec.Code != 400 {
		t.Fatal("unreachable manifest replacement accepted")
	}
	unchanged, _ := mgr.Registry().Get(manifest.ID)
	if unchanged.Revision != updated.Revision {
		t.Fatal("failed update replaced working instance")
	}
	checkSecrets(request("DELETE", "/admin/modules/test-provider", adminToken, "", false))
	if _, exists := mgr.Registry().Get(manifest.ID); exists {
		t.Fatal("uninstalled module remains active")
	}
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded[manifest.ID]; exists {
		t.Fatal("uninstalled credentials retained in configuration store")
	}
	if _, err := os.Stat(filepath.Join(root, "test-provider")); !os.IsNotExist(err) {
		t.Fatal("managed manifest directory remains")
	}
	if report, err := mgr.Reload(ctx); err != nil || len(report.Errors) > 0 || len(mgr.Registry().List()) != 0 {
		t.Fatal("watcher reloaded an uninstalled module")
	}
	manifest.Transport.URL = upstream.URL
	if err := mgr.Install(ctx, &manifest, module.Settings{Enabled: true, Config: map[string]any{"apiKey": "api-secret"}, Token: "module-secret"}); err != nil {
		t.Fatalf("cannot reinstall: %v", err)
	}
}
