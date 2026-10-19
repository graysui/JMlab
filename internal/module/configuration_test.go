package module

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type unavailableStore struct{}

func (unavailableStore) Load(context.Context) (map[string]Settings, error) {
	return map[string]Settings{}, nil
}
func (unavailableStore) Save(context.Context, string, Settings) error {
	return errors.New("database unavailable")
}

func TestConfigurationSaveFailureRestoresManifest(t *testing.T) {
	ctx := context.Background()
	m := NewManager(t.TempDir(), nil)
	manifest := &Manifest{ID: "rollback-module", Name: "Rollback", Version: "1", Type: TypeStream, Transport: TransportConfig{Type: TransportHTTP, URL: "http://127.0.0.1:9092"}}
	if err := m.Install(ctx, manifest, Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.modulesDir, manifest.ID, "manifest.json")
	original, _ := os.ReadFile(path)
	before, _ := m.Registry().Get(manifest.ID)
	if err := m.SetStateStore(ctx, unavailableStore{}); err != nil {
		t.Fatal(err)
	}
	err := m.UpdateConfiguration(ctx, manifest.ID, Settings{Enabled: false}, &TransportConfig{Type: TransportHTTP, URL: "http://127.0.0.1:9192"}, &MediaAccess{AllowedOrigins: []string{"https://example.com"}})
	if err == nil {
		t.Fatal("failed persistence accepted")
	}
	after, _ := m.Registry().Get(manifest.ID)
	current, _ := os.ReadFile(path)
	if string(current) != string(original) || after.Revision != before.Revision || after.Manifest.Transport.URL != before.Manifest.Transport.URL {
		t.Fatal("failed save changed manifest or runtime")
	}
}

type deleteFailureStore struct{ unavailableStore }

func (deleteFailureStore) Delete(context.Context, string) error {
	return errors.New("delete failed")
}

func TestUninstallFailureKeepsRuntimeAndManifest(t *testing.T) {
	ctx := context.Background()
	m := NewManager(t.TempDir(), nil)
	manifest := &Manifest{ID: "keep-module", Name: "Keep", Version: "1", Type: TypeStream,
		Transport: TransportConfig{Type: TransportHTTP, URL: "http://127.0.0.1:9092"}}
	if err := m.Install(ctx, manifest, Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.modulesDir, manifest.ID, "manifest.json")
	original, _ := os.ReadFile(path)
	before, _ := m.Registry().Get(manifest.ID)
	if err := m.SetStateStore(ctx, deleteFailureStore{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(ctx, manifest.ID); err == nil {
		t.Fatal("failed state deletion accepted")
	}
	current, err := os.ReadFile(path)
	after, exists := m.Registry().Get(manifest.ID)
	if err != nil || string(original) != string(current) || !exists || before.Revision != after.Revision {
		t.Fatal("failed uninstall changed the working definition")
	}
	// Never remove a directory containing an independently maintained program.
	program := filepath.Join(m.modulesDir, manifest.ID, "server.js")
	if err := os.WriteFile(program, []byte("independent program"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(ctx, manifest.ID); err == nil {
		t.Fatal("uninstall accepted a directory containing a program")
	}
	if _, err := os.Stat(program); err != nil {
		t.Fatal("uninstall removed independent program")
	}
	m.builtin[manifest.ID] = true
	if err := m.Uninstall(ctx, manifest.ID); err == nil {
		t.Fatal("built-in uninstall accepted")
	}
	if err := m.UpdateManifest(ctx, manifest.ID, manifest); err == nil {
		t.Fatal("built-in manifest replacement accepted")
	}
}
