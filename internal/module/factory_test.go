package module

import (
	"context"
	"errors"
	"testing"
)

type configuredMetadata struct{ enabled bool }

func (f configuredMetadata) ModuleID() string { return "factory-source" }
func (f configuredMetadata) Caps(context.Context) (MetadataCaps, error) {
	return MetadataCaps{SupportsSearch: f.enabled, SupportedTypes: []ItemType{ItemMovie}}, nil
}
func (f configuredMetadata) Search(context.Context, string, ItemType, int, int) ([]MediaSummary, int, error) {
	return nil, 0, nil
}
func (f configuredMetadata) GetDetail(context.Context, string, map[string]string) (MediaDetail, error) {
	return MediaDetail{}, nil
}
func (f configuredMetadata) GetRankings(context.Context, string, string, int) (RankingSnapshot, error) {
	return RankingSnapshot{}, nil
}

type factoryRestoreStore struct {
	state   map[string]Settings
	pending []RestoreRecord
	fail    bool
}

func (s *factoryRestoreStore) Load(context.Context) (map[string]Settings, error) { return s.state, nil }
func (s *factoryRestoreStore) Save(_ context.Context, id string, state Settings) error {
	if s.fail {
		return errors.New("failed save")
	}
	s.state[id] = state
	return nil
}
func (s *factoryRestoreStore) PendingRestore(context.Context) ([]RestoreRecord, error) {
	return s.pending, nil
}
func (s *factoryRestoreStore) CompleteRestore(context.Context, string) error {
	s.pending = nil
	return nil
}
func (s *factoryRestoreStore) SaveRestore(_ context.Context, r RestoreRecord) error {
	s.pending = []RestoreRecord{r}
	return nil
}

func TestFactoryConfigurationAndRestoreRebuildCapabilities(t *testing.T) {
	ctx := context.Background()
	m := NewManager("", nil)
	store := &factoryRestoreStore{state: map[string]Settings{}}
	if err := m.SetStateStore(ctx, store); err != nil {
		t.Fatal(err)
	}
	manifest := &Manifest{ID: "factory-source", Name: "Ordinary Metadata", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportInProcess}, ConfigSchema: map[string]ConfigField{"searchEnabled": {Type: "boolean"}}}
	factory := func(_ context.Context, state Settings) (MetadataProvider, StreamProvider, ActionProvider, error) {
		enabled, _ := state.Config["searchEnabled"].(bool)
		return configuredMetadata{enabled}, nil, nil, nil
	}
	if err := m.RegisterFactory(ctx, manifest, factory); err != nil {
		t.Fatal(err)
	}
	original, _ := m.registry.Get(manifest.ID)
	state := Settings{Enabled: true, Config: map[string]any{"searchEnabled": true}}
	if err := m.UpdateConfiguration(ctx, manifest.ID, state, nil, nil); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.registry.Get(manifest.ID)
	if !updated.Capabilities.SupportsSearch || updated.Revision == original.Revision {
		t.Fatal("caps not refreshed")
	}
	store.fail = true
	state.Config["searchEnabled"] = false
	if err := m.UpdateConfiguration(ctx, manifest.ID, state, nil, nil); err == nil {
		t.Fatal("failed persistence accepted")
	}
	unchanged, _ := m.registry.Get(manifest.ID)
	if !unchanged.Capabilities.SupportsSearch || unchanged.Revision != updated.Revision {
		t.Fatal("failed save replaced live instance")
	}
	store.fail = false
	store.pending = []RestoreRecord{{ID: manifest.ID, Manifest: manifest, Settings: state}}
	report, err := m.CommitRestore(ctx, func() error { return nil })
	if err != nil || len(report.Errors) != 0 {
		t.Fatal(report, err)
	}
	restored, _ := m.registry.Get(manifest.ID)
	if restored.Capabilities.SupportsSearch || restored.MetadataInstance.(configuredMetadata).enabled || len(store.pending) != 0 {
		t.Fatal("restored settings were not applied to instance")
	}
}
