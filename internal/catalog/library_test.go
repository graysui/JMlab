package catalog

import (
	"encoding/json"
	"testing"
)

func TestParseLibrarySort(t *testing.T) {
	got, _, err := parseLibrarySort(json.RawMessage(`{"by":"rating","order":"ASC"}`))
	if err != nil {
		t.Fatalf("parseLibrarySort returned error: %v", err)
	}
	if got.Field != "rating" || got.Direction != "asc" {
		t.Fatalf("parseLibrarySort = %#v, want rating/asc", got)
	}
	if _, _, err := parseLibrarySort(json.RawMessage(`{"field":"password"}`)); err == nil {
		t.Fatal("parseLibrarySort accepted an unsupported field")
	}
}

func TestParseLibraryFilterNormalizesAndRejects(t *testing.T) {
	filter, encoded, expression, err := parseLibraryFilter(json.RawMessage(`{"years":[2024,2025],"minRating":8.5}`))
	if err != nil {
		t.Fatalf("parseLibraryFilter returned error: %v", err)
	}
	if expression == nil || len(filter) != 2 || encoded == "" {
		t.Fatalf("parseLibraryFilter returned incomplete result: filter=%#v encoded=%q expression=%#v", filter, encoded, expression)
	}
	if _, _, _, err := parseLibraryFilter(json.RawMessage(`{"field":"years","value":[1899]}`)); err == nil {
		t.Fatal("parseLibraryFilter accepted an out-of-range year")
	}
	if _, _, _, err := parseLibraryFilter(json.RawMessage(`{"all":[{"field":"years","value":[2024]}],"any":[]}`)); err == nil {
		t.Fatal("parseLibraryFilter accepted multiple logical operators")
	}
}

func TestRankingLibraryFilters(t *testing.T) {
	cases := []struct {
		rankingType   string
		rankingPeriod string
	}{
		{"ranking", "daily"},
		{"ranking", "weekly"},
		{"ranking", "monthly"},
		{"playback", "daily"},
		{"top250", "all"},
		{"top250", "2026"},
	}
	for _, tc := range cases {
		raw := `{"rankingType":"` + tc.rankingType + `","rankingPeriod":"` + tc.rankingPeriod + `"}`
		filter, encoded, expr, err := parseLibraryFilter(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parseLibraryFilter(%s) failed: %v", raw, err)
		}
		if expr == nil || encoded == "" || len(filter) != 2 {
			t.Fatalf("parseLibraryFilter(%s) incomplete: %#v", raw, filter)
		}
	}
}
