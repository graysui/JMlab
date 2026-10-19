package module_test

import (
	"context"
	"encoding/json"
	"github.com/graysui/JMlab/internal/module"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEndToEndModuleManager(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportsSearch: true, SupportsRankings: true, SupportsStreams: true})
	}))
	defer upstream.Close()
	mgr := module.NewManager(t.TempDir(), nil)
	defer mgr.Stop(context.Background())
	manifest := &module.Manifest{ID: "test-composite", Name: "Test composite", Version: "1", Type: module.TypeComposite, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: upstream.URL}}
	if err := mgr.Install(context.Background(), manifest, module.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	metas, streams := mgr.Registry().ListActiveMetadataProviders(), mgr.Registry().ListActiveStreamProviders()
	if len(metas) != 1 || len(streams) != 1 || metas[0].ModuleID() != manifest.ID || streams[0].ModuleID() != manifest.ID {
		t.Fatal("composite capabilities missing")
	}
	caps, err := metas[0].Caps(context.Background())
	if err != nil || !caps.SupportsSearch || !caps.SupportsRankings {
		t.Fatalf("caps: %+v %v", caps, err)
	}
	received := make(chan bool, 1)
	mgr.EventBus().Subscribe("media.item.created", func(context.Context, module.Event) error { received <- true; return nil })
	mgr.EventBus().Publish(context.Background(), "media.item.created", "test-event", `{"title":"Test movie"}`)
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
	if err := mgr.DisableModule(manifest.ID); err != nil {
		t.Fatal(err)
	}
	if len(mgr.Registry().ListActiveMetadataProviders()) != 0 || len(mgr.Registry().ListActiveStreamProviders()) != 0 {
		t.Fatal("disabled module still active")
	}
}
