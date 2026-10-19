package playback

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

type discoveryFixture struct {
	id       string
	discover func(context.Context) ([]module.StreamCandidate, error)
	resolve  func(context.Context) (module.ResolvedStream, error)
	probe    func(context.Context) (module.HealthResult, error)
}

func (f discoveryFixture) ModuleID() string { return f.id }
func (f discoveryFixture) DiscoverSources(ctx context.Context, _, _ string, _ module.ItemType, _ map[string]string, _ map[string]any) ([]module.StreamCandidate, error) {
	if f.discover != nil {
		return f.discover(ctx)
	}
	return []module.StreamCandidate{{CandidateID: "one", Label: "Test", Protocol: "http"}}, nil
}
func (f discoveryFixture) ResolveStream(ctx context.Context, _, _ string, _ map[string]string) (module.ResolvedStream, error) {
	if f.resolve != nil {
		return f.resolve(ctx)
	}
	return module.ResolvedStream{StreamURL: "https://media.example.test/new.mp4", Protocol: "http"}, nil
}
func (f discoveryFixture) ProbeSource(ctx context.Context, _, _ string) (module.HealthResult, error) {
	if f.probe != nil {
		return f.probe(ctx)
	}
	return module.HealthResult{Healthy: true}, nil
}

func TestProbeDoesNotMarkReplacementSourceUnhealthy(t *testing.T) {
	s, item := discoveryService(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	registerDiscovery(t, s, discoveryFixture{id: "probe-stream", probe: func(context.Context) (module.HealthResult, error) {
		close(started)
		<-release
		return module.HealthResult{Healthy: false}, nil
	}})
	if err := s.DiscoverItemSources(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	sources, err := s.listSourceRecords(context.Background(), item.ID)
	if err != nil || len(sources) != 1 {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.probeSourceRecord(context.Background(), sources[0]); done <- err }()
	receiveSignal(t, started)
	registerDiscovery(t, s, discoveryFixture{id: "probe-stream"})
	if _, err := s.probeSourceRecord(context.Background(), sources[0]); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, module.ErrRevisionChanged) {
		t.Fatalf("old probe accepted: %v", err)
	}
	source, err := s.getSourceRecord(context.Background(), sources[0].ID)
	if err != nil || source.ProbeCount != 1 || source.Status != "active" {
		t.Fatalf("late probe changed replacement: %+v %v", source.view(), err)
	}
}

func discoveryService(t *testing.T) (*Service, catalog.Item) {
	t.Helper()
	db, err := repository.Open(context.Background(), "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.DB().Exec(`INSERT INTO media_items (id,source,source_id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('discovery-item','test','one','test:one','','Test','','now','now')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db, nil, nil, "isolated-test-key")
	s.SetModuleRegistry(module.NewRegistry())
	return s, catalog.Item{ID: "discovery-item", ItemType: "movie", Title: "Test"}
}
func registerDiscovery(t *testing.T, s *Service, f discoveryFixture, types ...module.ItemType) {
	t.Helper()
	if err := s.modules.Register(&module.ModuleEntry{Manifest: &module.Manifest{ID: f.id, Name: f.id, Version: "1", Type: module.TypeStream, SupportedItemTypes: types, MediaAccess: &module.MediaAccess{AllowedOrigins: []string{"https://media.example.test"}}}, Status: module.StatusActive, StreamInstance: f}); err != nil {
		t.Fatal(err)
	}
}
func receiveSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("module did not reach synchronization point")
	}
}

