package module

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Settings contains server-side credentials and must never be returned directly by an API.
type Settings struct {
	LibraryScope *LibraryScope  `json:"libraryScope,omitempty"`
	Enabled      bool           `json:"enabled"`
	Config       map[string]any `json:"config"`
	Token        string         `json:"token"`
}

type StateStore interface {
	Load(context.Context) (map[string]Settings, error)
	Save(context.Context, string, Settings) error
}

type loadedModule struct {
	id          string
	fingerprint [32]byte
}
type ReloadReport struct {
	Loaded  []string          `json:"loaded"`
	Updated []string          `json:"updated"`
	Removed []string          `json:"removed"`
	Errors  map[string]string `json:"errors"`
}

type Manager struct {
	modulesDir         string
	registry           *Registry
	eventBus           *EventBus
	logger             *slog.Logger
	mu                 sync.Mutex
	builtin            map[string]bool
	factories          map[string]Factory
	external           map[string]loadedModule
	settings           map[string]Settings
	store              StateStore
	hostBridge         HostBridge
	eventSubscriptions map[string]func()
	watchMu            sync.Mutex
	probeMu            sync.Mutex
	cancel             context.CancelFunc
	done               chan struct{}
}

func NewManager(modulesDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{modulesDir: modulesDir, registry: NewRegistry(), eventBus: NewEventBus(logger), logger: logger,
		builtin: map[string]bool{}, factories: map[string]Factory{}, external: map[string]loadedModule{}, settings: map[string]Settings{}, eventSubscriptions: map[string]func(){}}
}
func (m *Manager) Registry() *Registry { return m.registry }
func (m *Manager) EventBus() *EventBus { return m.eventBus }

func (m *Manager) SetStateStore(ctx context.Context, store StateStore) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := store.Load(ctx)
	if err != nil {
		return err
	}
	m.store, m.settings = store, states
	if m.settings == nil {
		m.settings = map[string]Settings{}
	}
	for id, settings := range states {
		if _, ok := m.registry.Get(id); ok && !settings.Enabled {
			_ = m.registry.SetStatus(id, StatusDisabled, "")
		}
	}
	return nil
}

func (m *Manager) RegisterInProcessModule(manifest *Manifest, meta MetadataProvider, stream StreamProvider) error {
	if manifest == nil {
		return errors.New("module manifest is required")
	}
	candidate := *manifest
	candidate.Transport.Type = TransportInProcess
	if err := candidate.Validate(); err != nil {
		return err
	}
	var capabilities *MetadataCaps
	if meta != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		caps, err := meta.Caps(ctx)
		cancel()
		if err != nil {
			return errors.New("内置模块能力检查失败")
		}
		capabilities = &caps
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.registry.Get(candidate.ID); exists {
		return fmt.Errorf("模块 ID 已存在：%s", candidate.ID)
	}
	status := StatusActive
	if state, ok := m.settings[candidate.ID]; ok && !state.Enabled {
		status = StatusDisabled
	}
	state := m.settingsFor(candidate.ID)
	if err := ValidateLibraryScope(&candidate, state.LibraryScope); err != nil {
		return err
	}
	if err := m.registry.Register(&ModuleEntry{Manifest: &candidate, Status: status, Capabilities: capabilities, LibraryScope: state.LibraryScope, MetadataInstance: meta, StreamInstance: stream}); err != nil {
		return err
	}
	m.builtin[candidate.ID] = true
	m.emitStatus(candidate.ID, string(status))
	return nil
}

func (m *Manager) DiscoverAndLoad(ctx context.Context) error { _, err := m.Reload(ctx); return err }

