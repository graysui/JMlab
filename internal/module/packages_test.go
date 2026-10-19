package module

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A real native child process exercises the startup contract on Windows and Unix.
func TestPackageProcessHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "jmlab-package-helper" {
		return
	}
	address := os.Getenv("JMLAB_MODULE_LISTEN_ADDR")
	if address == "" {
		return
	}
	if os.Getenv("JMLAB_MASTER_KEY") != "" || os.Getenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD") != "" {
		os.Exit(12)
	}
	_ = os.WriteFile(filepath.Join(os.Getenv("JMLAB_MODULE_DATA_DIR"), "retained.txt"), []byte("module data"), 0600)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("JMLAB_MODULE_TOKEN") {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/exit" {
			os.Exit(9)
		}
		var config map[string]any
		data, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-JMlab-Module-Config"))
		_ = json.Unmarshal(data, &config)
		if config["fail"] == true {
			http.Error(w, "preflight failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MetadataCaps{SupportsSearch: true, SupportedTypes: []ItemType{ItemMovie}})
	})
	if err := http.ListenAndServe(address, handler); err != nil {
		os.Exit(11)
	}
	os.Exit(0)
}

func nativePackage(t *testing.T, manifest *Manifest) []byte {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	data, _ := json.Marshal(manifest)
	entry, _ := archive.Create("manifest.json")
	_, _ = entry.Write(data)
	entry, err = archive.Create(manifest.Runtime.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(entry, file); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func packageManifest() *Manifest {
	exe := "bin/provider"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	return &Manifest{ID: "native-package", Name: "本地模块", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP}, Runtime: &RuntimeConfig{OS: runtime.GOOS, Arch: runtime.GOARCH, Executable: exe, Args: []string{"-test.run=^TestPackageProcessHelper$", "--", "jmlab-package-helper"}}, ConfigSchema: map[string]ConfigField{"apiKey": {Type: "password", Secret: true, Label: "凭证"}, "fail": {Type: "boolean", Label: "失败检查"}}}
}

func TestPackageLifecycleAndFailedReplacement(t *testing.T) {
	ctx := context.Background()
	t.Setenv("JMLAB_MASTER_KEY", "must-not-reach-child")
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD", "must-not-reach-child")
	dir := t.TempDir()
	m := NewManager(dir, nil)
	store := &factoryRestoreStore{state: map[string]Settings{}}
	if err := m.SetStateStore(ctx, store); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	manifest := packageManifest()
	data := nativePackage(t, manifest)
	stage := func(data []byte) *PackagePreview {
		t.Helper()
		p, err := m.InspectPackage(ctx, "admin", bytes.NewReader(data), "")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	preview := stage(data)
	if _, err := os.Stat(filepath.Join(dir, ".data", manifest.ID)); !os.IsNotExist(err) {
		t.Fatal("inspection executed package code")
	}
	input := PackageInstallOptions{PackageToken: preview.Token, ModuleID: manifest.ID, Config: map[string]any{"apiKey": "retained-secret"}}
	if _, err := m.InstallPackage(ctx, "another-admin", input); err == nil {
		t.Fatal("staged package was not owner-bound")
	}
	if _, err := m.InstallPackage(ctx, "admin", input); err != nil {
		t.Fatal(err)
	}
	old, _ := m.registry.Get(manifest.ID)
	if old.process == nil || !old.process.alive() || old.Status != StatusActive {
		t.Fatal("native module did not start")
	}
	if _, err := m.InstallPackage(ctx, "admin", input); err == nil {
		t.Fatal("consumed package token replayed")
	}
	before, _ := os.ReadFile(filepath.Join(dir, manifest.ID, "manifest.json"))
	manifest.Version = "2"
	update := stage(nativePackage(t, manifest))
	bad := PackageInstallOptions{PackageToken: update.Token, ModuleID: manifest.ID, Revision: update.Revision, Config: map[string]any{"fail": true, "apiKey": "changed-secret"}}
	if _, err := m.InstallPackage(ctx, "admin", bad); err == nil {
		t.Fatal("failed preflight replaced live module")
	}
	current, _ := m.registry.Get(manifest.ID)
	after, _ := os.ReadFile(filepath.Join(dir, manifest.ID, "manifest.json"))
	if current.Revision != old.Revision || !bytes.Equal(before, after) || !old.process.alive() || m.Settings(manifest.ID).Config["apiKey"] != "retained-secret" {
		t.Fatal("failed update changed old runtime, manifest or configuration")
	}
	bad.Config = map[string]any{"apiKey": ""}
	store.fail = true
	if _, err := m.InstallPackage(ctx, "admin", bad); err == nil {
		t.Fatal("failed persistence accepted")
	}
	store.fail = false
	after, _ = os.ReadFile(filepath.Join(dir, manifest.ID, "manifest.json"))
	if !bytes.Equal(before, after) || !old.process.alive() {
		t.Fatal("persistence failure did not roll back")
	}
	if _, err := m.InstallPackage(ctx, "admin", bad); err != nil {
		t.Fatal(err)
	}
	current, _ = m.registry.Get(manifest.ID)
	if old.process.alive() || current.Manifest.Version != "2" || m.Settings(manifest.ID).Config["apiKey"] != "retained-secret" {
		t.Fatal("successful update did not preserve credentials or stop previous executable")
	}
	state := m.Settings(manifest.ID)
	state.Enabled = false
	if err := m.UpdateSettings(ctx, manifest.ID, state); err != nil {
		t.Fatal(err)
	}
	if current.process.alive() {
		t.Fatal("disabled executable still running")
	}
	state.Enabled = true
	if err := m.UpdateSettings(ctx, manifest.ID, state); err != nil {
		t.Fatal(err)
	}
	current, _ = m.registry.Get(manifest.ID)
	if !current.process.alive() {
		t.Fatal("enable did not restart executable")
	}
	// A full host restart reloads the selected release and saved configuration.
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := NewManager(dir, nil)
	_ = restarted.SetStateStore(ctx, store)
	t.Cleanup(func() { _ = restarted.Stop(ctx) })
	if _, err := restarted.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = restarted.registry.Get(manifest.ID)
	if current == nil || current.Status != StatusActive || current.process == nil {
		t.Fatal("host restart did not restore packaged module")
	}
	// Crash recovery uses the same persisted release, with a new process/token.
	crashed := current.process
	request, _ := http.NewRequest("POST", crashed.url+"/exit", nil)
	request.Header.Set("Authorization", "Bearer "+crashed.token)
	_, _ = http.DefaultClient.Do(request)
	select {
	case <-crashed.done:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture failed to exit")
	}
	if _, err := restarted.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = restarted.registry.Get(manifest.ID)
	if current.process == crashed || !current.process.alive() {
		t.Fatal("crashed module did not recover")
	}
	// Restore records exclude executables; reimporting repairs the durable queue.
	store.pending = []RestoreRecord{{ID: manifest.ID, Manifest: current.Manifest, Settings: restarted.Settings(manifest.ID)}}
	store.pending[0].Manifest.Runtime.Release = strings.Repeat("x", 24)
	report, err := restarted.Reload(ctx)
	if err != nil || len(report.Errors) == 0 {
		t.Fatal("missing executable was treated as restored")
	}
	repair, err := restarted.InspectPackage(ctx, "admin", bytes.NewReader(data), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.InstallPackage(ctx, "admin", PackageInstallOptions{PackageToken: repair.Token, ModuleID: manifest.ID, Revision: repair.Revision}); err != nil {
		t.Fatal(err)
	}
	if len(store.pending) != 0 {
		t.Fatal("package reimport did not complete restore")
	}
	// The real SQL store supports deletion; use a manager without storage here.
	restarted.store = nil
	current, _ = restarted.registry.Get(manifest.ID)
	if err := restarted.Uninstall(ctx, manifest.ID); err != nil {
		t.Fatal(err)
	}
	if current.process.alive() {
		t.Fatal("uninstall left executable running")
	}
	if _, err := os.Stat(filepath.Join(dir, ".data", manifest.ID, "retained.txt")); err != nil {
		t.Fatal("uninstall deleted module data")
	}
	if _, err := os.Stat(filepath.Join(dir, manifest.ID)); !os.IsNotExist(err) {
		t.Fatal("uninstall left executable files installed")
	}
}

func TestPackageArchiveBoundaries(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	manifest := packageManifest()
	for _, test := range []struct {
		name          string
		entries       []string
		symlink       bool
		wrongPlatform bool
	}{
		{"traversal", []string{"../escape"}, false, false}, {"absolute", []string{"/escape"}, false, false}, {"backslash", []string{"bin\\escape"}, false, false}, {"device", []string{"CON.txt"}, false, false}, {"stream", []string{"file:stream"}, false, false}, {"alias", []string{"File.txt", "file.txt"}, false, false}, {"symlink", []string{"link"}, true, false}, {"platform", nil, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			archive := zip.NewWriter(&buffer)
			copy := *manifest
			run := *manifest.Runtime
			copy.Runtime = &run
			if test.wrongPlatform {
				run.Arch = "wrong"
			}
			data, _ := json.Marshal(copy)
			entry, _ := archive.Create("manifest.json")
			_, _ = entry.Write(data)
			entry, _ = archive.Create(run.Executable)
			_, _ = entry.Write([]byte("not executed"))
			for _, name := range test.entries {
				header := &zip.FileHeader{Name: name}
				if test.symlink {
					header.SetMode(os.ModeSymlink | 0777)
				}
				entry, _ = archive.CreateHeader(header)
				_, _ = entry.Write([]byte("unsafe"))
			}
			_ = archive.Close()
			if _, err := m.InspectPackage(context.Background(), "admin", bytes.NewReader(buffer.Bytes()), ""); err == nil {
				t.Fatal("invalid package accepted")
			}
		})
	}
	data := nativePackage(t, manifest)
	if _, err := m.InspectPackage(context.Background(), "admin", bytes.NewReader(data), strings.Repeat("0", 64)); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	preview, err := m.InspectPackage(context.Background(), "admin", bytes.NewReader(data), "")
	if err != nil {
		t.Fatal(err)
	}
	m.stagedPackages[preview.Token].ExpiresAt = time.Now().Add(-time.Second)
	if _, err := m.InstallPackage(context.Background(), "admin", PackageInstallOptions{PackageToken: preview.Token, ModuleID: manifest.ID}); err == nil {
		t.Fatal("expired package accepted")
	}
	if len(m.stagedPackages) != 0 {
		t.Fatal("expired staging files not cleaned")
	}
}
