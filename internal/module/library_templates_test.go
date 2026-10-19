package module

import (
	"encoding/json"
	"testing"
)

func TestLibraryTemplateValidation(t *testing.T) {
	base := `{"id":"reference-provider-javdb","name":"JavDB","version":"1","type":"metadata","transport":{"type":"in_process"},"libraryTemplates":[{"id":"popular-ranking","name":"热门榜","scope":"metadata","itemType":"movie","filter":{"minRating":8},"sort":{"field":"rating","direction":"desc"},"rankingScopes":[{"id":"ranking-daily","name":"日榜","rankingType":"ranking","rankingPeriod":"daily"}]}]}`
	for _, test := range []struct {
		name    string
		mutate  func(*Manifest)
		invalid bool
	}{
		{"valid", func(*Manifest) {}, false},
		{"duplicate", func(m *Manifest) { m.LibraryTemplates = append(m.LibraryTemplates, m.LibraryTemplates[0]) }, true},
		{"wrong module scope", func(m *Manifest) { m.LibraryTemplates[0].Scope = "stream" }, true},
		{"third-party ranking template", func(m *Manifest) { m.ID = "another-source" }, false},
		{"invalid period", func(m *Manifest) { m.LibraryTemplates[0].RankingScopes[0].RankingPeriod = "invalid period" }, true},
		{"invalid sort", func(m *Manifest) { m.LibraryTemplates[0].Sort.Field = "unknown" }, true},
		{"invalid rating", func(m *Manifest) { *m.LibraryTemplates[0].Filter.MinRating = 11 }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var m Manifest
			if err := json.Unmarshal([]byte(base), &m); err != nil {
				t.Fatal(err)
			}
			test.mutate(&m)
			if err := m.Validate(); (err != nil) != test.invalid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}