// Reload builds and checks replacement clients before publishing an immutable registry
// entry. In-flight requests retain their old client; failed replacements preserve it.
func (m *Manager) Reload(ctx context.Context) (ReloadReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	report := newReloadReport()
	pending, err := m.restorePending(ctx, &report)
	if err != nil {
		return report, errors.New("读取待恢复模块失败，暂停扫描")
	}
	if m.modulesDir == "" {
		return report, nil
	}
	entries, err := os.ReadDir(m.modulesDir)
	if os.IsNotExist(err) && len(m.external) == 0 {
		return report, nil
	}
	if err != nil {
		return report, fmt.Errorf("读取模块目录失败: %w", err)
	}
	present := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			present[filepath.Join(m.modulesDir, entry.Name())] = true
		}
	}
	for dir, old := range m.external {
		if pending[old.id] {
			continue
		}
		if !present[dir] {
			if entry, ok := m.registry.Get(old.id); ok {
				closeEntry(entry)
			}
			m.registry.Remove(old.id)
			m.syncEvents(old.id)
			m.emitStatus(old.id, "removed")
			delete(m.external, dir)
			report.Removed = append(report.Removed, old.id)
		}
	}
	owners := map[string]string{}
	for dir, old := range m.external {
		owners[old.id] = dir
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		dir := filepath.Join(m.modulesDir, entry.Name())
		manifest, err := LoadManifestFromDir(dir)
		if err != nil {
			report.Errors[entry.Name()] = "模块清单无效，保留原版本"
			if old, ok := m.external[dir]; ok {
				m.registry.SetError(old.id, report.Errors[entry.Name()])
			}
			continue
		}
		if manifest.Transport.Type == TransportInProcess {
			continue
		}
		if pending[manifest.ID] {
			continue
		}
		if m.builtin[manifest.ID] || (owners[manifest.ID] != "" && owners[manifest.ID] != dir) {
			report.Errors[manifest.ID] = "模块 ID 冲突，未替换原模块"
			continue
		}
		old, exists := m.external[dir]
		if exists && old.id != manifest.ID {
			report.Errors[manifest.ID] = "更新时不能修改模块 ID，请使用新目录"
			continue
		}
		encoded, _ := json.Marshal(manifest)
		fingerprint := sha256.Sum256(encoded)
		if exists && old.fingerprint == fingerprint {
			m.registry.SetError(manifest.ID, "")
			continue
		}
		state := m.settingsFor(manifest.ID)
		candidate, err := m.prepare(ctx, manifest, state)
		if err != nil {
			report.Errors[manifest.ID] = err.Error()
			if exists {
				m.registry.SetError(manifest.ID, err.Error())
			} else {
				_ = m.registry.Register(&ModuleEntry{Manifest: manifest, Status: StatusError, LastError: err.Error()})
				m.external[dir] = loadedModule{id: manifest.ID}
				owners[manifest.ID] = dir
			}
			continue
		}
		previous, _ := m.registry.Get(manifest.ID)
		if err := m.registry.Register(candidate); err != nil {
			closeEntry(candidate)
			report.Errors[manifest.ID] = "模块清单不可编码"
			continue
		}
		m.syncEvents(manifest.ID)
		m.emitStatus(manifest.ID, string(candidate.Status))
		closeEntry(previous)
		m.external[dir] = loadedModule{id: manifest.ID, fingerprint: fingerprint}
		owners[manifest.ID] = dir
		if exists {
			report.Updated = append(report.Updated, manifest.ID)
		} else {
			report.Loaded = append(report.Loaded, manifest.ID)
		}
	}
	return report, nil
}

func (m *Manager) prepare(ctx context.Context, manifest *Manifest, state Settings) (*ModuleEntry, error) {
	if err := ValidateLibraryScope(manifest, state.LibraryScope); err != nil {
		return nil, err
	}
	config, err := validateConfig(manifest, state.Config, state.Enabled)
	if err != nil {
		return nil, err
	}
	if manifest.Transport.Type != TransportHTTP {
		return nil, fmt.Errorf("尚不支持外部传输类型 %s", manifest.Transport.Type)
	}
	client, err := NewHTTPModuleClientWithConfig(manifest, config, state.Token)
	if err != nil {
		return nil, err
	}
	if err := client.SetHostBridge(m.hostBridge); err != nil {
		client.Close()
		return nil, err
	}
	var capabilities *MetadataCaps
	if state.Enabled {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		caps, err := client.Caps(checkCtx)
		cancel()
		if err != nil {
			client.Close()
			return nil, errors.New("模块连接或能力检查失败，请检查地址、认证和服务状态")
		}
		if (manifest.Type == TypeStream || manifest.Type == TypeComposite) && !caps.SupportsStreams {
			client.Close()
			return nil, errors.New("模块未声明播放源能力")
		}
		capabilities = &caps
	}
	candidate := &ModuleEntry{Manifest: manifest, Status: StatusActive, Capabilities: capabilities, LibraryScope: copyLibraryScope(state.LibraryScope)}
	if !state.Enabled {
		candidate.Status = StatusDisabled
	}
	if manifest.Type == TypeMetadata || manifest.Type == TypeComposite {
		candidate.MetadataInstance = client
	}
	if manifest.Type == TypeStream || manifest.Type == TypeComposite {
		candidate.StreamInstance = client
	}
	candidate.Health = client.Health()
	client.onHealth = func(health HealthSnapshot) { m.observeHealth(manifest.ID, client, health) }
	return candidate, nil
}

