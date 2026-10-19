package catalog

import (
	"strings"

	"github.com/graysui/JMlab/internal/module"
)

// AvailableSourceSQL describes a recorded candidate that can be used by the
// current runtime. Aliases are internal constants, never request input. It does
// not probe the network or promise successful playback.
func (s *Service) AvailableSourceSQL(sourceAlias, itemAlias string) (string, []any) {
	if s.modules == nil {
		return "1=0", nil
	}
	var choices []string
	var args []any
	for _, entry := range s.modules.List() {
		if entry.Status != module.StatusActive || entry.StreamInstance == nil {
			continue
		}
		choice := sourceAlias + ".module_id=?"
		args = append(args, entry.Manifest.ID)
		scopeSQL, scopeArgs := PlaybackScopeSQL(entry.LibraryScope, itemAlias)
		choice += " AND (" + scopeSQL + ")"
		args = append(args, scopeArgs...)
		if types := entry.SupportedTypes(); len(types) > 0 {
			marks := make([]string, len(types))
			for index, kind := range types {
				marks[index] = "?"
				args = append(args, string(kind))
			}
			choice += " AND " + itemAlias + ".item_type IN (" + strings.Join(marks, ",") + ")"
		} else if len(entry.Manifest.SupportedItemTypes) > 0 || entry.Capabilities != nil && len(entry.Capabilities.SupportedTypes) > 0 {
			// Two explicit, disjoint declarations support no media type.
			choice += " AND 1=0"
		}
		choices = append(choices, "("+choice+")")
	}
	if len(choices) == 0 {
		return "1=0", nil
	}
	return sourceAlias + ".enabled=1 AND " + sourceAlias + ".status='active' AND (" + sourceAlias + `.provider_id IS NULL OR EXISTS (SELECT 1 FROM online_providers available_provider WHERE available_provider.id=` + sourceAlias + `.provider_id AND available_provider.enabled=1)) AND (` + strings.Join(choices, " OR ") + ")", args
}

// HasAvailableSourceSQL shares the same recorded-source rule with home/stats.
func (s *Service) HasAvailableSourceSQL(itemAlias string) (string, []any) {
	clause, args := s.AvailableSourceSQL("available_source", itemAlias)
	return "EXISTS (SELECT 1 FROM stream_sources available_source WHERE available_source.item_id=" + itemAlias + ".id AND (" + clause + "))", args
}
