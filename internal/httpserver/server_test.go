package httpserver

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graysui/JMlab/internal/config"
)

func TestWebItemNavigationAndProtocolBoundary(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>JMlab</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(config.Config{WebDir: webDir}, nil, nil)
	for _, tc := range []struct {
		name, method, path, accept, token string
		want                              int
	}{
		{"detail", "GET", "/items/javdb:AUD-001", "text/html", "", 200},
		{"play refresh", "GET", "/items/javdb:AUD-001/play", "text/html,application/xhtml+xml", "", 200},
		{"head", "HEAD", "/items/javdb:AUD-001", "text/html", "", 200},
		{"JSON client", "GET", "/items/missing", "application/json", "", 404},
		{"unspecified client", "GET", "/items/missing", "", "", 404},
		{"protocol token", "GET", "/items/missing", "text/html", "test-token", 404},
		{"protocol query token", "GET", "/items/missing?api_key=test", "text/html", "", 404},
		{"protocol prefix", "GET", "/emby/items/missing", "text/html", "", 404},
		{"protocol case", "GET", "/Items/missing", "text/html", "", 404},
		{"unknown protocol child", "GET", "/items/missing/unknown", "text/html", "", 404},
		{"write", "POST", "/items/missing", "text/html", "", 404},
		{"unknown API", "GET", "/api/v1/missing", "text/html", "", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Accept", tc.accept)
			if tc.token != "" {
				req.Header.Set("X-Emby-Token", tc.token)
			}
			rec := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status %d; body %s", rec.Code, rec.Body.String())
			}
			if tc.want == 200 && !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("expected Web document: %s", rec.Header())
			}
			if tc.want == 404 && !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("expected API/protocol JSON: %s", rec.Body.String())
			}
		})
	}
}
