package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventQueuesDeduplicateBoundAndIsolateBlockedHandlers(t *testing.T) {
	b := NewEventBus(nil)
	entered, release := make(chan struct{}), make(chan struct{})
	b.SubscribeNamed("blocked", []string{"media.item.created"}, func(ctx context.Context, e Event) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return nil
	})
	healthy := make(chan string, 128)
	b.SubscribeNamed("healthy", []string{"media.item.created"}, func(ctx context.Context, e Event) error { healthy <- e.EventID; return nil })
	defer func() { close(release); _ = b.Close(context.Background()) }()
	if !b.Publish(context.Background(), "media.item.created", "first", `{}`) {
		t.Fatal("first rejected")
	}
	<-entered
	select {
	case <-healthy:
	case <-time.After(time.Second):
		t.Fatal("healthy subscriber blocked")
	}
	b.Publish(context.Background(), "media.item.created", "first", `{}`)
	for i := 0; i < 64; i++ {
		id := fmt.Sprint(i)
		if !b.Publish(context.Background(), "media.item.created", id, `{}`) {
			t.Fatal("queue filled early", i)
		}
		select {
		case got := <-healthy:
			if got != id {
				t.Fatal("duplicate or ordering error", got, id)
			}
		case <-time.After(time.Second):
			t.Fatal("other consumer blocked")
		}
	}
	if b.Publish(context.Background(), "media.item.created", "overflow", `{}`) {
		t.Fatal("overflow not reported")
	}
	stats := b.Stats("blocked")
	if stats.Queued != 64 || stats.Dropped != 1 {
		t.Fatal(stats)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if !errors.Is(b.Close(ctx), context.DeadlineExceeded) {
		t.Fatal("close failed to bound uncooperative native handler")
	}
	if b.Publish(context.Background(), "media.item.created", "after-close", `{}`) {
		t.Fatal("publish after close accepted")
	}
}

func TestEventRetriesUseSameIdentityAndRecoverPanic(t *testing.T) {
	b := NewEventBus(nil)
	defer b.Close(context.Background())
	var retries, panics atomic.Int32
	done := make(chan Event, 1)
	b.SubscribeNamed("retry", []string{"topic"}, func(ctx context.Context, e Event) error {
		if e.EventID != "stable-id" {
			t.Error("retry identity changed")
		}
		if retries.Add(1) < 3 {
			return errors.New("temporary")
		}
		done <- e
		return nil
	})
	b.SubscribeNamed("panic", []string{"topic"}, func(ctx context.Context, e Event) error { panics.Add(1); panic("private secret") })
	b.Publish(context.Background(), "topic", "stable-id", `{}`)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not recover")
	}
	deadline := time.Now().Add(time.Second)
	for b.Stats("panic").Failed != 1 {
		if time.Now().After(deadline) {
			t.Fatal("panic subscriber did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if panics.Load() != 3 || retries.Load() != 3 {
		t.Fatal("unbounded retry")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b.Publish(ctx, "topic", "cancelled", `{}`) {
		t.Fatal("cancelled publish accepted")
	}
}

func TestExternalEventsRetryReloadDisableAndShutdown(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	defer m.Stop(context.Background())
	m.eventBus.deliveryTimeout = 30 * time.Millisecond
	var attempts atomic.Int32
	blocked, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	received := make(chan Event, 8)
	serve := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer module-secret" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/v1/caps" {
			_ = json.NewEncoder(w).Encode(MetadataCaps{SupportsSearch: true})
			return
		}
		var event Event
		if json.NewDecoder(r.Body).Decode(&event) != nil {
			w.WriteHeader(400)
			return
		}
		if event.EventID == "blocked" {
			blocked <- struct{}{}
			<-r.Context().Done()
			cancelled <- struct{}{}
			return
		}
		if event.EventID == "retry" && attempts.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		received <- event
		w.WriteHeader(204)
	}
	old := httptest.NewServer(http.HandlerFunc(serve))
	defer old.Close()
	manifest := &Manifest{ID: "event-module", Name: "Events", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: old.URL}, Subscriptions: []string{"media.item.created"}}
	if err := m.Install(context.Background(), manifest, Settings{Enabled: true, Token: "module-secret"}); err != nil {
		t.Fatal(err)
	}
	m.eventBus.Publish(context.Background(), "media.item.created", "retry", `{}`)
	select {
	case e := <-received:
		if e.EventID != "retry" {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP retry did not recover")
	}
	m.eventBus.Publish(context.Background(), "media.item.created", "blocked", `{}`)
	<-blocked
	// Disable cancels in-flight delivery rather than waiting for retries.
	if err := m.DisableModule("event-module"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("disabled delivery not cancelled")
	}
	m.eventBus.Publish(context.Background(), "media.item.created", "disabled", `{}`)
	newReceived := make(chan Event, 4)
	replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/caps" {
			_ = json.NewEncoder(w).Encode(MetadataCaps{})
			return
		}
		var event Event
		_ = json.NewDecoder(r.Body).Decode(&event)
		newReceived <- event
		w.WriteHeader(204)
	}))
	defer replacement.Close()
	if err := m.UpdateConfiguration(context.Background(), "event-module", Settings{Enabled: true, Token: "module-secret"}, &TransportConfig{Type: TransportHTTP, URL: replacement.URL}, nil); err != nil {
		t.Fatal(err)
	}
	m.eventBus.Publish(context.Background(), "media.item.created", "new-version", `{}`)
	select {
	case e := <-newReceived:
		if e.EventID != "new-version" {
			t.Fatal("disabled queue leaked to replacement", e)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement not subscribed")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.eventBus.Publish(context.Background(), "media.item.created", "stopped", `{}`) {
		t.Fatal("shutdown accepted event")
	}
}
