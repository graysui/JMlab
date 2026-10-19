package metadata

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/graysui/JMlab/internal/module"
)

// ModuleSourceAdapter preserves the existing JavDB catalog contract while every
// request selects the currently active module. It never caches provider instances.
type ModuleSourceAdapter struct {
	registry         *module.Registry
	moduleID, source string
}

func NewModuleSourceAdapter(registry *module.Registry, moduleID, source string) *ModuleSourceAdapter {
	return &ModuleSourceAdapter{registry: registry, moduleID: moduleID, source: source}
}
func (a *ModuleSourceAdapter) Name() string { return a.source }
func (a *ModuleSourceAdapter) current(method string) (module.MetadataProvider, error) {
	entry, ok := a.registry.Get(a.moduleID)
	if !ok || entry.Status != module.StatusActive || entry.MetadataInstance == nil {
		return nil, errors.New("元数据模块已停用或不可用")
	}
	if caps := entry.Capabilities; caps != nil && (method == "search" && !caps.SupportsSearch || method == "rankings" && !caps.SupportsRankings) {
		return nil, errors.New("元数据模块不支持此操作")
	}
	return entry.MetadataInstance, nil
}
func (a *ModuleSourceAdapter) Search(ctx context.Context, query string) ([]SourceSummary, error) {
	provider, err := a.current("search")
	if err != nil {
		return nil, err
	}
	items, _, err := provider.Search(ctx, query, module.ItemMovie, 1, 250)
	if err != nil {
		return nil, err
	}
	result := make([]SourceSummary, 0, len(items))
	for _, item := range items {
		result = append(result, SourceSummary{SourceID: item.SourceID, Code: item.ProviderIDs["code"], Title: item.Title, URL: item.SourceURL})
	}
	return result, nil
}
func (a *ModuleSourceAdapter) Ranking(ctx context.Context, kind, period string) (RankingSnapshot, error) {
	provider, err := a.current("rankings")
	if err != nil {
		return RankingSnapshot{}, err
	}
	snapshot, err := provider.GetRankings(ctx, kind, period, 1)
	if err != nil {
		return RankingSnapshot{}, err
	}
	result := RankingSnapshot{RankingType: snapshot.RankingType, Period: snapshot.Period, ObservedAt: snapshot.ObservedAt}
	for i, item := range snapshot.Items {
		rank := item.Rank
		if rank == 0 {
			rank = i + 1
		}
		result.Items = append(result.Items, RankingEntry{SourceID: item.SourceID, Code: item.ProviderIDs["code"], Title: item.Title, URL: item.SourceURL, Rank: rank})
	}
	return result, nil
}
func (a *ModuleSourceAdapter) Fetch(ctx context.Context, sourceID, sourceURL string) (SourceItem, error) {
	provider, err := a.current("detail")
	if err != nil {
		return SourceItem{}, err
	}
	detail, err := provider.GetDetail(ctx, sourceID, map[string]string{"sourceUrl": sourceURL})
	if err != nil {
		return SourceItem{}, err
	}
	return DetailToSourceItem(detail), nil
}

func DetailToSourceItem(detail module.MediaDetail) SourceItem {
	item := SourceItem{SourceID: detail.SourceID, URL: detail.SourceURL, Code: detail.ProviderIDs["code"], Title: detail.Title,
		OriginalTitle: detail.OriginalTitle, Overview: detail.Overview, ReleaseDate: detail.ReleaseDate, Year: detail.ProductionYear,
		RuntimeSeconds: detail.RuntimeSeconds, Rating: detail.Rating, RatingCount: detail.RatingCount}
	item.RawCode, _ = detail.ExtraAttributes["rawCode"].(string)
	if item.RawCode == "" {
		item.RawCode = item.Code
	}
	for _, relation := range detail.Entities {
		value := SourceRelation{Name: relation.Name, Role: string(relation.Role)}
		switch relation.Role {
		case module.RoleActor, module.RoleDirector, module.RoleWriter:
			item.People = append(item.People, value)
		case module.RoleStudio:
			item.Studios = append(item.Studios, value)
		case module.RoleGenre, module.RoleTag:
			item.Tags = append(item.Tags, value)
		}
	}
	if series, ok := detail.ExtraAttributes["javdbSeries"]; ok {
		encoded, _ := json.Marshal(series)
		_ = json.Unmarshal(encoded, &item.Series)
	}
	for _, image := range detail.Images {
		item.Images = append(item.Images, SourceImage{Type: image.Type, URL: image.URL, Label: image.Label, Priority: image.Priority})
		if image.Type == "poster" && item.PosterURL == "" {
			item.PosterURL = image.URL
		}
		if image.Type == "backdrop" && item.BackdropURL == "" {
			item.BackdropURL = image.URL
		}
	}
	return item
}
