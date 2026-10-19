package module

import (
	"context"
	"errors"
)

// SeedSettings imports compatibility configuration only before registration.
func (m *Manager) SeedSettings(ctx context.Context, id string, state Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.registry.Get(id); exists {
		return errors.New("模块已注册，不能导入初始配置")
	}
	if m.store != nil {
		if err := m.store.Save(ctx, id, state); err != nil {
			return err
		}
	}
	m.settings[id] = state
	return nil
}
