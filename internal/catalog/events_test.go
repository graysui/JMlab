package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/userdata"
)

func TestCommittedMediaAndPlaybackReachExternalSubscriber(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "events.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := auth.NewStore(db).CreateUser(ctx, "viewer", "Viewer", "unused", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan module.Event, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer event-secret" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/v1/caps" {
			_ = json.NewEncoder(w).Encode(module.MetadataCaps{})
			return
		}
		if r.URL.Path != "/api/v1/events" {
			w.WriteHeader(404)
			return
		}
		var event module.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		received <- event
		w.WriteHeader(204)
	}))
	defer server.Close()
	m := module.NewManager(t.TempDir(), nil)
	defer m.Stop(ctx)
	manifest := &module.Manifest{ID: "event-listener", Name: "Events", Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: server.URL}, Subscriptions: []string{"media.item.created", "media.item.updated", "user.state.updated", "playback.started", "playback.stopped", "module.status.changed"}}
	if err := m.Install(ctx, manifest, module.Settings{Enabled: true, Token: "event-secret"}); err != nil {
		t.Fatal(err)
	}
	s := NewService(db, nil, nil)
	s.SetEventBus(m.EventBus())
	if err := s.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	detail := module.MediaDetail{SourceID: "movie:42", ItemType: module.ItemMovie, Title: "Event movie"}
	item, _, err := s.saveModuleMedia(ctx, "tmdb", detail, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	detail.Title = "Updated"
	if _, _, err := s.saveModuleMedia(ctx, "tmdb", detail, "", nil); err != nil {
		t.Fatal(err)
	}
	// A rejected write must not create a phantom event.
	detail.Title = ""
	if _, _, err := s.saveModuleMedia(ctx, "tmdb", detail, "", nil); err == nil {
		t.Fatal("invalid media accepted")
	}
	u := userdata.NewService(db, nil)
	u.SetEventBus(m.EventBus())
	note := "private-note-never-delivered"
	fav := true
	if _, err := u.UpsertItemState(ctx, user.ID, "", item.ID, userdata.StateInput{Favorite: &fav, Note: &note}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Second)
	if _, err := u.UpsertPlayback(ctx, user.ID, item.ID, userdata.PlaybackInput{EventType: "playback_start", EventAt: start, SessionID: "", DeviceName: "private-device"}); err != nil {
		t.Fatal(err)
	}
	stop := userdata.PlaybackInput{EventType: "playback_stop", PositionTicks: 100, EventAt: start.Add(time.Millisecond)}
	if result, err := u.UpsertPlayback(ctx, user.ID, item.ID, stop); err != nil || !result.Accepted {
		t.Fatal(result, err)
	}
	if result, err := u.UpsertPlayback(ctx, user.ID, item.ID, stop); err != nil || !result.Stale {
		t.Fatal("duplicate stop accepted", result, err)
	}
	want := []string{"module.status.changed", "media.item.created", "media.item.updated", "user.state.updated", "playback.started", "playback.stopped"}
	ids := map[string]bool{}
	for _, topic := range want {
		select {
		case event := <-received:
			if event.Topic != topic || event.EventID == "" || ids[event.EventID] {
				t.Fatal("identity/order", event)
			}
			ids[event.EventID] = true
			if strings.Contains(event.PayloadJSON, "private-") || strings.Contains(event.PayloadJSON, "secret") {
				t.Fatal("event leaked personal data", event)
			}
			if topic != "module.status.changed" {
				var payload struct {
					ItemID string `json:"itemId"`
				}
				_ = json.Unmarshal([]byte(event.PayloadJSON), &payload)
				var title string
				if db.DB().QueryRow("SELECT title FROM media_items WHERE id=?", payload.ItemID).Scan(&title) != nil {
					t.Fatal("event before commit")
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("missing business event", topic)
		}
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-received:
		t.Fatal("failed/stale operation emitted", event)
	default:
	}
}