func TestDiscoverySharesInflightWorkAndResolvesCandidatesConcurrently(t *testing.T) {
	s, item := discoveryService(t)
	var discoveries, running, peak, resolved atomic.Int32
	started, release := make(chan struct{}, 20), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := discoveryFixture{id: "concurrent-stream", discover: func(context.Context) ([]module.StreamCandidate, error) {
		discoveries.Add(1)
		candidates := []module.StreamCandidate{}
		for i := 0; i < 12; i++ {
			candidates = append(candidates, module.StreamCandidate{CandidateID: fmt.Sprint(i), Protocol: "http"})
		}
		return candidates, nil
	}, resolve: func(ctx context.Context) (module.ResolvedStream, error) {
		count := running.Add(1)
		defer running.Add(-1)
		for previous := peak.Load(); count > previous && !peak.CompareAndSwap(previous, count); previous = peak.Load() {
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return module.ResolvedStream{}, ctx.Err()
		}
		resolved.Add(1)
		return module.ResolvedStream{StreamURL: "https://media.example.test/video.mp4", Protocol: "http"}, nil
	}}
	registerDiscovery(t, s, f, module.ItemMovie)
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() { done <- s.discoverModuleSources(context.Background(), item, false) }()
	}
	for i := 0; i < 4; i++ {
		receiveSignal(t, started)
	}
	once.Do(func() { close(release) })
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if discoveries.Load() != 1 || resolved.Load() != 12 || peak.Load() != 4 {
		t.Fatalf("duplicate or unbounded calls: discoveries=%d resolved=%d peak=%d", discoveries.Load(), resolved.Load(), peak.Load())
	}
	sources, err := s.listSourceRecords(context.Background(), item.ID)
	if err != nil || len(sources) != 12 {
		t.Fatalf("missing candidates: %d %v", len(sources), err)
	}
	// Media type and disabled state are enforced before making module calls.
	if err := s.modules.SetStatus(f.id, module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.discoverModuleSources(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	registerDiscovery(t, s, f, module.ItemEpisode)
	if err := s.discoverModuleSources(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	if discoveries.Load() != 1 {
		t.Fatal("disabled or unsupported module was invoked")
	}
}

func TestDiscoveryDiscardsOldRevisionAfterHotReplacement(t *testing.T) {
	s, item := discoveryService(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := discoveryFixture{id: "replace-stream", resolve: func(context.Context) (module.ResolvedStream, error) {
		close(started)
		<-release
		return module.ResolvedStream{StreamURL: "https://media.example.test/old.mp4", Protocol: "http"}, nil
	}}
	registerDiscovery(t, s, f)
	done := make(chan error, 1)
	go func() { done <- s.discoverModuleSources(context.Background(), item, true) }()
	receiveSignal(t, started)
	registerDiscovery(t, s, discoveryFixture{id: f.id})
	if err := s.discoverModuleSources(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, module.ErrRevisionChanged) {
		t.Fatalf("old revision persisted: %v", err)
	}
	sources, err := s.listSourceRecords(context.Background(), item.ID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %v %v", sources, err)
	}
	target, _ := s.sourceURL(sources[0])
	if !strings.HasSuffix(target, "/new.mp4") {
		t.Fatalf("old instance overwrote replacement: %s", target)
	}
}

func TestDiscoveryDeadlineKeepsHealthySourcesAndDoesNotWriteLateResults(t *testing.T) {
	s, item := discoveryService(t)
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	registerDiscovery(t, s, discoveryFixture{id: "healthy-stream"})
	registerDiscovery(t, s, discoveryFixture{id: "slow-stream", discover: func(context.Context) ([]module.StreamCandidate, error) {
		close(started)
		<-release
		defer close(finished)
		return []module.StreamCandidate{{CandidateID: "late", Protocol: "http"}}, nil
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.discoverModuleSources(ctx, item, false) }()
	receiveSignal(t, started)
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not propagated: %v", err)
	}
	once.Do(func() { close(release) })
	receiveSignal(t, finished)
	sources, err := s.listSourceRecords(context.Background(), item.ID)
	if err != nil || len(sources) != 1 || sources[0].ModuleID != "healthy-stream" {
		t.Fatalf("slow module blocked or wrote late: %+v %v", sources, err)
	}
	// Cancellation is not cached as a successful discovery for 30 seconds.
	registerDiscovery(t, s, discoveryFixture{id: "slow-stream"})
	if err := s.discoverModuleSources(context.Background(), item, false); err != nil {
		t.Fatal(err)
	}
	sources, err = s.listSourceRecords(context.Background(), item.ID)
	if err != nil || len(sources) != 2 {
		t.Fatalf("retry did not recover: %d %v", len(sources), err)
	}
}

func TestResolutionDiscardsLateOldRevision(t *testing.T) {
	s, item := discoveryService(t)
	registerDiscovery(t, s, discoveryFixture{id: "resolve-stream"})
	if err := s.discoverModuleSources(context.Background(), item, false); err != nil {
		t.Fatal(err)
	}
	sources, err := s.listSourceRecords(context.Background(), item.ID)
	if err != nil || len(sources) != 1 {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	registerDiscovery(t, s, discoveryFixture{id: "resolve-stream", resolve: func(context.Context) (module.ResolvedStream, error) {
		close(started)
		<-release
		return module.ResolvedStream{StreamURL: "https://media.example.test/old.mp4", Protocol: "http"}, nil
	}})
	done := make(chan error, 1)
	go func() { _, err := s.resolveModuleSource(context.Background(), item, sources[0], true); done <- err }()
	receiveSignal(t, started)
	registerDiscovery(t, s, discoveryFixture{id: "resolve-stream"})
	if err := s.discoverModuleSources(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, module.ErrRevisionChanged) {
		t.Fatalf("late resolve accepted: %v", err)
	}
	sources, err = s.listSourceRecords(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := s.sourceURL(sources[0])
	if !strings.HasSuffix(target, "/new.mp4") {
		t.Fatal("late resolution replaced current URL")
	}
}

func TestSourcePreferenceDoesNotChangeWithTimestampRemainder(t *testing.T) {
	preferred := sourceRecord{ProviderPriority: 2, Protocol: "http", LastResolvedAt: time.Unix(1000, 0).Format(time.RFC3339Nano)}
	other := sourceRecord{ProviderPriority: 1, Protocol: "http", LastResolvedAt: time.Unix(999, 0).Format(time.RFC3339Nano)}
	if sourceScore(preferred, SelectionOptions{}) <= sourceScore(other, SelectionOptions{}) {
		t.Fatal("timestamp remainder overrode configured priority")
	}
}

type payloadVersionFixture struct {
	discoveryFixture
	version string
	wrong   *atomic.Int32
}

func (f payloadVersionFixture) DiscoverSources(context.Context, string, string, module.ItemType, map[string]string, map[string]any) ([]module.StreamCandidate, error) {
	return []module.StreamCandidate{{CandidateID: "one", Protocol: "http", EncryptedPayload: f.version}}, nil
}
func (f payloadVersionFixture) ResolveStream(_ context.Context, _ string, payload string, _ map[string]string) (module.ResolvedStream, error) {
	if payload != f.version {
		f.wrong.Add(1)
		return module.ResolvedStream{}, errors.New("old reference format")
	}
	return module.ResolvedStream{StreamURL: "https://media.example.test/" + f.version + ".mp4", Protocol: "http"}, nil
}
func TestExplicitSourceRefreshesOpaqueReferenceAfterReplacement(t *testing.T) {
	s, item := discoveryService(t)
	var wrong atomic.Int32
	register := func(version string) {
		t.Helper()
		entry := &module.ModuleEntry{Manifest: &module.Manifest{ID: "versioned-stream", Name: "Versioned", Version: version, Type: module.TypeStream, MediaAccess: &module.MediaAccess{AllowedOrigins: []string{"https://media.example.test"}}}, Status: module.StatusActive, StreamInstance: payloadVersionFixture{discoveryFixture: discoveryFixture{id: "versioned-stream"}, version: version, wrong: &wrong}}
		if err := s.modules.Register(entry); err != nil {
			t.Fatal(err)
		}
	}
	register("one")
	if err := s.DiscoverItemSources(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	sources, err := s.listSourceRecords(context.Background(), item.ID)
	if err != nil || len(sources) != 1 {
		t.Fatal(err)
	}
	register("two")
	resolved, err := s.resolveModuleSource(context.Background(), item, sources[0], false)
	if err != nil || !strings.HasSuffix(resolved.URL, "/two.mp4") || wrong.Load() != 0 {
		t.Fatalf("replacement consumed old reference: %+v %v wrong=%d", resolved, err, wrong.Load())
	}
}
