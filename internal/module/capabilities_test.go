package module

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCapabilitiesPersistWithRegistrationAndAreImmutable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/caps" {
			t.Errorf("unexpected call: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(MetadataCaps{SupportedTypes: []ItemType{ItemMovie}, SupportsSearch: false})
	}))
	defer server.Close()
	manager := NewManager(t.TempDir(), nil)
	manifest := &Manifest{ID: "caps-metadata", Name: "Caps", Version: "1", Type: TypeMetadata, SupportedItemTypes: []ItemType{ItemMovie, ItemSeries}, Transport: TransportConfig{Type: TransportHTTP, URL: server.URL}}
	if err := manager.Install(context.Background(), manifest, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop(context.Background())
	entry, ok := manager.Registry().Get(manifest.ID)
	if !ok || entry.Capabilities == nil || entry.Capabilities.SupportsSearch {
		t.Fatal("preflight capability declaration was discarded")
	}
	if types := entry.SupportedTypes(); !reflect.DeepEqual(types, []ItemType{ItemMovie}) {
		t.Fatalf("manifest bypassed runtime types: %v", types)
	}
	entry.Capabilities.SupportedTypes[0] = ItemEpisode
	entry, _ = manager.Registry().Get(manifest.ID)
	if entry.Capabilities.SupportedTypes[0] != ItemMovie {
		t.Fatal("capability snapshot mutated registry")
	}
	for _, candidate := range manager.Registry().List() {
		candidate.Capabilities.SupportsSearch = true
	}
	entry, _ = manager.Registry().Get(manifest.ID)
	if entry.Capabilities.SupportsSearch {
		t.Fatal("list snapshot mutated registry")
	}
}

func TestSupportedTypeDeclarationsIntersectWithoutInventingCapabilities(t *testing.T) {
	for _, test := range []struct {
		name              string
		manifest, runtime []ItemType
		want              []ItemType
	}{
		{"legacy", []ItemType{ItemMovie}, nil, []ItemType{ItemMovie}},
		{"runtime-only", nil, []ItemType{ItemEpisode}, []ItemType{ItemEpisode}},
		{"intersection", []ItemType{ItemMovie, ItemSeries}, []ItemType{ItemSeries, ItemEpisode}, []ItemType{ItemSeries}},
		{"incompatible", []ItemType{ItemMovie}, []ItemType{ItemEpisode}, []ItemType{}},
		{"unspecified", nil, nil, []ItemType{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := &ModuleEntry{Manifest: &Manifest{SupportedItemTypes: test.manifest}, Capabilities: &MetadataCaps{SupportedTypes: test.runtime}}
			if got := entry.SupportedTypes(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}
