package module

import (
	"errors"
	"regexp"
	"time"
)

var rankingValuePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidRankingValue(value string) bool { return rankingValuePattern.MatchString(value) }

type RankingDefinition struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Periods  []FieldOption `json:"periods"`
	MaxPages int           `json:"maxPages,omitempty"`
}

func (m *Manifest) validateRankings() error {
	if len(m.Rankings) > 32 {
		return errors.New("模块榜单类型过多")
	}
	seen := map[string]bool{}
	for _, def := range m.Rankings {
		if m.Type == TypeStream || !ValidRankingValue(def.ID) || def.Name == "" || seen[def.ID] || len(def.Periods) == 0 || len(def.Periods) > 128 || def.MaxPages < 0 || def.MaxPages > 10 {
			return errors.New("模块榜单声明无效")
		}
		seen[def.ID] = true
		periods := map[string]bool{}
		for _, p := range def.Periods {
			if !ValidRankingValue(p.Value) || p.Label == "" || periods[p.Value] {
				return errors.New("榜单周期声明无效")
			}
			periods[p.Value] = true
		}
	}
	return nil
}

func (m *Manifest) RankingDefinitions() []RankingDefinition {
	if len(m.Rankings) > 0 {
		return append([]RankingDefinition(nil), m.Rankings...)
	}
	result := []RankingDefinition{}
	for _, t := range m.LibraryTemplates {
		for _, s := range t.RankingScopes {
			idx := -1
			for i := range result {
				if result[i].ID == s.RankingType {
					idx = i
				}
			}
			if idx < 0 {
				result = append(result, RankingDefinition{ID: s.RankingType, Name: t.Name, MaxPages: 1})
				idx = len(result) - 1
			}
			found := false
			for _, p := range result[idx].Periods {
				if p.Value == s.RankingPeriod {
					found = true
				}
			}
			if !found {
				result[idx].Periods = append(result[idx].Periods, FieldOption{Value: s.RankingPeriod, Label: s.Name})
			}
		}
	}
	return result
}

func (m *Manifest) Ranking(kind, period string) (RankingDefinition, bool) {
	for _, d := range m.RankingDefinitions() {
		if d.ID == kind {
			for _, p := range d.Periods {
				if p.Value == period {
					return d, true
				}
			}
		}
	}
	return RankingDefinition{}, false
}

// Empty uses an hourly interval; HH:MM uses the host local daily time.
func ValidRankingUpdateTime(value string) bool {
	if value == "" {
		return true
	}
	_, err := time.Parse("15:04", value)
	return err == nil && len(value) == 5
}