func (m *Manager) settingsFor(id string) Settings {
	if state, ok := m.settings[id]; ok {
		return state
	}
	return Settings{Enabled: true, Config: map[string]any{}}
}
func (m *Manager) Settings(id string) Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.settingsFor(id)
	encoded, _ := json.Marshal(state)
	var snapshot Settings
	_ = json.Unmarshal(encoded, &snapshot)
	return snapshot
}

func (m *Manager) UpdateSettings(ctx context.Context, id string, state Settings) error {
	return m.UpdateConfiguration(ctx, id, state, nil, nil)
}

// UpdateConfiguration checks the whole replacement before publishing it. File
// watchers share this lock, so they never observe a partially saved update.
func (m *Manager) UpdateConfiguration(ctx context.Context, id string, state Settings, transport *TransportConfig, mediaAccess *MediaAccess) error {
	return m.updateConfiguration(ctx, id, state, transport, mediaAccess, nil)
}

// UpdateManifest preflights an external replacement and preserves existing credentials.
func (m *Manager) UpdateManifest(ctx context.Context, id string, manifest *Manifest) error {
	if manifest == nil || manifest.ID != id {
		return errors.New("更新时不能修改模块 ID")
	}
	return m.updateConfiguration(ctx, id, Settings{}, nil, nil, manifest)
}

func (m *Manager) updateConfiguration(ctx context.Context, id string, state Settings, transport *TransportConfig, mediaAccess *MediaAccess, replacement *Manifest) error {
	return m.updateConfigurationExpected(ctx, id, state, transport, mediaAccess, replacement, "")
}

func (m *Manager) updateConfigurationExpected(ctx context.Context, id string, state Settings, transport *TransportConfig, mediaAccess *MediaAccess, replacement *Manifest, expectedRevision string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending, err := m.pendingRestore(ctx, id)
	if err != nil {
		return err
	}
	if pending != nil {
		if replacement != nil {
			return errors.New("模块正在恢复，请等待恢复完成后再更新")
		}
		return m.updatePendingConfiguration(ctx, *pending, state, transport, mediaAccess)
	}
	previous, ok := m.registry.Get(id)
	if !ok {
		return fmt.Errorf("模块不存在")
	}
	if expectedRevision != "" && previous.Revision != expectedRevision {
		return ErrRevisionChanged
	}
	manifest := previous.Manifest
	definitionChanged := transport != nil || mediaAccess != nil || replacement != nil
	if definitionChanged {
		if m.builtin[id] {
			return errors.New("内置模块的连接由宿主管理")
		}
		if replacement != nil {
			manifest = replacement
			state = m.settingsFor(id)
		}
		if transport != nil {
			manifest.Transport = *transport
		}
		if mediaAccess != nil {
			manifest.MediaAccess = mediaAccess
		}
		if err := manifest.Validate(); err != nil {
			return err
		}
		if manifest.Transport.Type != TransportHTTP {
			return errors.New("只支持 HTTP 模块连接")
		}
	}
	normalized, err := validateConfig(manifest, state.Config, state.Enabled)
	if err != nil {
		return err
	}
	state.Config = normalized
	state.LibraryScope = copyLibraryScope(state.LibraryScope)
	if err := ValidateLibraryScope(manifest, state.LibraryScope); err != nil {
		return err
	}
	candidate := previous
	if !m.builtin[id] {
		candidate, err = m.prepare(ctx, manifest, state)
		if err != nil {
			return err
		}
	} else {
		candidate, err = m.prepareBuiltin(ctx, previous, state)
		if err != nil {
			return err
		}
	}
	var original []byte
	var manifestPath string
	if definitionChanged {
		for dir, entry := range m.external {
			if entry.id == id {
				manifestPath = filepath.Join(dir, "manifest.json")
				break
			}
		}
		if manifestPath == "" {
			closeEntry(candidate)
			return errors.New("找不到模块清单目录")
		}
		original, err = os.ReadFile(manifestPath)
		if err != nil {
			closeEntry(candidate)
			return errors.New("无法读取原模块清单")
		}
		encoded, encodeErr := json.MarshalIndent(manifest, "", "  ")
		if encodeErr != nil {
			closeEntry(candidate)
			return errors.New("模块清单无法编码")
		}
		if err := replaceManifest(manifestPath, encoded); err != nil {
			closeEntry(candidate)
			return errors.New("保存模块连接失败，原配置保持不变")
		}
	}
	if m.store != nil {
		if err := m.store.Save(ctx, id, state); err != nil {
			if !m.builtin[id] || m.factories[id] != nil {
				closeEntry(candidate)
			}
			if definitionChanged {
				if err := replaceManifest(manifestPath, original); err != nil {
					return errors.New("配置保存失败且清单恢复失败，请检查模块目录权限")
				}
			}
			return errors.New("保存模块配置失败，原配置保持不变")
		}
	}
	if err := m.registry.Register(candidate); err != nil {
		return err
	}
	m.settings[id] = state
	m.syncEvents(id)
	m.emitStatus(id, string(candidate.Status))
	if definitionChanged {
		canonical, _ := json.Marshal(manifest)
		m.external[filepath.Dir(manifestPath)] = loadedModule{id: id, fingerprint: sha256.Sum256(canonical)}
	}
	if !m.builtin[id] || m.factories[id] != nil {
		closeEntry(previous)
	}
	return nil
}
func (m *Manager) EnableModule(id string) error {
	state := m.Settings(id)
	state.Enabled = true
	return m.UpdateSettings(context.Background(), id, state)
}
func (m *Manager) DisableModule(id string) error {
	state := m.Settings(id)
	state.Enabled = false
	return m.UpdateSettings(context.Background(), id, state)
}

