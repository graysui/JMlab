package module

import (
	"context"
	"errors"
	"time"
)

func (m *Manager) RegisterFactory(ctx context.Context, manifest *Manifest, factory Factory) error {
	if manifest == nil || factory == nil {
		return errors.New("模块清单和构造器不能为空")
	}
	state := m.Settings(manifest.ID)
	config, err := validateConfig(manifest, state.Config, state.Enabled)
	if err != nil {
		return err
	}
	state.Config = config
	meta, stream, actions, err := factory(ctx, state)
	if err != nil {
		return err
	}
	if err := m.RegisterInProcessModule(manifest, meta, stream); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, _ := m.registry.Get(manifest.ID)
	entry.ActionInstance = actions
	if err := m.registry.Register(entry); err != nil {
		return err
	}
	m.factories[manifest.ID] = factory
	return nil
}

// Called while manager configuration is serialized. Never mutate the old entry.
func (m *Manager) prepareBuiltin(ctx context.Context, previous *ModuleEntry, state Settings) (*ModuleEntry, error) {
	candidate := *previous
	if factory := m.factories[previous.Manifest.ID]; factory != nil {
		meta, stream, actions, err := factory(ctx, state)
		if err != nil {
			return nil, err
		}
		candidate.MetadataInstance, candidate.StreamInstance, candidate.ActionInstance = meta, stream, actions
		candidate.Capabilities = nil
		if meta != nil {
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			caps, err := meta.Caps(checkCtx)
			if err != nil {
				closeEntry(&candidate)
				return nil, errors.New("模块能力检查失败，原配置保持不变")
			}
			candidate.Capabilities = &caps
		}
	}
	candidate.LibraryScope = copyLibraryScope(state.LibraryScope)
	candidate.Status, candidate.LastError = StatusActive, ""
	if !state.Enabled {
		candidate.Status = StatusDisabled
	}
	return &candidate, nil
}
