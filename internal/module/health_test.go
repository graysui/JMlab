package module

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func forceHealthCheck(client *HTTPModuleClient) {
	client.healthMu.Lock()
	client.health.NextCheckAt = time.Now().Add(-time.Second)
	if !client.openUntil.IsZero() {
		client.openUntil = time.Now().Add(-time.Second)
	}
	client.healthMu.Unlock()
}

func TestHealthCircuitIsolatesFailureAndRecoversWithoutRestart(t *testing.T) {
	ctx := context.Background()
	var failing atomic.Bool
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if failing.Load() {
			http.Error(w, "private-upstream-secret", 503)
			return
		}
		if r.URL.Path == "/api/v1/caps" {
			json.NewEncoder(w).Encode(MetadataCaps{SupportsSearch: true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []MediaSummary{{SourceID: "one", Title: "Working", ItemType: ItemMovie}}, "total": 1})
	}))
	defer server.Close()
	mgr := NewManager(t.TempDir(), nil)
	defer mgr.Stop(ctx)
	manifest := Manifest{ID: "health-provider", Name: "Health", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: server.URL}}
	if err := mgr.Install(ctx, &manifest, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	entry, _ := mgr.Registry().Get(manifest.ID)
	client := httpClient(entry)
	if entry.Health.State != "healthy" {
		t.Fatal(entry.Health)
	}
	failing.Store(true)
	for i := 0; i < 3; i++ {
		if _, _, err := client.Search(ctx, "test", ItemMovie, 1, 20); err == nil {
			t.Fatal("failing source returned success")
		}
		current, _ := mgr.Registry().Get(manifest.ID)
		if current.Health.ConsecutiveFailures != i+1 || (i < 2 && current.Status != StatusActive) {
			t.Fatal("failure state not tracked", current)
		}
	}
	opened, _ := mgr.Registry().Get(manifest.ID)
	if opened.Status != StatusError || opened.Health.State != "open" || strings.Contains(opened.LastError, "secret") || strings.Contains(opened.LastError, server.URL) {
		t.Fatal("circuit not open or leaked diagnostics", opened)
	}
	// More failures from the same already-running batch keep the first window.
	client.recordHealth(ctx, errors.New("late failure"))
	if client.Health().NextCheckAt != opened.Health.NextCheckAt {
		t.Fatal("in-flight failure extended open window")
	}
	before := requests.Load()
	for i := 0; i < 20; i++ {
		_, _, err := client.Search(ctx, "test", ItemMovie, 1, 20)
		if err == nil {
			t.Fatal("open circuit allowed request")
		}
	}
	mgr.CheckHealth(ctx)
	if requests.Load() != before {
		t.Fatal("open circuit bypassed cooldown")
	}
	// A healthy peer remains usable while this source is open.
	peerServer := testModuleServer(t, "Peer", nil, nil)
	peer := manifest
	peer.ID, peer.Transport.URL = "healthy-peer", peerServer.URL
	if err := mgr.Install(ctx, &peer, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	peers := mgr.Registry().ListActiveMetadataProviders()
	if len(peers) != 1 || peers[0].ModuleID() != peer.ID {
		t.Fatal("failed source blocked healthy peer", peers)
	}
	if items, _, err := peers[0].Search(ctx, "test", ItemMovie, 1, 20); err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	forceHealthCheck(client)
	mgr.CheckHealth(ctx)
	retried := client.Health()
	if retried.State != "open" || time.Until(retried.NextCheckAt) < 50*time.Second {
		t.Fatal("failed half-open probe did not back off", retried)
	}
	failing.Store(false)
	forceHealthCheck(client)
	mgr.CheckHealth(ctx)
	recovered, _ := mgr.Registry().Get(manifest.ID)
	if recovered.Status != StatusActive || recovered.Health.State != "healthy" || recovered.Health.ConsecutiveFailures != 0 || recovered.Revision == entry.Revision {
		t.Fatal("module did not recover and renew credentials", recovered)
	}
	if items, _, err := recovered.MetadataInstance.Search(ctx, "test", ItemMovie, 1, 20); err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if err := mgr.DisableModule(manifest.ID); err != nil {
		t.Fatal(err)
	}
	before = requests.Load()
	disabled, _ := mgr.Registry().Get(manifest.ID)
	forceHealthCheck(httpClient(disabled))
	mgr.CheckHealth(ctx)
	if requests.Load() != before {
		t.Fatal("disabled module was probed")
	}
}

func TestActiveHealthProbeDetectsIdleFailureAndNotificationErrorsAreSeparate(t *testing.T) {
	ctx := context.Background()
	var offline atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/detail" {
			http.NotFound(w, r)
			return
		}
		if offline.Load() || r.URL.Path == "/api/v1/events" {
			http.Error(w, "private notification failure", 503)
			return
		}
		json.NewEncoder(w).Encode(MetadataCaps{SupportsSearch: true})
	}))
	defer server.Close()
	mgr := NewManager(t.TempDir(), nil)
	defer mgr.Stop(ctx)
	mf := &Manifest{ID: "idle-provider", Name: "Idle", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: server.URL}}
	if err := mgr.Install(ctx, mf, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	entry, _ := mgr.Registry().Get(mf.ID)
	client := httpClient(entry)
	for i := 0; i < 4; i++ {
		if _, err := client.GetDetail(ctx, "missing", nil); err == nil {
			t.Fatal("missing content unexpectedly returned success")
		}
	}
	if client.Health().ConsecutiveFailures != 0 {
		t.Fatal("ordinary missing content opened connection circuit")
	}
	for i := 0; i < 4; i++ {
		if err := client.DeliverEvent(ctx, Event{}); err == nil {
			t.Fatal("notification unexpectedly succeeded")
		}
	}
	if client.Health().ConsecutiveFailures != 0 {
		t.Fatal("notification handler failure opened business circuit")
	}
	offline.Store(true)
	for i := 0; i < 3; i++ {
		forceHealthCheck(client)
		mgr.CheckHealth(ctx)
	}
	failed, _ := mgr.Registry().Get(mf.ID)
	if failed.Status != StatusError || failed.Health.State != "open" {
		t.Fatal("idle source was not checked", failed)
	}
}