func (m *Manager) StartWatcher(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("模块扫描间隔必须大于零")
	}
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	if m.cancel != nil {
		return errors.New("模块监控已启动")
	}
	watchCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		healthDone := make(chan struct{})
		go func() {
			defer close(healthDone)
			ticker := time.NewTicker(healthInterval)
			defer ticker.Stop()
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
					m.CheckHealth(watchCtx)
				}
			}
		}()
		defer func() { cancel(); <-healthDone }()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				report, err := m.Reload(watchCtx)
				if err != nil && watchCtx.Err() == nil {
					m.logger.Warn("模块目录扫描失败")
				}
				if len(report.Loaded)+len(report.Updated)+len(report.Removed) > 0 {
					m.logger.Info("模块热加载完成", "loaded", report.Loaded, "updated", report.Updated, "removed", report.Removed)
				}
			}
		}
	}()
	return nil
}
func (m *Manager) Stop(ctx context.Context) error {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	if m.cancel != nil {
		m.cancel()
		select {
		case <-m.done:
			m.cancel = nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, entry := range m.registry.List() {
		closeEntry(entry)
	}
	return m.eventBus.Close(ctx)
}
func closeEntry(entry *ModuleEntry) {
	if entry == nil {
		return
	}
	if client, ok := entry.MetadataInstance.(*HTTPModuleClient); ok {
		client.Close()
	}
	if client, ok := entry.StreamInstance.(*HTTPModuleClient); ok {
		client.Close()
	}
}

func ValidateConfig(manifest *Manifest, input map[string]any) (map[string]any, error) {
	return validateConfig(manifest, input, true)
}

func ValidateSettings(manifest *Manifest, state Settings) error {
	if err := ValidateLibraryScope(manifest, state.LibraryScope); err != nil {
		return err
	}
	_, err := validateConfig(manifest, state.Config, state.Enabled)
	return err
}

func validateConfig(manifest *Manifest, input map[string]any, required bool) (map[string]any, error) {
	result := map[string]any{}
	for key := range input {
		if _, ok := manifest.ConfigSchema[key]; !ok {
			return nil, fmt.Errorf("未知配置字段：%s", key)
		}
	}
	for name, field := range manifest.ConfigSchema {
		value, ok := input[name]
		if !ok {
			value = field.Default
		}
		missing := value == nil
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			missing = true
		}
		if missing {
			if field.Required && required {
				return nil, fmt.Errorf("请填写：%s", field.Label)
			}
			continue
		}
		valid := false
		switch field.Type {
		case "string", "password":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "number":
			switch value.(type) {
			case float64, int, int64, json.Number:
				valid = true
			}
		case "json":
			_, err := json.Marshal(value)
			valid = err == nil
		}
		if !valid {
			return nil, fmt.Errorf("配置字段类型不正确：%s", field.Label)
		}
		if len(field.Options) > 0 {
			found := false
			for _, option := range field.Options {
				if value == option.Value {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("请选择有效选项：%s", field.Label)
			}
		}
		result[name] = value
	}
	return result, nil
}
