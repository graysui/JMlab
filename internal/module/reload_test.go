package module

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTestManifest(t *testing.T, root string, manifest Manifest) string {
	t.Helper()
	dir := filepath.Join(root, "test-provider")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testModuleServer(t *testing.T, title string, entered, release chan struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/caps" {
			_ = json.NewEncoder(w).Encode(MetadataCaps{SupportedTypes: []ItemType{ItemMovie}, SupportsSearch: true})
			return
		}
		if r.URL.Path == "/api/v1/search" {
			if entered != nil {
				close(entered)
				<-release
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []MediaSummary{{SourceID: "one", Title: title, ItemType: ItemMovie}}, "total": 1})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHotReloadPreservesInflightAndRejectsBrokenReplacement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	v1 := testModuleServer(t, "version one", entered, release)
	v2 := testModuleServer(t, "version two", nil, nil)
	manifest := Manifest{ID: "test-provider", Name: "Test", Version: "1.0.0", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: v1.URL}}
	dir := writeTestManifest(t, root, manifest)
	mgr := NewManager(root, nil)
	if _, err := mgr.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	entry, _ := mgr.Registry().Get(manifest.ID)
	finished := make(chan string, 1)
	go func() {
		items, _, err := entry.MetadataInstance.Search(ctx, "test", ItemMovie, 1, 10)
		if err != nil {
			finished <- err.Error()
			return
		}
		finished <- items[0].Title
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old request did not enter")
	}
	manifest.Version, manifest.Transport.URL = "2.0.0", v2.URL
	writeTestManifest(t, root, manifest)
	report, err := mgr.Reload(ctx)
	close(release)
	if err != nil || len(report.Updated) != 1 {
		t.Fatalf("reload: %+v %v", report, err)
	}
	if result := <-finished; result != "version one" {
		t.Fatalf("in-flight interrupted: %s", result)
	}
	checkCurrent := func() {
		t.Helper()
		entry, ok := mgr.Registry().Get(manifest.ID)
		if !ok || entry.Manifest.Version != "2.0.0" {
			t.Fatal("last working version lost")
		}
		items, _, err := entry.MetadataInstance.Search(ctx, "test", ItemMovie, 1, 10)
		if err != nil || len(items) != 1 || items[0].Title != "version two" {
			t.Fatalf("search: %+v %v", items, err)
		}
	}
	checkCurrent()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if report, err := mgr.Reload(ctx); err != nil || len(report.Errors) == 0 {
		t.Fatal("invalid replacement not reported")
	}
	checkCurrent()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "secret-upstream-error", 503) }))
	defer bad.Close()
	manifest.Version, manifest.Transport.URL = "3.0.0", bad.URL
	writeTestManifest(t, root, manifest)
	if report, err := mgr.Reload(ctx); err != nil || len(report.Errors) == 0 {
		t.Fatal("unhealthy replacement accepted")
	}
	checkCurrent()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if report, err := mgr.Reload(ctx); err != nil || len(report.Removed) != 1 {
		t.Fatalf("remove: %+v %v", report, err)
	}
	if _, ok := mgr.Registry().Get(manifest.ID); ok {
		t.Fatal("removed module still registered")
	}
}

func TestWatcherAutomaticallyLoadsUpdatesAndKeepsDisabledState(t *testing.T) {
	root := t.TempDir()
	server := testModuleServer(t, "watched", nil, nil)
	mgr := NewManager(root, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.StartWatcher(ctx, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := mgr.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	manifest := Manifest{ID: "test-provider", Name: "Test", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: server.URL}}
	writeTestManifest(t, root, manifest)
	await := func(version string) {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-deadline.C:
				t.Fatalf("watcher did not load version %s", version)
			case <-tick.C:
				if entry, ok := mgr.Registry().Get(manifest.ID); ok && entry.Manifest.Version == version {
					return
				}
			}
		}
	}
	await("1")
	if err := mgr.DisableModule(manifest.ID); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "2"
	writeTestManifest(t, root, manifest)
	await("2")
	if len(mgr.Registry().ListActiveMetadataProviders()) != 0 {
		t.Fatal("reload re-enabled a disabled module")
	}
	if err := mgr.EnableModule(manifest.ID); err != nil {
		t.Fatal(err)
	}
	if len(mgr.Registry().ListActiveMetadataProviders()) != 1 {
		t.Fatal("enable failed")
	}
	// Concurrent readers receive independent snapshots during live configuration changes.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				entries := mgr.Registry().List()
				entries[0].Manifest.Name = "private snapshot"
			}
		}()
	}
	for j := 0; j < 10; j++ {
		_ = mgr.DisableModule(manifest.ID)
		_ = mgr.EnableModule(manifest.ID)
	}
	wg.Wait()
	entry, _ := mgr.Registry().Get(manifest.ID)
	if entry.Manifest.Name != "Test" {
		t.Fatal("external mutation changed registry")
	}
}

func TestModuleHTTPRejectsRedirectAndHidesErrorBody(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/caps" {
			http.Redirect(w, r, target.URL, 302)
			return
		}
		http.Error(w, "private-secret-token", 500)
	}))
	defer server.Close()
	client, err := NewHTTPModuleClientWithConfig(&Manifest{ID: "test-provider", Name: "Test", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: server.URL}}, map[string]any{"password": "private"}, "credential")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Caps(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("module credential request followed redirect")
	}
	_, _, err = client.Search(context.Background(), "test", ItemMovie, 1, 10)
	if err == nil || strings.Contains(err.Error(), "private-secret-token") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestConfigValidationRejectsNonScalarWithoutPanic(t *testing.T) {
	manifest := &Manifest{ConfigSchema: map[string]ConfigField{"key": {Type: "password", Required: true, Label: "API Key"}}}
	for _, value := range []any{map[string]any{"bad": "value"}, []any{"bad"}, true, "   "} {
		if _, err := ValidateConfig(manifest, map[string]any{"key": value}); err == nil {
			t.Fatalf("invalid config accepted: %T", value)
		}
	}
}