func TestHealthIgnoresOldClientCancellationAndRetainsReloadErrors(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	server := testModuleServer(t, "Working", nil, nil)
	manifest := Manifest{ID: "health-provider", Name: "Health", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: server.URL}}
	dir := writeTestManifest(t, root, manifest)
	mgr := NewManager(root, nil)
	defer mgr.Stop(ctx)
	if _, err := mgr.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	entry, _ := mgr.Registry().Get(manifest.ID)
	old := httpClient(entry)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for i := 0; i < 4; i++ {
		old.recordHealth(canceled, context.Canceled)
	}
	if old.Health().ConsecutiveFailures != 0 {
		t.Fatal("user cancellation disabled source")
	}
	manifest.Version = "2"
	writeTestManifest(t, root, manifest)
	if _, err := mgr.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		old.recordHealth(ctx, errors.New("old private failure"))
	}
	fresh, _ := mgr.Registry().Get(manifest.ID)
	if fresh.Status != StatusActive || fresh.Health.ConsecutiveFailures != 0 {
		t.Fatal("old client disabled replacement", fresh)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if report, err := mgr.Reload(ctx); err != nil || len(report.Errors) == 0 {
		t.Fatal(report, err)
	}
	forceHealthCheck(httpClient(fresh))
	mgr.CheckHealth(ctx)
	current, _ := mgr.Registry().Get(manifest.ID)
	if current.LastError == "" || current.Health.State != "healthy" {
		t.Fatal("health success hid failed hot update", current)
	}
}
