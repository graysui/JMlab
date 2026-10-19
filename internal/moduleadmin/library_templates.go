package moduleadmin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/module"
)

func (s *Service) libraryTemplateModules(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	for _, entry := range s.manager.Registry().List() {
		items = append(items, map[string]any{"id": entry.Manifest.ID, "name": entry.Manifest.Name, "status": entry.Status, "templateCount": len(entry.Manifest.LibraryTemplates)})
	}
	respond(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) libraryTemplates(w http.ResponseWriter, r *http.Request) {
	entry, ok := s.manager.Registry().Get(chi.URLParam(r, "moduleID"))
	if !ok {
		fail(w, r, 404, "MODULE_NOT_FOUND", "模块不存在，请重新选择")
		return
	}
	if entry.Status != module.StatusActive {
		fail(w, r, 409, "MODULE_UNAVAILABLE", "模块已停用或暂时不可用，请启用后重试")
		return
	}
	items := []map[string]any{}
	for _, template := range entry.Manifest.LibraryTemplates {
		encoded, _ := json.Marshal(template.Filter)
		filter := map[string]any{}
		_ = json.Unmarshal(encoded, &filter)
		if template.Scope == "metadata" {
			filter["metadataModuleId"] = entry.Manifest.ID
		} else {
			filter["streamModuleId"] = entry.Manifest.ID
		}
		scopes := template.RankingScopes
		if scopes == nil {
			scopes = []module.LibraryRankingScope{}
		}
		items = append(items, map[string]any{"id": template.ID, "name": template.Name, "description": template.Description, "itemType": template.ItemType, "scope": template.Scope, "isAdult": template.IsAdult, "filter": filter, "sort": template.Sort, "rankingScopes": scopes})
	}
	respond(w, 200, map[string]any{"moduleId": entry.Manifest.ID, "revision": entry.Revision, "items": items})
}
