package catalog

import (
	"errors"

	"github.com/graysui/JMlab/internal/module"
)

// Template selection is transient. The resulting filter remains independently
// editable and continues to work with recorded data after a module is removed.
type libraryTemplateSelection struct {
	ModuleID   string `json:"moduleId"`
	TemplateID string `json:"templateId"`
	Revision   string `json:"revision"`
}

func (s *Service) checkLibraryTemplate(selection *libraryTemplateSelection) error {
	if s.modules != nil {
		entry, exists := s.modules.Get(selection.ModuleID)
		if exists && entry.Status == module.StatusActive && entry.Revision == selection.Revision {
			for _, template := range entry.Manifest.LibraryTemplates {
				if template.ID == selection.TemplateID {
					return nil
				}
			}
		}
	}
	return errors.New("模块模板已更新、停用或移除，请重新选择模块和模板")
}
