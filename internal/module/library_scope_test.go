package module

import (
	"context"
	"testing"
)

func TestNativeStreamScopeSnapshotsAndValidation(t *testing.T) {
	m := NewManager("", nil)
	defer m.Stop(context.Background())
	manifest := &Manifest{ID: "native-source", Name: "Native", Version: "1", Type: TypeStream}
	if err := m.RegisterInProcessModule(manifest, nil, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Registry().Get(manifest.ID)
	state := Settings{Enabled: true, LibraryScope: &LibraryScope{Mode: "selected", LibraryIDs: []string{"content"}}}
	if err := m.UpdateSettings(context.Background(), manifest.ID, state); err != nil {
		t.Fatal(err)
	}
	state.LibraryScope.LibraryIDs[0] = "changed-by-caller"
	entry, _ := m.Registry().Get(manifest.ID)
	if before.Revision == entry.Revision || entry.LibraryScope.LibraryIDs[0] != "content" {
		t.Fatal("scope was not published as a new immutable revision")
	}
	entry.LibraryScope.LibraryIDs[0] = "changed-snapshot"
	if m.Settings(manifest.ID).LibraryScope.LibraryIDs[0] != "content" {
		t.Fatal("manager scope leaked mutable settings")
	}
	entry, _ = m.Registry().Get(manifest.ID)
	if entry.LibraryScope.LibraryIDs[0] != "content" {
		t.Fatal("registry scope leaked mutable settings")
	}
	for _, scope := range []*LibraryScope{{Mode: "unknown"}, {Mode: "all", LibraryIDs: []string{"content"}}, {Mode: "selected", LibraryIDs: []string{"content", "content"}}} {
		if err := m.UpdateSettings(context.Background(), manifest.ID, Settings{Enabled: true, LibraryScope: scope}); err == nil {
			t.Fatal("invalid scope accepted", scope)
		}
	}
	if err := ValidateLibraryScope(&Manifest{Type: TypeMetadata}, &LibraryScope{Mode: "all"}); err == nil {
		t.Fatal("metadata-only module accepted playback scope")
	}
}
