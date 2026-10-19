package module

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Uninstall removes a managed HTTP connection, never media or independently managed programs.
func (m *Manager) Uninstall(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.builtin[id] {
		return errors.New("内置模块不能卸载，可以随时停用")
	}
	if err := m.checkRestorePending(ctx, id); err != nil {
		return err
	}
	previous, ok := m.registry.Get(id)
	if !ok {
		return errors.New("模块不存在")
	}
	var directory string
	for dir, entry := range m.external {
		if entry.id == id {
			directory = dir
			break
		}
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 || files[0].Name() != "manifest.json" {
		return errors.New("此模块目录还含有其他文件，请先停用，再由服务器维护者整理目录")
	}
	var removeState func(context.Context, string) error
	if m.store != nil {
		store, ok := m.store.(interface {
			Delete(context.Context, string) error
		})
		if !ok {
			return errors.New("当前配置存储不支持卸载，请先停用模块")
		}
		removeState = store.Delete
	}
	path := filepath.Join(directory, "manifest.json")
	temporary := filepath.Join(m.modulesDir, ".uninstall-"+rand.Text())
	if err := os.Rename(path, temporary); err != nil {
		return errors.New("无法移除模块清单，原模块保持不变")
	}
	if removeState != nil {
		if err := removeState(ctx, id); err != nil {
			if restoreErr := os.Rename(temporary, path); restoreErr != nil {
				return errors.New("卸载失败且清单恢复失败，请检查模块目录权限")
			}
			return errors.New("配置移除失败，原模块保持不变")
		}
	}
	m.registry.Remove(id)
	m.syncEvents(id)
	m.emitStatus(id, "removed")
	closeEntry(previous)
	delete(m.external, directory)
	delete(m.settings, id)
	_ = os.Remove(temporary)
	_ = os.Remove(directory) // Only removes an empty directory.
	return nil
}

// Install stores only a manifest for an independently running HTTP module.
// Executables and arbitrary entrypoint commands are never started by the host.
func (m *Manager) Install(ctx context.Context, manifest *Manifest, state Settings) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if manifest.Transport.Type != TransportHTTP {
		return errors.New("只支持安装 HTTP 模块")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkRestorePending(ctx, manifest.ID); err != nil {
		return err
	}
	if m.modulesDir == "" {
		return errors.New("模块目录尚未配置")
	}
	if _, exists := m.registry.Get(manifest.ID); exists || m.builtin[manifest.ID] {
		return errors.New("模块 ID 已存在")
	}
	config, err := validateConfig(manifest, state.Config, state.Enabled)
	if err != nil {
		return err
	}
	state.Config = config
	state.LibraryScope = copyLibraryScope(state.LibraryScope)
	candidate, err := m.prepare(ctx, manifest, state)
	if err != nil {
		return err
	}
	keepClient := false
	defer func() {
		if !keepClient {
			closeEntry(candidate)
		}
	}()
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New("模块清单无法编码")
	}
	if err := os.MkdirAll(m.modulesDir, 0750); err != nil {
		return errors.New("无法创建模块目录")
	}
	dir := filepath.Join(m.modulesDir, manifest.ID)
	if err := os.Mkdir(dir, 0750); err != nil {
		return errors.New("模块目录已存在或无法创建")
	}
	keepDirectory := false
	defer func() {
		if !keepDirectory {
			_ = os.Remove(filepath.Join(dir, "manifest.json"))
			_ = os.Remove(dir)
		}
	}()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), encoded, 0600); err != nil {
		return errors.New("保存模块清单失败")
	}
	if m.store != nil {
		if err := m.store.Save(ctx, manifest.ID, state); err != nil {
			return errors.New("保存模块配置失败")
		}
	}
	candidate.Manifest.Directory = dir
	if err := m.registry.Register(candidate); err != nil {
		return err
	}
	canonical, _ := json.Marshal(manifest)
	m.external[dir] = loadedModule{id: manifest.ID, fingerprint: sha256.Sum256(canonical)}
	m.settings[manifest.ID] = state
	m.syncEvents(manifest.ID)
	m.emitStatus(manifest.ID, string(candidate.Status))
	keepClient, keepDirectory = true, true
	return nil
}
