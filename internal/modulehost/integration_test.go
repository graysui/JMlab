package modulehost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

func TestManagerCallbacksAndReplacementCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "host.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := module.NewManager(t.TempDir(), nil)
	defer manager.Stop(ctx)
	s := NewService(db, manager.Registry(), strings.Repeat("key", 16), "")
	router := chi.NewRouter()
	router.Get("/health/live", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	router.Route("/api/v1", s.RegisterRoutes)
	host := httptest.NewServer(router)
	defer host.Close()
	s.publicURL = host.URL
	manager.SetHostBridge(s)
	client := &http.Client{Timeout: 8 * time.Second}
	callback := func(r *http.Request, method, path, body string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(r.Context(), method, r.Header.Get("X-JMlab-Host-URL")+path, strings.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+r.Header.Get("X-JMlab-Host-Token"))
		req.Header.Set("X-JMlab-Call-Ticket", r.Header.Get("X-JMlab-Call-Ticket"))
		response, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		return response.StatusCode, data, err
	}
	seen := make(chan http.Header, 8)
	slowStarted, slowCancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	targetHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/caps" {
			if r.Header.Get("X-JMlab-Host-Token") != "" {
				t.Error("preflight received live credentials")
			}
			_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportsSearch: true})
			return
		}
		var input struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Query == "slow" {
			slowStarted <- struct{}{}
			<-r.Context().Done()
			slowCancelled <- struct{}{}
			return
		}
		entry, _ := manager.Registry().Get("target-module")
		claims, err := s.parseTicket(r.Header.Get("X-JMlab-Call-Ticket"), entry)
		if err != nil || strings.Join(claims.Chain, "/") != "caller-module/target-module" {
			t.Errorf("chain not propagated: %+v %v", claims, err)
			w.WriteHeader(500)
			return
		}
		status, _, err := callback(r, "POST", "/invoke", `{"moduleId":"caller-module","method":"search","payload":{"query":"cycle"}}`)
		if err != nil || status != 409 {
			t.Errorf("cycle accepted: %d %v", status, err)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []module.MediaSummary{{Title: "真实反向调用结果", SourceID: "42", ItemType: module.ItemMovie}}, "total": 1})
	}))
	defer targetHTTP.Close()
	sourceHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/caps" {
			if r.Header.Get("X-JMlab-Host-Token") != "" {
				t.Error("replacement preflight received live credentials")
			}
			_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportsSearch: true})
			return
		}
		seen <- r.Header.Clone()
		if r.Header.Get("X-JMlab-Host-Token") == "" {
			w.WriteHeader(401)
			return
		}
		status, _, err := callback(r, "PUT", "/storage/cursor", `{"value":{"page":2}}`)
		if err != nil || status != 200 {
			t.Errorf("storage callback: %d %v", status, err)
			w.WriteHeader(500)
			return
		}
		status, data, err := callback(r, "POST", "/invoke", `{"moduleId":"target-module","method":"search","payload":{"query":"example","itemType":"movie"}}`)
		if err != nil || status != 200 {
			t.Errorf("invoke callback: %d %s %v", status, data, err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer sourceHTTP.Close()
	for _, definition := range []struct{ id, url, target string }{{"target-module", targetHTTP.URL, "caller-module"}, {"caller-module", sourceHTTP.URL, "target-module"}} {
		manifest := &module.Manifest{ID: definition.id, Name: definition.id, Version: "1", Type: module.TypeMetadata,
			Transport:   module.TransportConfig{Type: module.TransportHTTP, URL: definition.url},
			Permissions: &module.HostPermissions{Storage: true, Invoke: []module.InvokePermission{{ModuleID: definition.target, Methods: []string{"search"}}}}}
		if err := manager.Install(ctx, manifest, module.Settings{Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	search := func(entry *module.ModuleEntry) {
		t.Helper()
		items, total, err := entry.MetadataInstance.Search(ctx, "example", module.ItemMovie, 1, 20)
		if err != nil || total != 1 || len(items) != 1 || items[0].Title != "真实反向调用结果" {
			t.Fatalf("end-to-end search: %+v %d %v", items, total, err)
		}
	}
	old, _ := manager.Registry().Get("caller-module")
	search(old)
	oldHeaders := <-seen
	if err := manager.UpdateSettings(ctx, "caller-module", module.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := manager.Registry().Get("caller-module")
	search(fresh)
	freshHeaders := <-seen
	if oldHeaders.Get("X-JMlab-Host-Token") == freshHeaders.Get("X-JMlab-Host-Token") {
		t.Fatal("replacement did not revoke credentials")
	}
	if _, _, err := old.MetadataInstance.Search(ctx, "stale", module.ItemMovie, 1, 20); err == nil {
		t.Fatal("old client received replacement credentials")
	}
	if header := <-seen; header.Get("X-JMlab-Host-Token") != "" {
		t.Fatal("stale client was issued credentials")
	}
	status, _, err := callback(httptest.NewRequest("GET", "/", nil).WithContext(ctx), "GET", "/storage", "")
	if err == nil || status != 0 {
		t.Fatal("missing callback URL unexpectedly usable")
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header = oldHeaders
	status, _, err = callback(request, "GET", "/storage", "")
	if err != nil || status != 401 {
		t.Fatal("old token accepted", status, err)
	}
	// A slow target consumes only this invocation; independent host routes remain responsive.
	request.Header = freshHeaders
	type result struct {
		status int
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		status, _, err := callback(request, "POST", "/invoke", `{"moduleId":"target-module","method":"search","payload":{"query":"slow"}}`)
		completed <- result{status, err}
	}()
	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow target not reached")
	}
	healthClient := &http.Client{Timeout: time.Second}
	response, err := healthClient.Get(host.URL + "/health/live")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("health blocked")
	}
	select {
	case got := <-completed:
		if got.err != nil || got.status != 502 {
			t.Fatal(got)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("invoke timeout not enforced")
	}
	select {
	case <-slowCancelled:
	case <-time.After(time.Second):
		t.Fatal("target context was not cancelled")
	}
	if err := manager.DisableModule("caller-module"); err != nil {
		t.Fatal(err)
	}
	status, _, err = callback(request, "GET", "/storage", "")
	if err != nil || status != 401 {
		t.Fatal("disabled token accepted", status, err)
	}
	if err := manager.EnableModule("caller-module"); err != nil {
		t.Fatal(err)
	}
	entry, _ := manager.Registry().Get("caller-module")
	search(entry)
	<-seen
	status, _, err = callback(request, "GET", "/storage", "")
	if err != nil || status != 401 {
		t.Fatal("reenable resurrected old token", status, err)
	}
}
