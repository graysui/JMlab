package moduleadmin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

func TestPackageUploadAndURLAdministration(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "packages.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := auth.NewStore(db)
	user, err := store.CreateUser(ctx, "package-admin", "Admin", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, user, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateUser(ctx, "package-viewer", "Viewer", "unused", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	_, viewerToken, err := store.CreateSession(ctx, viewer, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m := module.NewManager(t.TempDir(), nil)
	t.Cleanup(func() { _ = m.Stop(ctx) })
	if err := m.SetStateStore(ctx, NewStore(db, strings.Repeat("k", 32))); err != nil {
		t.Fatal(err)
	}
	s := NewService(db, auth.NewService(db, config.Config{}, nil), m)
	router := chi.NewRouter()
	s.RegisterRoutes(router)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	manifest := module.Manifest{ID: "package-api", Name: "API 测试模块", Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportHTTP}, Runtime: &module.RuntimeConfig{OS: runtime.GOOS, Arch: runtime.GOARCH, Executable: "provider"}, ConfigSchema: map[string]module.ConfigField{"password": {Type: "password", Secret: true, Label: "密码"}}}
	data, _ := json.Marshal(manifest)
	entry, _ := archive.Create("manifest.json")
	_, _ = entry.Write(data)
	entry, _ = archive.Create("provider")
	_, _ = entry.Write([]byte("not executed during inspect or disabled install"))
	_ = archive.Close()
	zipData := buffer.Bytes()
	hash := sha256.Sum256(zipData)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("download forwarded user credentials")
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	defer upstream.Close()
	// Only this isolated fixture replaces the downloader. Production uses pinned public IPs.
	s.packageClient = upstream.Client()
	request := func(method, path, token, contentType string, body io.Reader, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", contentType)
		if cookie {
			r.AddCookie(&http.Cookie{Name: "jmlab_session", Value: token})
		} else if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		token  string
		status int
		cookie bool
	}{{"", 401, false}, {viewerToken, 403, false}, {token, 403, true}} {
		response := request("POST", "/admin/modules/packages/inspect", test.token, "application/json", strings.NewReader(`{"url":"`+upstream.URL+`"}`), test.cookie)
		if response.Code != test.status {
			t.Fatalf("authorization: %d %s", response.Code, response.Body.String())
		}
	}
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	part, _ := form.CreateFormFile("file", "module.zip")
	_, _ = part.Write(zipData)
	_ = form.Close()
	response := request("POST", "/admin/modules/packages/inspect", token, form.FormDataContentType(), &upload, false)
	if response.Code != 200 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var preview module.PackagePreview
	_ = json.Unmarshal(response.Body.Bytes(), &preview)
	if preview.SHA256 != hex.EncodeToString(hash[:]) || len(m.Registry().List()) != 0 {
		t.Fatal("preview hash or execution boundary incorrect")
	}
	// Cancel frees a staging slot, and the old token cannot be installed.
	response = request("DELETE", "/admin/modules/packages/"+preview.Token, token, "application/json", nil, false)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	input := module.PackageInstallOptions{PackageToken: preview.Token, ModuleID: manifest.ID}
	encoded, _ := json.Marshal(input)
	if response = request("POST", "/admin/modules/packages/install", token, "application/json", bytes.NewReader(encoded), false); response.Code != 400 {
		t.Fatal("cancelled upload accepted")
	}
	response = request("POST", "/admin/modules/packages/inspect", token, "application/json", strings.NewReader(`{"url":"`+upstream.URL+`/module.zip?signature=private","sha256":"`+hex.EncodeToString(hash[:])+`"}`), false)
	if response.Code != 200 {
		t.Fatalf("URL import: %s", response.Body.String())
	}
	_ = json.Unmarshal(response.Body.Bytes(), &preview)
	disabled := false
	input = module.PackageInstallOptions{PackageToken: preview.Token, ModuleID: manifest.ID, Enabled: &disabled, Config: map[string]any{"password": "api-test-private-value"}}
	encoded, _ = json.Marshal(input)
	response = request("POST", "/admin/modules/packages/install", token, "application/json", bytes.NewReader(encoded), false)
	if response.Code != 201 {
		t.Fatalf("install: %s", response.Body.String())
	}
	response = request("GET", "/admin/modules", token, "application/json", nil, false)
	var listed map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &listed)
	if listed["packagePlatform"] != runtime.GOOS+"/"+runtime.GOARCH || strings.Contains(response.Body.String(), `"api-test-private-value"`) || strings.Contains(response.Body.String(), "signature=private") {
		t.Fatal("list leaked secrets or omitted package platform")
	}
	response = request("DELETE", "/admin/modules/"+manifest.ID, token, "application/json", nil, false)
	if response.Code != 200 {
		t.Fatalf("uninstall: %s", response.Body.String())
	}
}

func TestPackageDownloadNetworkBoundaries(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "198.18.0.1", "::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "2002:7f00:1::"} {
		if publicPackageIP(net.ParseIP(raw)) {
			t.Fatalf("unsafe IP allowed: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicPackageIP(net.ParseIP(raw)) {
			t.Fatalf("public IP rejected: %s", raw)
		}
	}
	client := packageHTTPClient()
	for _, raw := range []string{"file:///etc/passwd", "http://user:password@example.com/module.zip", "http://localhost/module.zip", "http://127.0.0.1:1/module.zip", "http://169.254.169.254/module.zip", "http://[::1]/module.zip"} {
		if response, err := downloadPackage(context.Background(), raw, client); err == nil {
			response.Body.Close()
			t.Fatalf("unsafe download allowed: %s", raw)
		}
	}
	redirect := httptest.NewRequest("GET", "http://example.com/module.zip", nil)
	via := []*http.Request{httptest.NewRequest("GET", "https://example.com/module.zip", nil)}
	if client.CheckRedirect(redirect, via) == nil {
		t.Fatal("HTTPS downgrade accepted")
	}
}
