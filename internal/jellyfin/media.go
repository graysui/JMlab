package jellyfin

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func mediaType(kind string) string {
	switch kind {
	case "series":
		return "Series"
	case "season":
		return "Season"
	case "episode":
		return "Episode"
	case "video":
		return "Video"
	default:
		return "Movie"
	}
}
func mediaProviderIDs(source, code, sourceID string, ids map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range ids {
		if key != "code" {
			result[key] = value
		}
	}
	if source == "javdb" {
		result["JavDB"] = code
		if sourceID != "" {
			result["JavDBId"] = sourceID
		}
	}
	return result
}
func (s *Service) showChildren(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	parent := chi.URLParam(r, "itemID")
	if strings.HasSuffix(r.URL.Path, "/Seasons") {
		values.Set("IncludeItemTypes", "Season")
	} else {
		values.Set("IncludeItemTypes", "Episode")
		values.Set("seriesId", parent)
		if seasonID := firstValue(values, "SeasonId", "seasonId"); seasonID != "" {
			parent = seasonID
		} else {
			// Series episode browsing is a single query over its season parents.
			values.Set("seriesId", parent)
			parent = ""
		}
	}
	values.Set("ParentId", parent)
	r.URL.RawQuery = values.Encode()
	s.items(w, r)
}
