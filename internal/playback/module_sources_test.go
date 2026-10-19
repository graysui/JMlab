package playback

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
	provider "github.com/graysui/JMlab/internal/stream"
)

func TestLegacyReferenceConversionNeverBypassesModuleState(t *testing.T) {
	s := NewService(nil, nil, nil, "isolated-legacy-test-key")
	const target = "https://media.example.test/legacy.mp4"
	encrypted, err := s.encrypt(target)
	if err != nil {
		t.Fatal(err)
	}
	source := sourceRecord{ID: "old-source-id", ModuleID: builtinStreamModuleID, ProviderID: "legacy-provider", CandidateEncrypted: encrypted}
	item := catalog.Item{ID: "old-media-id", Code: "AUD-001", Title: "Old media"}
	ref, err := s.moduleReference(item, source)
	if err != nil || ref.CandidateID != source.ID || ref.ModuleID != builtinStreamModuleID {
		t.Fatalf("legacy identity changed: %+v %v", ref, err)
	}
	var legacy struct {
		ProviderID   string        `json:"providerId"`
		CandidateURL string        `json:"candidateUrl"`
		Item         provider.Item `json:"item"`
	}
	if err := json.Unmarshal([]byte(ref.Payload), &legacy); err != nil || legacy.ProviderID != source.ProviderID || legacy.CandidateURL != target || legacy.Item.ID != item.ID || legacy.Item.Code != item.Code {
		t.Fatalf("legacy reference lost data: %+v %v", legacy, err)
	}
	if s.moduleSourceEnabled(source) {
		t.Fatal("missing module enabled legacy provider bypass")
	}
	s.SetModuleRegistry(module.NewRegistry())
	registerDiscovery(t, s, discoveryFixture{id: builtinStreamModuleID})
	if !s.moduleSourceEnabled(source) {
		t.Fatal("registered legacy module unavailable")
	}
	if err := s.modules.SetStatus(builtinStreamModuleID, module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	if s.moduleSourceEnabled(source) {
		t.Fatal("disabled module enabled legacy provider bypass")
	}
}

type streamFixture struct{ target, protocol string }

func (f streamFixture) ModuleID() string { return "test-stream" }
func (f streamFixture) DiscoverSources(context.Context, string, string, module.ItemType, map[string]string, map[string]any) ([]module.StreamCandidate, error) {
	return []module.StreamCandidate{{CandidateID: "one", Label: "First", Protocol: f.protocol, EncryptedPayload: "opaque-private-payload"}, {CandidateID: "two", Label: "Second", Protocol: f.protocol, EncryptedPayload: "opaque-private-payload"}}, nil
}
func (f streamFixture) ResolveStream(context.Context, string, string, map[string]string) (module.ResolvedStream, error) {
	return module.ResolvedStream{StreamURL: f.target, Protocol: f.protocol, RequiredHeaders: map[string]string{"Authorization": "Basic private-upstream", "X-Api-Token": "private-token"}, RequiresProxy: true}, nil
}
func (f streamFixture) ProbeSource(context.Context, string, string) (module.HealthResult, error) {
	return module.HealthResult{Healthy: true}, nil
}

func TestModulePlaybackHotUpdateRangeHLSAndDisable(t *testing.T) {
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic private-upstream" {
			t.Error("required credential missing")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/master.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:4,\nsegment.ts\n"))
			return
		}
		if r.Header.Get("Range") == "bytes=0-2" {
			w.Header().Set("Content-Range", "bytes 0-2/6")
			w.WriteHeader(206)
			_, _ = w.Write([]byte("abc"))
			return
		}
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer upstream.Close()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authService := auth.NewService(db, config.Config{}, nil)
	user, err := auth.NewStore(db).CreateUser(ctx, "admin", "Admin", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	catalogService := catalog.NewService(db, authService, nil)
	if err := catalogService.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	_, err = db.DB().Exec(`INSERT INTO media_items (id,source,source_id,code,raw_code,title,source_url,created_at,updated_at) VALUES ('javdb:AUD-001','javdb','upstream-1','AUD-001','AUD-001','Test','https://example.test/item','now','now')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.DB().Exec(`INSERT INTO library_items (library_id,item_id,position,created_at) VALUES ('library-default','javdb:AUD-001',0,'now')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db, authService, catalogService, "test-master-key-that-is-at-least-32-bytes")
	manager := module.NewManager(t.TempDir(), nil)
	registry := manager.Registry()
	s.SetModuleRegistry(registry)
	var current atomic.Value
	moduleRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/caps" {
			_ = json.NewEncoder(w).Encode(module.MetadataCaps{SupportsStreams: true})
			return
		}
		fixture := current.Load().(streamFixture)
		switch r.URL.Path {
		case "/api/v1/stream/discover":
			candidates, _ := fixture.DiscoverSources(r.Context(), "", "", module.ItemMovie, nil, nil)
			_ = json.NewEncoder(w).Encode(map[string]any{"candidates": candidates})
		case "/api/v1/stream/resolve":
			resolved, _ := fixture.ResolveStream(r.Context(), "", "", nil)
			_ = json.NewEncoder(w).Encode(resolved)
		case "/api/v1/stream/probe":
			_ = json.NewEncoder(w).Encode(module.HealthResult{Healthy: true, StatusCode: 200})
		default:
			http.NotFound(w, r)
		}
	}))
	defer moduleRPC.Close()
	manifest := &module.Manifest{ID: "test-stream", Name: "Test stream", Version: "1", Type: module.TypeStream, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: moduleRPC.URL}, MediaAccess: &module.MediaAccess{AllowedOrigins: []string{upstream.URL}, AllowPrivate: true}}
	register := func(target, protocol string) {
		t.Helper()
		current.Store(streamFixture{target: target, protocol: protocol})
		if _, exists := registry.Get(manifest.ID); !exists {
			if err := manager.Install(ctx, manifest, module.Settings{Enabled: true}); err != nil {
				t.Fatal(err)
			}
		} else {
			encoded, _ := json.Marshal(manifest)
			if err := os.WriteFile(filepath.Join(manifest.Directory, "manifest.json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			if report, err := manager.Reload(ctx); err != nil || len(report.Updated) != 1 {
				t.Fatalf("hot reload: %+v %v", report, err)
			}
		}
	}
	register(upstream.URL+"/version-one.mp4", "http")
	item, err := catalogService.GetItemForUser(ctx, "javdb:AUD-001", user)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := s.selectAndResolve(ctx, item, user, SelectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := s.listSourceRecords(ctx, item.ID)
	if err != nil || len(sources) != 2 {
		t.Fatalf("candidates collapsed: %d %v", len(sources), err)
	}
	for _, source := range sources {
		if source.ModuleID != manifest.ID || source.ProviderID != "" || source.ModuleName != manifest.Name {
			t.Fatalf("module source still depends on legacy provider: %+v", source.view())
		}
	}
	var placeholders int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM online_providers WHERE id LIKE 'module:%'`).Scan(&placeholders); err != nil || placeholders != 0 {
		t.Fatal("module discovery created legacy placeholder", err)
	}
	// Admin editing and probing use the module reference, without a provider service.
	adminRouter := chi.NewRouter()
	adminRouter.Patch("/items/{itemID}/sources/{sourceID}", s.updateSource)
	adminRouter.Post("/items/{itemID}/sources/{sourceID}/probe", s.probeSource)
	requestAdmin := func(method, suffix, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		adminRouter.ServeHTTP(rec, httptest.NewRequest(method, "/items/"+item.ID+"/sources/"+sources[0].ID+suffix, strings.NewReader(body)))
		return rec
	}
	beforeCipher := sources[0].CandidateEncrypted
	if rec := requestAdmin("POST", "/probe", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"healthy":true`) {
		t.Fatalf("module probe: %d %s", rec.Code, rec.Body.String())
	}
	if rec := requestAdmin("PATCH", "", `{"label":"我的视频","quality":"4K","sortOrder":3}`); rec.Code != 200 {
		t.Fatalf("module metadata patch: %d %s", rec.Code, rec.Body.String())
	}
	for _, body := range []string{`{"providerId":"other"}`, `{"candidateUrl":"https://example.test"}`, `{"resolvedUrl":"https://example.test"}`} {
		if rec := requestAdmin("PATCH", "", body); rec.Code != 400 {
			t.Fatalf("module ownership mutation allowed: %d %s", rec.Code, rec.Body.String())
		}
	}
	patched, err := s.getSourceRecord(ctx, sources[0].ID)
	if err != nil || patched.CandidateEncrypted != beforeCipher || patched.ProbeCount != 1 || patched.ProbeSuccessCount != 1 {
		t.Fatal("module edit lost opaque reference or probe statistics", err)
	}
	if err := s.discoverModuleSources(ctx, item, true); err != nil {
		t.Fatal(err)
	}
	patched, err = s.getSourceRecord(ctx, sources[0].ID)
	if err != nil || patched.Label != "我的视频" || patched.Quality != "4K" || patched.SortOrder != 3 {
		t.Fatal("rediscovery overwrote administrator choices", err)
	}
	if strings.Contains(sources[0].CandidateEncrypted, "opaque-private-payload") {
		t.Fatal("candidate not encrypted")
	}
	encoded, _ := json.Marshal(resolution)
	if strings.Contains(string(encoded), upstream.URL) || strings.Contains(string(encoded), "private-upstream") {
		t.Fatal("upstream address or credential leaked to client")
	}
	router := chi.NewRouter()
	s.RegisterProtocolRoutes(router)
	get := func(path, rangeHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Range", rangeHeader)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(resolution.PlaybackURL, "bytes=0-2"); rec.Code != 206 || rec.Body.String() != "abc" {
		t.Fatalf("Range: %d %s", rec.Code, rec.Body.String())
	}
	manifest.Version = "2"
	register(upstream.URL+"/version-two.mp4", "http")
	updated, err := s.selectAndResolve(ctx, item, user, SelectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(resolution.PlaybackURL, ""); rec.Code < 400 {
		t.Fatal("hot update kept the previous signed target usable")
	}
	parsed, _ := url.Parse(updated.PlaybackURL)
	bound, err := s.decodeBoundToken(parsed.Query().Get("token"), playbackTokenVersion)
	if err != nil || !strings.HasSuffix(bound.Target, "/version-two.mp4") {
		t.Fatalf("hot update used old resolved URL: %+v %v", bound, err)
	}
	manifest.Version = "3"
	register(upstream.URL+"/master.m3u8", "hls")
	hls, err := s.selectAndResolve(ctx, item, user, SelectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rec := get(hls.PlaybackURL, "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), upstream.URL) || !strings.Contains(rec.Body.String(), "resource=") {
		t.Fatalf("HLS not rewritten: %d %s", rec.Code, rec.Body.String())
	}
	if err := registry.SetStatus(manifest.ID, module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	if rec := get(updated.PlaybackURL, ""); rec.Code < 400 {
		t.Fatal("disabled module still served a signed playback URL")
	}
	if _, err := s.selectAndResolve(ctx, item, user, SelectionOptions{}); err == nil {
		t.Fatal("disabled module still selected")
	}
	if rec := get("/Videos/javdb:AUD-001/stream", ""); rec.Code != 401 {
		t.Fatalf("anonymous playback: %d", rec.Code)
	}
}

func TestModuleNetworkRejectsPrivateDNSAndStripsCrossOriginCredentials(t *testing.T) {
	var leaked atomic.Bool
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Token") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte("video"))
	}))
	defer cdn.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, cdn.URL+"/video.mp4", 302) }))
	defer upstream.Close()
	s := NewService(nil, nil, nil, "test-master-key-that-is-at-least-32-bytes")
	registry := module.NewRegistry()
	s.SetModuleRegistry(registry)
	manifest := &module.Manifest{ID: "test-stream", Name: "Test", Version: "1", Type: module.TypeStream, Transport: module.TransportConfig{Type: module.TransportInProcess}, MediaAccess: &module.MediaAccess{AllowedOrigins: []string{upstream.URL, cdn.URL}, AllowPrivate: true}}
	if err := registry.Register(&module.ModuleEntry{Manifest: manifest, Status: module.StatusActive, StreamInstance: streamFixture{}}); err != nil {
		t.Fatal(err)
	}
	target := upstream.URL + "/redirect"
	urlEncrypted, _ := s.encrypt(target)
	headersEncrypted, _ := s.encrypt(`{"Authorization":"Basic private-upstream","X-Api-Token":"private-token"}`)
	source := sourceRecord{ModuleID: "test-stream", ResolvedEncrypted: urlEncrypted, HeadersEncrypted: headersEncrypted}
	response, err := s.fetchModuleStream(context.Background(), httptest.NewRequest("GET", "/", nil), source, target)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if leaked.Load() {
		t.Fatal("credential forwarded across media origins")
	}
	// A hostname that resolves to loopback must still be denied without private-network authorization.
	localTarget := strings.Replace(target, "127.0.0.1", "localhost", 1)
	manifest.MediaAccess = &module.MediaAccess{AllowedOrigins: []string{strings.TrimSuffix(localTarget, "/redirect")}}
	if err := registry.Register(&module.ModuleEntry{Manifest: manifest, Status: module.StatusActive, StreamInstance: streamFixture{}}); err != nil {
		t.Fatal(err)
	}
	source.ResolvedEncrypted, _ = s.encrypt(localTarget)
	if response, err := s.fetchModuleStream(context.Background(), httptest.NewRequest("GET", "/", nil), source, localTarget); err == nil {
		response.Body.Close()
		t.Fatal("private DNS target accepted without authorization")
	}
	for _, ip := range []string{"169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1"} {
		if allowedIP(net.ParseIP(ip), true) {
			t.Fatalf("unsafe address allowed: %s", ip)
		}
	}
}
