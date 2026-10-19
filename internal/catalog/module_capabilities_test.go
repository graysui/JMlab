package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/module"
)

func TestMetadataSearchHonorsCapabilitiesAndExplicitSourceIdentity(t *testing.T) {
	registry := module.NewRegistry()
	for _, id := range []string{"searchable-source", "detail-only-source"} {
		manifest := &module.Manifest{ID: id, Name: id, Version: "1", Type: module.TypeMetadata, SupportedItemTypes: []module.ItemType{module.ItemMovie, module.ItemSeries}, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: "https://example.test"}}
		client, err := module.NewHTTPModuleClient(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err = registry.Register(&module.ModuleEntry{Manifest: manifest, Status: module.StatusActive, MetadataInstance: client, Capabilities: &module.MetadataCaps{SupportsSearch: id == "searchable-source", SupportedTypes: []module.ItemType{module.ItemMovie}}}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(nil, nil, nil)
	s.SetModuleRegistry(registry)
	router := chi.NewRouter()
	router.Get("/modules", s.metadataModules)
	router.Get("/{moduleID}/search", s.searchMetadata)
	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/modules", nil))
	var response struct {
		Items []struct {
			ID    string            `json:"id"`
			Types []module.ItemType `json:"supportedItemTypes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].ID != "searchable-source" || len(response.Items[0].Types) != 1 || response.Items[0].Types[0] != module.ItemMovie {
		t.Fatalf("unsupported search capability exposed: %s", list.Body.String())
	}
	for _, test := range []struct {
		path   string
		status int
		code   string
	}{
		{"/detail-only-source/search?q=test", 400, "SEARCH_UNSUPPORTED"},
		{"/searchable-source/search?q=test&itemType=series", 400, "INVALID_SEARCH"},
		{"/missing-source/search?q=test", 404, "METADATA_UNAVAILABLE"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, test.path, nil))
		if rec.Code != test.status || !strings.Contains(rec.Body.String(), test.code) {
			t.Fatalf("%s: %d %s", test.path, rec.Code, rec.Body.String())
		}
	}
	if err := registry.SetStatus("searchable-source", module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	list = httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/modules", nil))
	if !strings.Contains(list.Body.String(), `"items":[]`) {
		t.Fatalf("disabled source remained searchable: %s", list.Body.String())
	}
}
