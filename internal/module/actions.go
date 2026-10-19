package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ActionDescriptor describes module-owned management without host-specific pages.
type ActionDescriptor struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	ReadOnly     bool                   `json:"readOnly,omitempty"`
	Fields       map[string]ConfigField `json:"fields,omitempty"`
	Columns      map[string]string      `json:"columns,omitempty"`
	Confirmation string                 `json:"confirmation,omitempty"`
}

type ActionResult struct {
	Message string           `json:"message,omitempty"`
	Values  map[string]any   `json:"values,omitempty"`
	Items   []map[string]any `json:"items,omitempty"`
	// ConfigPatch is consumed by the host and never exposed to the browser.
	ConfigPatch map[string]any `json:"configPatch,omitempty"`
}

type ActionProvider interface {
	InvokeAction(context.Context, string, map[string]any) (ActionResult, error)
}

// Factory creates immutable configured instances, preserving the previous version
// when validation, initialization or persistence fails.
type Factory func(context.Context, Settings) (MetadataProvider, StreamProvider, ActionProvider, error)

func (m *Manifest) validateActions() error {
	if len(m.Actions) > 32 {
		return errors.New("模块管理操作不能超过 32 个")
	}
	seen := map[string]bool{}
	for _, action := range m.Actions {
		if !idRegex.MatchString(action.ID) || seen[action.ID] || strings.TrimSpace(action.Name) == "" {
			return errors.New("模块操作标识或名称无效")
		}
		seen[action.ID] = true
		if len(action.Fields) > 32 || len(action.Columns) > 32 {
			return errors.New("模块操作字段过多")
		}
		for key, field := range action.Fields {
			if key == "" || len(key) > 100 {
				return errors.New("模块操作字段标识无效")
			}
			switch field.Type {
			case "string", "password", "number", "boolean", "json":
			default:
				return errors.New("模块操作字段类型无效")
			}
			if len(field.Options) > 128 {
				return errors.New("模块操作选项过多")
			}
			if field.Secret && field.Default != nil {
				return errors.New("秘密字段不能声明默认值")
			}
		}
	}
	return nil
}

func (m *Manifest) Action(id string) (ActionDescriptor, bool) {
	for _, action := range m.Actions {
		if action.ID == id {
			return action, true
		}
	}
	return ActionDescriptor{}, false
}

func ValidateActionInput(action ActionDescriptor, input map[string]any) error {
	for key := range input {
		if _, ok := action.Fields[key]; !ok {
			return errors.New("操作包含未声明的字段")
		}
	}
	for key, field := range action.Fields {
		value, ok := input[key]
		if (!ok || value == nil || value == "") && field.Required {
			return fmt.Errorf("请填写%s", field.Label)
		}
		if !ok || value == nil {
			continue
		}
		valid := false
		switch field.Type {
		case "string", "password":
			_, valid = value.(string)
		case "number":
			_, valid = value.(float64)
		case "boolean":
			_, valid = value.(bool)
		case "json":
			valid = true
		}
		if !valid {
			return errors.New("操作字段类型不正确")
		}
		if len(field.Options) > 0 {
			found := false
			for _, o := range field.Options {
				if value == o.Value {
					found = true
				}
			}
			if !found {
				return errors.New("请选择有效选项")
			}
		}
	}
	return nil
}

func (c *HTTPModuleClient) InvokeAction(ctx context.Context, action string, input map[string]any) (ActionResult, error) {
	var result ActionResult
	err := c.requestJSON(ctx, http.MethodPost, "/api/v1/actions/"+url.PathEscape(action), map[string]any{"input": input}, &result)
	return result, err
}

func (m *Manager) InvokeAction(ctx context.Context, id, actionID, revision string, input map[string]any) (ActionResult, error) {
	entry, ok := m.registry.Get(id)
	if !ok || entry.ActionInstance == nil || entry.Status != StatusActive {
		return ActionResult{}, errors.New("模块未提供管理操作")
	}
	if revision == "" || revision != entry.Revision {
		return ActionResult{}, ErrRevisionChanged
	}
	action, ok := entry.Manifest.Action(actionID)
	if !ok {
		return ActionResult{}, errors.New("模块未声明此操作")
	}
	if err := ValidateActionInput(action, input); err != nil {
		return ActionResult{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := entry.ActionInstance.InvokeAction(callCtx, actionID, input)
	if err != nil {
		return ActionResult{}, errors.New("模块操作失败，请检查输入和模块连接")
	}
	current, ok := m.registry.Get(id)
	if !ok || current.Revision != revision {
		return ActionResult{}, ErrRevisionChanged
	}
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil || len(encoded) > 1<<20 || len(result.Items) > 1000 {
		return ActionResult{}, errors.New("模块操作结果过大")
	}
	if len(result.ConfigPatch) > 0 {
		if action.ReadOnly {
			return ActionResult{}, errors.New("只读操作不能更新模块配置")
		}
		state := m.Settings(id)
		if state.Config == nil {
			state.Config = map[string]any{}
		}
		for key, value := range result.ConfigPatch {
			if _, ok := entry.Manifest.ConfigSchema[key]; !ok {
				return ActionResult{}, errors.New("操作返回未声明的配置")
			}
			state.Config[key] = value
		}
		if err := m.updateConfigurationExpected(ctx, id, state, nil, nil, nil, revision); err != nil {
			return ActionResult{}, err
		}
	}
	result.ConfigPatch = nil
	// Return only declared display/edit fields, and never a declared secret.
	allowed, secrets := map[string]bool{}, map[string]bool{}
	for _, declaration := range entry.Manifest.Actions {
		for key, field := range declaration.Fields {
			if field.Secret || field.Type == "password" {
				secrets[key] = true
			} else {
				allowed[key] = true
			}
		}
		for key := range declaration.Columns {
			allowed[key] = true
		}
	}
	for key, field := range entry.Manifest.ConfigSchema {
		if field.Secret || field.Type == "password" {
			secrets[key] = true
		}
	}
	filter := func(values map[string]any) map[string]any {
		if values == nil {
			return nil
		}
		safe := map[string]any{}
		for key, value := range values {
			if allowed[key] && !secrets[key] {
				safe[key] = value
			}
		}
		return safe
	}
	result.Values = filter(result.Values)
	for i := range result.Items {
		result.Items[i] = filter(result.Items[i])
	}

	return result, nil
}
