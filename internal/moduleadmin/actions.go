package moduleadmin

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/module"
	"net/http"
)

func (s *Service) invokeAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision string         `json:"revision"`
		Input    map[string]any `json:"input"`
	}
	if decode(w, r, &input) != nil {
		fail(w, r, 400, "INVALID_MODULE_ACTION", "操作参数格式不正确")
		return
	}
	id := chi.URLParam(r, "moduleID")
	result, err := s.manager.InvokeAction(r.Context(), id, chi.URLParam(r, "actionID"), input.Revision, input.Input)
	if err != nil {
		status := 400
		if errors.Is(err, module.ErrRevisionChanged) {
			status = 409
		}
		fail(w, r, status, "MODULE_ACTION_FAILED", err.Error())
		return
	}
	s.audit(r, "invoke_module_action", id+":"+chi.URLParam(r, "actionID"))
	respond(w, 200, result)
}
