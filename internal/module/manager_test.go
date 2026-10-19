package module

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestManifestValidation(t *testing.T) {
	valid := &Manifest{
		ID:      "provider-tmdb",
		Name:    "TMDB Provider",
		Version: "1.0.0",
		Type:    TypeMetadata,
		Transport: TransportConfig{
			Type: TransportHTTP,
			URL:  "http://127.0.0.1:8080",
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid manifest, got: %v", err)
	}

	invalidID := *valid
	invalidID.ID = "INVALID_ID!"
	if err := invalidID.Validate(); err == nil {
		t.Fatalf("expected error on invalid ID")
	}

	missingName := *valid
	missingName.Name = ""
	if err := missingName.Validate(); err == nil {
		t.Fatalf("expected error on missing Name")
	}
}

func TestEventBusPublishSubscribe(t *testing.T) {
	bus := NewEventBus(nil)
	var called int32

	bus.Subscribe("media.item.created", func(ctx context.Context, e Event) error {
		if e.Topic != "media.item.created" {
			t.Errorf("expected topic media.item.created, got %s", e.Topic)
		}
		if e.EventID != "evt-123" {
			t.Errorf("expected eventID evt-123, got %s", e.EventID)
		}
		atomic.AddInt32(&called, 1)
		return nil
	})

	bus.Publish(context.Background(), "media.item.created", "evt-123", `{"id":"item-1"}`)

	// 等待异步派发
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("expected event handler to be called once, got %d", called)
	}
}

func TestHTTPModuleClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/caps":
			_ = json.NewEncoder(w).Encode(MetadataCaps{
				SupportedTypes: []ItemType{ItemMovie},
				SupportsSearch: true,
			})
		case "/api/v1/search":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []MediaSummary{
					{SourceID: "101", Title: "Test Movie", ItemType: ItemMovie},
				},
				"total": 1,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := &Manifest{
		ID:      "test-http-mod",
		Name:    "Test HTTP Module",
		Version: "1.0.0",
		Type:    TypeMetadata,
		Transport: TransportConfig{
			Type: TransportHTTP,
			URL:  server.URL,
		},
	}

	client, err := NewHTTPModuleClient(m)
	if err != nil {
		t.Fatalf("failed to create http client: %v", err)
	}

	caps, err := client.Caps(context.Background())
	if err != nil {
		t.Fatalf("Caps() failed: %v", err)
	}
	if !caps.SupportsSearch {
		t.Errorf("expected SupportsSearch to be true")
	}

	items, total, err := client.Search(context.Background(), "test", ItemMovie, 1, 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Title != "Test Movie" {
		t.Errorf("unexpected search result: total=%d, items=%+v", total, items)
	}
}

func TestRegistryAndManager(t *testing.T) {
	mgr := NewManager("", nil)
	manifest := &Manifest{
		ID:      "in-process-test",
		Name:    "In Process Provider",
		Version: "1.0.0",
		Type:    TypeMetadata,
		Transport: TransportConfig{
			Type: TransportInProcess,
		},
	}

	err := mgr.RegisterInProcessModule(manifest, nil, nil)
	if err != nil {
		t.Fatalf("RegisterInProcessModule failed: %v", err)
	}

	entry, ok := mgr.Registry().Get("in-process-test")
	if !ok || entry.Status != StatusActive {
		t.Fatalf("expected module to be registered and active")
	}

	if err := mgr.DisableModule("in-process-test"); err != nil {
		t.Fatalf("failed to disable module: %v", err)
	}

	entry, _ = mgr.Registry().Get("in-process-test")
	if entry.Status != StatusDisabled {
		t.Fatalf("expected module to be disabled")
	}
}
