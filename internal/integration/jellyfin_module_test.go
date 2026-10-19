package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/jellyfin"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/playback"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/userdata"
)

// An ordinary film and TV module uses exactly the public HTTP contract. No
// JavDB code, source IDs, credentials or source-specific host registrations.
func TestJellyfinPublicModuleMovieEpisodePlaybackAndResume(t *testing.T) {
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-media-token" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".vtt") {
			w.Header().Set("Content-Type", "text/vtt")
			io.WriteString(w, "WEBVTT\n\n00:00.000 --> 00:01.000\nSubtitle")
			return
		}
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXTINF:4,\nsegment.ts\n")
			return
		}
		if r.Header.Get("Range") == "bytes=0-2" {
			w.Header().Set("Content-Range", "bytes 0-2/6")
			w.WriteHeader(206)
			io.WriteString(w, "abc")
			return
		}
		io.WriteString(w, "abcdef")
	}))
	defer upstream.Close()
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer module-test-token" {
			w.WriteHeader(401)
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/api/v1/caps":
			out(module.MetadataCaps{SupportsSearch: true, SupportsStreams: true, SupportedTypes: []module.ItemType{module.ItemMovie, module.ItemSeries, module.ItemSeason, module.ItemEpisode}})
		case "/api/v1/detail":
			id, _ := in["sourceId"].(string)
			kind := module.ItemType(id)
			out(module.MediaDetail{SourceID: id, ItemType: kind, Title: "Ordinary " + id, ProviderIDs: map[string]string{"tmdb": "42"}})
		case "/api/v1/stream/discover":
			out(map[string]any{"candidates": []module.StreamCandidate{{CandidateID: "mkv", Label: "Original MKV", Protocol: "http", Language: "en"}, {CandidateID: "hls", Label: "HLS", Protocol: "hls"}}})
		case "/api/v1/stream/resolve":
			id, _ := in["candidateId"].(string)
			protocol, ext := "http", "mkv"
			if id == "hls" {
				protocol, ext = "hls", "m3u8"
			}
			duration, size, width, height, channels := 3600, int64(1<<34), 1920, 1080, 6
			out(module.ResolvedStream{StreamURL: upstream.URL + "/video." + ext, Protocol: protocol, RequiresProxy: true, RequiredHeaders: map[string]string{"Authorization": "Bearer private-media-token"}, Media: &module.MediaDescription{Container: ext, DurationSeconds: &duration, SizeBytes: &size, Tracks: []module.MediaTrack{{Index: 0, Type: "video", Codec: "hevc", Width: &width, Height: &height, IsDefault: true}, {Index: 2, Type: "audio", Codec: "eac3", Language: "en", Channels: &channels, IsDefault: true}}}, Subtitles: []module.SubtitleTrack{{URL: upstream.URL + "/subtitle.vtt", Language: "zh", Label: "Chinese", Format: "vtt"}}})
		case "/api/v1/stream/probe":
			out(module.HealthResult{Healthy: true})
		default:
			w.WriteHeader(404)
		}
	}))
	defer rpc.Close()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "jellyfin.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{}
	a := auth.NewService(db, cfg, nil)
	store := auth.NewStore(db)
	hash, err := auth.HashPassword("Local-jellyfin-test-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"admin", "viewer", "other"} {
		role := "Viewer"
		if name == "admin" {
			role = "Admin"
		}
		if _, err := store.CreateUser(ctx, name, name, hash, role); err != nil {
			t.Fatal(err)
		}
	}
	manager := module.NewManager(t.TempDir(), nil)
	defer manager.Stop(ctx)
	manifest := &module.Manifest{ID: "ordinary-media", Name: "Ordinary Media", Version: "1", Type: module.TypeComposite, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: rpc.URL}, MediaAccess: &module.MediaAccess{AllowedOrigins: []string{upstream.URL}, AllowPrivate: true}}
	if err := manager.Install(ctx, manifest, module.Settings{Enabled: true, Token: "module-test-token"}); err != nil {
		t.Fatal(err)
	}
	c := catalog.NewService(db, a, nil)
	c.SetModuleRegistry(manager.Registry())
	p := playback.NewService(db, a, c, "isolated-jellyfin-test-key")
	p.SetModuleRegistry(manager.Registry())
	u := userdata.NewService(db, a)
	j := jellyfin.NewService(db, cfg, a, c, u)
	j.SetMediaSourcesProvider(p.JellyfinSources)
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) { c.RegisterRoutes(r); p.RegisterRoutes(r); u.RegisterRoutes(r) })
	j.RegisterProtocolRoutes(router)
	p.RegisterProtocolRoutes(router)
	call := func(token, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-Emby-Token", token)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	login := func(name string) (string, string) {
		t.Helper()
		r := call("", "POST", "/Users/AuthenticateByName", fmt.Sprintf(`{"Username":%q,"Pw":"Local-jellyfin-test-123"}`, name), nil)
		var result struct {
			AccessToken string
			User        struct{ Id string }
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || result.AccessToken == "" {
			t.Fatalf("login %d %s", r.Code, r.Body.String())
		}
		return result.AccessToken, result.User.Id
	}
	admin, _ := login("admin")
	viewer, viewerID := login("viewer")
	other, otherID := login("other")
	lib := call(admin, "POST", "/api/v1/libraries", `{"name":"Ordinary Film and TV","itemType":"mixed","filter":{"metadataModuleId":"ordinary-media"},"isPublic":true,"isAdult":false}`, nil)
	if lib.Code != 201 {
		t.Fatal(lib.Code, lib.Body.String())
	}
	added := map[string]string{}
	for _, kind := range []string{"movie", "series", "season", "episode"} {
		body := map[string]any{"sourceId": kind}
		if kind == "season" {
			body["parentId"] = added["series"]
			body["indexNumber"] = 1
		}
		if kind == "episode" {
			body["parentId"] = added["season"]
			body["indexNumber"] = 1
		}
		raw, _ := json.Marshal(body)
		r := call(admin, "POST", "/api/v1/metadata/ordinary-media/import", string(raw), nil)
		var result struct{ Item catalog.Item }
		if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &result) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
		added[kind] = result.Item.ID
	}
	for _, path := range []string{"/Users/" + viewerID + "/Views", "/Shows/" + added["series"] + "/Seasons", "/Shows/" + added["series"] + "/Episodes", "/Items/" + added["episode"]} {
		r := call(viewer, "GET", path, "", nil)
		if r.Code != 200 {
			t.Fatal(path, r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), "JavDB") {
			t.Fatal("adult provider leaked")
		}
	}
	type mediaSource struct {
		Id, Container, DirectStreamUrl                      string
		DefaultAudioStreamIndex, DefaultSubtitleStreamIndex int
		SupportsTranscoding                                 bool
		RunTimeTicks, Size                                  int64
		RequiredHttpHeaders                                 map[string]string
		MediaStreams                                        []struct {
			Type, Codec, DeliveryUrl string
			Index                    int
			IsExternal               bool
		}
	}
	type info struct {
		MediaSources  []mediaSource
		PlaySessionId string
	}
	for _, kind := range []string{"movie", "episode"} {
		itemID := added[kind]
		r := call(viewer, "POST", "/Items/"+itemID+"/PlaybackInfo", `{}`, nil)
		var data info
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &data) != nil || len(data.MediaSources) != 2 || data.PlaySessionId == "" {
			t.Fatalf("info %d %s", r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), "private-media-token") || strings.Contains(r.Body.String(), rpc.URL) || strings.Contains(r.Body.String(), upstream.URL) {
			t.Fatal("upstream secret exposed")
		}
		var original mediaSource
		for _, v := range data.MediaSources {
			if v.Container == "mkv" {
				original = v
			}
		}
		if original.Id == "" || original.SupportsTranscoding || original.RunTimeTicks != 3600*10000000 || original.Size != 1<<34 || len(original.RequiredHttpHeaders) != 0 || len(original.MediaStreams) != 3 || original.MediaStreams[0].Codec != "hevc" || original.MediaStreams[1].Codec != "eac3" {
			t.Fatalf("incorrect media properties: %+v", original)
		}
		r = call(viewer, "POST", "/Items/"+itemID+"/PlaybackInfo", fmt.Sprintf(`{"MediaSourceId":%q,"StartTimeTicks":123000000,"AudioStreamIndex":2,"SubtitleStreamIndex":-1}`, original.Id), nil)
		var explicit info
		_ = json.Unmarshal(r.Body.Bytes(), &explicit)
		if r.Code != 200 || len(explicit.MediaSources) == 0 || explicit.MediaSources[0].Id != original.Id || explicit.MediaSources[0].DefaultAudioStreamIndex != 2 || explicit.MediaSources[0].DefaultSubtitleStreamIndex != -1 {
			t.Fatal("explicit selection lost", r.Body.String())
		}
		for _, body := range []string{`{"AudioStreamIndex":99}`, `{"SubtitleStreamIndex":99}`, `{"StartTimeTicks":-1}`, `{"StartTimeTicks":"bad"}`, `{`} {
			bad := call(viewer, "POST", "/Items/"+itemID+"/PlaybackInfo", body, nil)
			if bad.Code != 400 {
				t.Fatalf("invalid playback request %s: %d %s", body, bad.Code, bad.Body.String())
			}
		}
		transcode := call(viewer, "POST", "/Items/"+itemID+"/PlaybackInfo", `{"EnableDirectPlay":false,"EnableDirectStream":false}`, nil)
		if transcode.Code != 200 || !strings.Contains(transcode.Body.String(), `"ErrorCode":"NoCompatibleStream"`) {
			t.Fatal("pretended to offer transcoding", transcode.Body.String())
		}
		for _, signedURL := range []string{original.DirectStreamUrl, original.MediaStreams[2].DeliveryUrl} {
			if cross := call(other, "GET", signedURL, "", nil); cross.Code != 403 {
				t.Fatal("cross user signed URL", cross.Code, cross.Body.String())
			}
		}
		stream := call("", "GET", original.DirectStreamUrl, "", map[string]string{"Range": "bytes=0-2"})
		if stream.Code != 206 || stream.Body.String() != "abc" || stream.Header().Get("Content-Range") != "bytes 0-2/6" {
			t.Fatal("range", stream.Code, stream.Body.String())
		}
		subtitle := call("", "GET", original.MediaStreams[2].DeliveryUrl, "", nil)
		if subtitle.Code != 200 || !strings.HasPrefix(subtitle.Body.String(), "WEBVTT") {
			t.Fatal("subtitle", subtitle.Code, subtitle.Body.String())
		}
		for _, v := range data.MediaSources {
			if v.Container == "m3u8" {
				hls := call("", "GET", v.DirectStreamUrl, "", nil)
				if hls.Code != 200 || !strings.Contains(hls.Body.String(), "resource=") {
					t.Fatal("hls rewrite", hls.Code, hls.Body.String())
				}
				for _, line := range strings.Split(hls.Body.String(), "\n") {
					if strings.HasPrefix(line, "/Videos/") {
						segment := call("", "GET", line, "", nil)
						if segment.Code != 200 || segment.Body.String() != "abcdef" {
							t.Fatal("segment")
						}
					}
				}
			}
		}
		for _, event := range []string{"/Sessions/Playing", "/Sessions/Playing/Progress", "/Sessions/Playing/Stopped"} {
			body := fmt.Sprintf(`{"ItemId":%q,"PlaySessionId":%q,"PositionTicks":123000000,"RunTimeTicks":36000000000}`, itemID, data.PlaySessionId)
			eventResult := call(viewer, "POST", event, body, nil)
			if eventResult.Code != 204 {
				t.Fatal("event", eventResult.Code, eventResult.Body.String())
			}
		}
		state := call(viewer, "GET", "/Users/"+viewerID+"/Items/"+itemID+"/UserData", "", nil)
		if state.Code != 200 || !strings.Contains(state.Body.String(), `"PlaybackPositionTicks":123000000`) {
			t.Fatal("resume state", state.Code, state.Body.String())
		}
		otherState := call(other, "GET", "/Users/"+otherID+"/Items/"+itemID+"/UserData", "", nil)
		if otherState.Code != 200 || strings.Contains(otherState.Body.String(), `"PlaybackPositionTicks":123000000`) {
			t.Fatal("cross user progress")
		}
		// Signed URLs remain subject to current access and module state.
		if err := manager.DisableModule("ordinary-media"); err != nil {
			t.Fatal(err)
		}
		if r := call("", "GET", original.DirectStreamUrl, "", nil); r.Code == 200 || r.Code == 206 {
			t.Fatal("disabled module served old token")
		}
		if r := call("", "GET", original.MediaStreams[2].DeliveryUrl, "", nil); r.Code == 200 {
			t.Fatal("disabled module served subtitle")
		}
		if err := manager.EnableModule("ordinary-media"); err != nil {
			t.Fatal(err)
		}
		for _, signedURL := range []string{original.DirectStreamUrl, original.MediaStreams[2].DeliveryUrl} {
			if stale := call("", "GET", signedURL, "", nil); stale.Code < 400 {
				t.Fatal("old revision served signed URL after re-enable")
			}
		}
	}
	resume := call(viewer, "GET", "/Users/"+viewerID+"/Items/Resume", "", nil)
	if resume.Code != 200 || !strings.Contains(resume.Body.String(), added["episode"]) {
		t.Fatal("resume list", resume.Code, resume.Body.String())
	}
	otherResume := call(other, "GET", "/Users/"+otherID+"/Items/Resume", "", nil)
	if otherResume.Code != 200 || !strings.Contains(otherResume.Body.String(), `"TotalRecordCount":0`) {
		t.Fatal("cross user resume list", otherResume.Body.String())
	}
	watched := call(viewer, "PUT", "/api/v1/me/items/"+added["movie"]+"/state", `{"watched":true}`, nil)
	if watched.Code != 200 {
		t.Fatal(watched.Code, watched.Body.String())
	}
	filtered := call(viewer, "GET", "/Users/"+viewerID+"/Items/Resume?SortBy=Unknown&StartIndex=0&Limit=1", "", nil)
	if filtered.Code != 200 || !strings.Contains(filtered.Body.String(), `"TotalRecordCount":1`) || !strings.Contains(filtered.Body.String(), added["episode"]) {
		t.Fatal("completed movie or resume paging", filtered.Code, filtered.Body.String())
	}
	atEnd := call(viewer, "PUT", "/api/v1/me/items/"+added["episode"]+"/state", `{"positionTicks":36000000000,"runtimeTicks":36000000000,"watched":false}`, nil)
	if atEnd.Code != 200 {
		t.Fatal(atEnd.Code, atEnd.Body.String())
	}
	for _, path := range []string{"/Items/" + added["episode"], "/Items?Ids=" + added["episode"] + "&Recursive=true"} {
		detail := call(viewer, "GET", path, "", nil)
		if detail.Code != 200 || strings.Contains(detail.Body.String(), `"CanResume":true`) {
			t.Fatal("finished progress advertised resumable", detail.Code, detail.Body.String())
		}
	}
	finishedResume := call(viewer, "GET", "/Users/"+viewerID+"/Items/Resume", "", nil)
	if finishedResume.Code != 200 || !strings.Contains(finishedResume.Body.String(), `"TotalRecordCount":0`) {
		t.Fatal("finished progress in resume list", finishedResume.Body.String())
	}
}
