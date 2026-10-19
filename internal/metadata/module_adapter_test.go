package metadata

import (
	"context"
	"testing"

	"github.com/graysui/JMlab/internal/module"
)

type metadataFixture struct{ title string }

func (f metadataFixture) ModuleID() string { return "test-metadata" }
func (f metadataFixture) Caps(context.Context) (module.MetadataCaps, error) {
	return module.MetadataCaps{SupportsSearch: true}, nil
}
func (f metadataFixture) Search(context.Context, string, module.ItemType, int, int) ([]module.MediaSummary, int, error) {
	return []module.MediaSummary{{SourceID: "upstream-1", SourceURL: "https://example.test/detail", Title: f.title, ProviderIDs: map[string]string{"code": "AUD-001"}}}, 1, nil
}
func (f metadataFixture) GetDetail(context.Context, string, map[string]string) (module.MediaDetail, error) {
	return module.MediaDetail{SourceID: "upstream-1", SourceURL: "https://example.test/detail", Title: f.title, ProviderIDs: map[string]string{"code": "AUD-001"},
		ExtraAttributes: map[string]any{"rawCode": "aud-001", "javdbSeries": []SourceRelation{{Name: "Legacy series"}}},
		Entities:        []module.EntityRelation{{Name: "Actor", Role: module.RoleActor}, {Name: "Studio", Role: module.RoleStudio}, {Name: "Tag", Role: module.RoleTag}},
		Images:          []module.MediaImage{{Type: "poster", URL: "https://example.test/image.jpg", Label: "Poster", Priority: 2}}}, nil
}
func (f metadataFixture) GetRankings(context.Context, string, string, int) (module.RankingSnapshot, error) {
	items, _, _ := f.Search(context.Background(), "", module.ItemMovie, 1, 10)
	items[0].Rank = 5
	return module.RankingSnapshot{RankingType: "ranking", Period: "daily", Items: items}, nil
}

func TestLegacyCatalogSelectsCurrentModuleAndRetainsMetadata(t *testing.T) {
	ctx := context.Background()
	registry := module.NewRegistry()
	manifest := &module.Manifest{ID: "test-metadata", Name: "Test", Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportInProcess}}
	set := func(title string) {
		t.Helper()
		if err := registry.Register(&module.ModuleEntry{Manifest: manifest, Status: module.StatusActive, MetadataInstance: metadataFixture{title: title}}); err != nil {
			t.Fatal(err)
		}
	}
	set("first")
	adapter := NewModuleSourceAdapter(registry, manifest.ID, "javdb")
	item, err := adapter.Fetch(ctx, "upstream-1", "")
	if err != nil || item.Code != "AUD-001" || item.RawCode != "aud-001" || len(item.Series) != 1 || len(item.People) != 1 || len(item.Studios) != 1 || len(item.Tags) != 1 || item.Images[0].Label != "Poster" {
		t.Fatalf("metadata lost: %+v %v", item, err)
	}
	snapshot, err := adapter.Ranking(ctx, "ranking", "daily")
	if err != nil || snapshot.Items[0].Rank != 5 {
		t.Fatalf("ranking: %+v %v", snapshot, err)
	}
	set("replacement")
	items, err := adapter.Search(ctx, "test")
	if err != nil || items[0].Title != "replacement" {
		t.Fatal("adapter cached old provider")
	}
	if err := registry.SetStatus(manifest.ID, module.StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Fetch(ctx, "upstream-1", ""); err == nil {
		t.Fatal("disabled module still serves metadata")
	}
}
