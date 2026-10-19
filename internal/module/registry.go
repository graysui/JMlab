package module

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var ErrRevisionChanged = errors.New("模块已更新或停用，请重试")

// WithActiveRevision serializes a short persistence operation with replacement
// and disable. Never perform network calls or re-enter the registry in fn.
func (r *Registry) WithActiveRevision(ctx context.Context, id, revision string, fn func() error) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry := r.modules[id]
	if entry == nil || entry.Status != StatusActive || entry.Revision != revision {
		return ErrRevisionChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

// ModuleStatus 模块当前的运行状态
type ModuleStatus string

const (
	StatusActive   ModuleStatus = "active"
	StatusDisabled ModuleStatus = "disabled"
	StatusError    ModuleStatus = "error"
)

// ModuleEntry 模块注册项封装
type ModuleEntry struct {
	ActionInstance   ActionProvider
	LibraryScope     *LibraryScope
	Capabilities     *MetadataCaps
	Health           HealthSnapshot
	loadError        string
	Revision         string
	Manifest         *Manifest
	Status           ModuleStatus
	LastError        string
	MetadataInstance MetadataProvider // 如果是 metadata/composite 类型
	StreamInstance   StreamProvider   // 如果是 stream/composite 类型
}

func (r *Registry) setHealth(id string, health HealthSnapshot, status ModuleStatus, message string, renew bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.modules[id]; entry != nil {
		if message == "" && entry.loadError != "" {
			message = entry.loadError
		}
		entry.Health, entry.Status, entry.LastError = health, status, message
		if renew {
			entry.Revision = rand.Text()
		}
	}
}

// Registry 模块注册中心
type Registry struct {
	mu      sync.RWMutex
	modules map[string]*ModuleEntry
}

func NewRegistry() *Registry {
	return &Registry{
		modules: make(map[string]*ModuleEntry),
	}
}

// Register 注册或更新模块
func (r *Registry) Register(entry *ModuleEntry) error {
	if entry == nil || entry.Manifest == nil {
		return errors.New("cannot register nil module entry or manifest")
	}
	if entry.ActionInstance == nil {
		if action, ok := entry.MetadataInstance.(ActionProvider); ok {
			entry.ActionInstance = action
		} else if action, ok := entry.StreamInstance.(ActionProvider); ok {
			entry.ActionInstance = action
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, err := copyEntry(entry)
	if err != nil {
		return err
	}
	snapshot.Revision = rand.Text()
	r.modules[entry.Manifest.ID] = snapshot
	return nil
}

// Get 根据模块 ID 获取模块
func (r *Registry) Get(id string) (*ModuleEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.modules[id]
	if !ok {
		return nil, false
	}
	snapshot, _ := copyEntry(entry)
	return snapshot, true
}

// List 列出所有注册的模块
func (r *Registry) List() []*ModuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*ModuleEntry, 0, len(r.modules))
	for _, entry := range r.modules {
		snapshot, _ := copyEntry(entry)
		list = append(list, snapshot)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Manifest.ID < list[j].Manifest.ID })
	return list
}

// ListActiveMetadataProviders 获取所有已启用的元数据提供模块
func (r *Registry) ListActiveMetadataProviders() []MetadataProvider {
	var list []MetadataProvider
	for _, entry := range r.List() {
		if entry.Status == StatusActive && entry.MetadataInstance != nil {
			list = append(list, entry.MetadataInstance)
		}
	}
	return list
}

// ListActiveStreamProviders 获取所有已启用的播放源提供模块
func (r *Registry) ListActiveStreamProviders() []StreamProvider {
	var list []StreamProvider
	for _, entry := range r.List() {
		if entry.Status == StatusActive && entry.StreamInstance != nil {
			list = append(list, entry.StreamInstance)
		}
	}
	return list
}

// SetStatus 更新模块状态
func (r *Registry) SetStatus(id string, status ModuleStatus, errMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.modules[id]
	if !ok {
		return fmt.Errorf("module %q not found", id)
	}
	if entry.Status != status {
		entry.Revision = rand.Text()
	}
	entry.Status = status
	entry.LastError = errMessage
	return nil
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.modules, id)
}

func (r *Registry) SetError(id, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.modules[id]; entry != nil {
		entry.loadError = message
		if message != "" || (entry.Health.State != "degraded" && entry.Health.State != "open") {
			entry.LastError = message
		}
	}
}

// Entries and manifests are snapshots. Callers cannot mutate registry state
// outside its lock while another request or hot reload reads it.
func copyEntry(entry *ModuleEntry) (*ModuleEntry, error) {
	data, err := json.Marshal(entry.Manifest)
	if err != nil {
		return nil, err
	}
	manifest := new(Manifest)
	if err := json.Unmarshal(data, manifest); err != nil {
		return nil, err
	}
	manifest.Directory = entry.Manifest.Directory
	snapshot := *entry
	snapshot.Manifest = manifest
	snapshot.LibraryScope = copyLibraryScope(entry.LibraryScope)
	if entry.Capabilities != nil {
		caps := *entry.Capabilities
		caps.SupportedTypes = append([]ItemType(nil), caps.SupportedTypes...)
		caps.AvailableRankingTypes = append([]string(nil), caps.AvailableRankingTypes...)
		snapshot.Capabilities = &caps
	}
	return &snapshot, nil
}

// SupportedTypes intersects a concrete runtime declaration with the manifest.
// Older modules may omit either declaration; the one present remains usable.
func (entry *ModuleEntry) SupportedTypes() []ItemType {
	declared := entry.Manifest.SupportedItemTypes
	if entry.Capabilities == nil || len(entry.Capabilities.SupportedTypes) == 0 {
		return append([]ItemType{}, declared...)
	}
	if len(declared) == 0 {
		return append([]ItemType(nil), entry.Capabilities.SupportedTypes...)
	}
	result := []ItemType{}
	for _, kind := range declared {
		for _, supported := range entry.Capabilities.SupportedTypes {
			if kind == supported {
				result = append(result, kind)
				break
			}
		}
	}
	return result
}
