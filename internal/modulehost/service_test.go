package modulehost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

func TestModuleHostNamespacesAuthenticationAndInvocation(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := module.NewRegistry()
	moduleHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/caps" {
			_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportsSearch: true})
			return
		}
		if r.URL.Path == "/api/v1/search" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []module.MediaSummary{{Title: "Called through host", SourceID: "42", ItemType: module.ItemMovie}}, "total": 1})
			return
		}
		http.Error(w, "private upstream message", 500)
	}))
	defer moduleHTTP.Close()
	target := &module.Manifest{ID: "target-module", Name: "Target", Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: moduleHTTP.URL}}
	client, err := module.NewHTTPModuleClient(target)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := registry.Register(&module.ModuleEntry{Manifest: target, Status: module.StatusActive, MetadataInstance: client}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"caller-one", "caller-two", "unprivileged"} {
		permissions := &module.HostPermissions{Storage: id != "unprivileged", Invoke: []module.InvokePermission{{ModuleID: "target-module", Methods: []string{"search", "detail"}}}}
		if err := registry.Register(&module.ModuleEntry{Manifest: &module.Manifest{ID: id, Name: id, Version: "1", Type: module.TypeMetadata, Permissions: permissions}, Status: module.StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(db, registry, strings.Repeat("test-key", 8), "http://127.0.0.1:18098")
	router := chi.NewRouter()
	s.RegisterRoutes(router)
	request := func(id, method, path, body, token, callTicket string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, "/module-host/"+id+path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("X-JMlab-Call-Ticket", callTicket)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %d: %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	headers := s.headers(ctx, "caller-one")
	token, ticket := headers["X-JMlab-Host-Token"], headers["X-JMlab-Call-Ticket"]
	request("caller-one", "GET", "/storage", "", "", "", 401)
	request("caller-one", "GET", "/storage", "", "user-session-token", "", 401)
	request("caller-two", "GET", "/storage", "", token, "", 401)
	request("caller-one", "PUT", "/storage/auth", `{"value":{"secret":"storage-secret"}}`, token, "", 200)
	if response := request("caller-one", "GET", "/storage/auth", "", token, "", 200); !strings.Contains(response, "storage-secret") {
		t.Fatal(response)
	}
	var encrypted string
	if err := db.DB().QueryRow("SELECT value_encrypted FROM module_storage WHERE module_id='caller-one'").Scan(&encrypted); err != nil || strings.Contains(encrypted, "storage-secret") {
		t.Fatal("unencrypted storage", err)
	}
	otherToken := s.headers(ctx, "caller-two")["X-JMlab-Host-Token"]
	request("caller-two", "GET", "/storage/auth", "", otherToken, "", 404)
	request("unprivileged", "GET", "/storage", "", s.headers(ctx, "unprivileged")["X-JMlab-Host-Token"], "", 403)
	request("caller-one", "PUT", "/storage/too-big", `{"value":"`+strings.Repeat("x", 17*1024)+`"}`, token, "", 400)
	request("caller-one", "POST", "/invoke", `{"moduleId":"target-module","method":"search","payload":{"query":"test","itemType":"movie"}}`, token, "", 401)
	request("caller-one", "POST", "/invoke", `{"moduleId":"target-module","method":"caps"}`, token, ticket, 403)
	if result := request("caller-one", "POST", "/invoke", `{"moduleId":"target-module","method":"search","payload":{"query":"test","itemType":"movie"}}`, token, ticket, 200); !strings.Contains(result, "Called through host") {
		t.Fatal(result)
	}
	response := request("caller-one", "POST", "/invoke", `{"moduleId":"target-module","method":"detail","payload":{"sourceId":"42"}}`, token, ticket, 502)
	if strings.Contains(response, "private upstream") {
		t.Fatal("upstream error leaked")
	}
	cycleTicket := s.headers(module.WithCallChain(ctx, []string{"target-module"}), "caller-one")["X-JMlab-Call-Ticket"]
	request("caller-one", "POST", "/invoke", `{"moduleId":"target-module","method":"search"}`, token, cycleTicket, 409)
	deepTicket := s.headers(module.WithCallChain(ctx, []string{"hop-a", "hop-b", "hop-c"}), "caller-one")["X-JMlab-Call-Ticket"]
	request("caller-one", "POST", "/invoke", `{"moduleId":"target-module","method":"search"}`, token, deepTicket, 409)
	entry, _ := registry.Get("caller-one")
	if err := registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	request("caller-one", "GET", "/storage", "", token, "", 401)
	freshToken := s.headers(ctx, "caller-one")["X-JMlab-Host-Token"]
	request("caller-one", "DELETE", "/storage/auth", "", freshToken, "", 200)
	if err := registry.SetStatus("caller-one", module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	request("caller-one", "GET", "/storage", "", freshToken, "", 401)
}
