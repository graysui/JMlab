package metadata

import "context"

// SourceAdapter separates upstream metadata fetching from catalog persistence.
type SourceAdapter interface {
	Name() string
	Search(ctx context.Context, query string) ([]SourceSummary, error)
	Ranking(ctx context.Context, rankingType, period string) (RankingSnapshot, error)
	Fetch(ctx context.Context, sourceID, sourceURL string) (SourceItem, error)
}

type SourceSummary struct{ SourceID, Code, Title, URL string }

// RankingSnapshot is the immutable observation returned by an upstream ranking
// page. The catalog stores the observation as a new snapshot after all detail
// pages have been fetched successfully.
type RankingSnapshot struct {
	RankingType string
	Period      string
	ObservedAt  string
	Items       []RankingEntry
}

type RankingEntry struct {
	SourceID string
	Code     string
	Title    string
	URL      string
	Rank     int
}

type SourceItem struct {
	SourceID, Code, RawCode, Title, OriginalTitle, Overview, ReleaseDate, URL, PosterURL, BackdropURL string
	Year, RuntimeSeconds                                                                              *int
	Rating                                                                                            *float64
	RatingCount                                                                                       int
	People, Studios, Series, Tags                                                                     []SourceRelation
	Images                                                                                            []SourceImage
}

type SourceRelation struct{ Name, Role string }

type SourceImage struct {
	Type     string
	URL      string
	Label    string
	Priority int
}
