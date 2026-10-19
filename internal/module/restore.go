package module

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RestoreRecord is server-only and contains secrets. Persist it encrypted.
// A nil manifest preserves settings for a module whose files were removed.
type RestoreRecord struct {
	ID       string    `json:"id"`
	Manifest *Manifest `json:"manifest,omitempty"`
	Settings Settings  `json:"settings"`
}

// RestoreStore is a durable handoff between a database restore and file/runtime
// reconciliation. A crash after commit leaves the record for the next scan.
type RestoreStore interface {
	PendingRestore(context.Context) ([]RestoreRecord, error)
	CompleteRestore(context.Context, string) error
	SaveRestore(context.Context, RestoreRecord) error
}

func (m *Manager) WithBackupSnapshot(fn func([]RestoreRecord) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	records := []RestoreRecord{}
	seen := map[string]bool{}
	for _, entry := range m.registry.List() {
		id := entry.Manifest.ID
		records = append(records, RestoreRecord{ID: id, Manifest: entry.Manifest, Settings: m.settingsFor(id)})
		seen[id] = true
	}
	for id, state := range m.settings {
		if !seen[id] {
			records = append(records, RestoreRecord{ID: id, Settings: state})
		}
	}
	// Clone settings too: consumers cannot mutate the manager through a snapshot.
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	var snapshot []RestoreRecord
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	return fn(snapshot)
}

// CommitRestore serializes database commit with configuration updates/scans.
// Only commit failures are returned as errors. After a successful commit,
// reconciliation failures are reported and retained in the durable queue.
func (m *Manager) CommitRestore(ctx context.Context, commit func() error) (ReloadReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	report := newReloadReport()
	if _, ok := m.store.(RestoreStore); !ok {
		return report, errors.New("模块存储不支持恢复")
	}
	if err := commit(); err != nil {
		return report, err
	}
	reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := m.restorePending(reloadCtx, &report)
	if err != nil {
		report.Errors["restore"] = "配置已恢复，模块重载等待重试"
	}
	return report, nil
}

func (m *Manager) checkRestorePending(ctx context.Context, id string) error {
	record, err := m.pendingRestore(ctx, id)
	if err != nil {
		return err
	}
	if record != nil {
		return errors.New("来源正在恢复，请从已有来源卡片修改设置")
	}
	return nil
}

func (m *Manager) pendingRestore(ctx context.Context, id string) (*RestoreRecord, error) {
	store, ok := m.store.(RestoreStore)
	if !ok {
		return nil, nil
	}
	records, err := store.PendingRestore(ctx)
	if err != nil {
		return nil, errors.New("暂时无法读取模块恢复状态")
	}
	for _, record := range records {
		if record.ID == id {
			return &record, nil
		}
	}
	return nil, nil
}

// Administrators may repair an address/credential or disable a pending source.
// The replacement remains durable even when the directory is still unavailable.
func (m *Manager) updatePendingConfiguration(ctx context.Context, record RestoreRecord, state Settings, transport *TransportConfig, mediaAccess *MediaAccess) error {
	manifest := record.Manifest
	if manifest == nil {
		return errors.New("缺少模块清单")
	}
	if m.builtin[record.ID] {
		if transport != nil || mediaAccess != nil {
			return errors.New("内置模块的连接由宿主管理")
		}
		entry, _ := m.registry.Get(record.ID)
		manifest = entry.Manifest
	} else {
		if transport != nil {
			manifest.Transport = *transport
		}
		if mediaAccess != nil {
			manifest.MediaAccess = mediaAccess
		}
		if manifest.Validate() != nil || manifest.Transport.Type != TransportHTTP {
			return errors.New("模块清单无效")
		}
	}
	normalized, err := validateConfig(manifest, state.Config, state.Enabled)
	if err != nil {
		return err
	}
	state.Config = normalized
	if err := ValidateLibraryScope(manifest, state.LibraryScope); err != nil {
		return err
	}
	if !m.builtin[record.ID] {
		candidate, err := m.prepare(ctx, manifest, state)
		if err != nil {
			return err
		}
		closeEntry(candidate)
	}
	record.Manifest, record.Settings = manifest, state
	if err := m.store.(RestoreStore).SaveRestore(ctx, record); err != nil {
		return errors.New("保存恢复中的来源配置失败")
	}
	m.settings[record.ID] = state
	report := newReloadReport()
	_, _ = m.restorePending(ctx, &report)
	return nil
}

func newReloadReport() ReloadReport {
	return ReloadReport{Loaded: []string{}, Updated: []string{}, Removed: []string{}, Errors: map[string]string{}}
}

// Called under m.mu. Pending IDs are excluded from normal scanning, so an old
// file can never reactivate with configuration from before the restore.
func (m *Manager) restorePending(ctx context.Context, report *ReloadReport) (map[string]bool, error) {
	pending := map[string]bool{}
	store, ok := m.store.(RestoreStore)
	if !ok {
		return pending, nil
	}
	records, err := store.PendingRestore(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		pending[record.ID] = true
	}
	// Invalidate all restored instances before any network preflight or file IO.
	for _, record := range records {
		if old, ok := m.registry.Get(record.ID); ok {
			if !m.builtin[record.ID] {
				closeEntry(old)
				m.registry.Remove(record.ID)
				m.syncEvents(record.ID)
			} else {
				_ = m.registry.SetStatus(record.ID, StatusDisabled, "配置恢复中")
			}
		}
		m.settings[record.ID] = record.Settings
	}
	for _, record := range records {
		if err := m.restoreOne(ctx, store, record); err != nil {
			report.Errors[record.ID] = "配置已恢复，来源重载失败；请检查连接和模块目录，系统会重试"
			if record.Manifest != nil && !m.builtin[record.ID] {
				_ = m.registry.Register(&ModuleEntry{Manifest: record.Manifest, Status: StatusError, LastError: report.Errors[record.ID]})
			} else {
				m.registry.SetError(record.ID, report.Errors[record.ID])
			}
			continue
		}
		delete(pending, record.ID)
		report.Updated = append(report.Updated, record.ID)
	}
	return pending, nil
}

func (m *Manager) restoreOne(ctx context.Context, store RestoreStore, record RestoreRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !idRegex.MatchString(record.ID) {
		return errors.New("模块 ID 无效")
	}
	if record.Manifest == nil {
		if _, exists := m.registry.Get(record.ID); exists {
			return errors.New("缺少模块清单")
		}
		return store.CompleteRestore(ctx, record.ID)
	}
	manifest := record.Manifest
	if manifest.ID != record.ID || manifest.Validate() != nil {
		return errors.New("恢复清单无效")
	}
	if m.builtin[record.ID] {
		if manifest.Transport.Type != TransportInProcess {
			return errors.New("不能替换内置模块")
		}
		if err := ValidateSettings(manifest, record.Settings); err != nil {
			return err
		}
		previous, _ := m.registry.Get(record.ID)
		entry, err := m.prepareBuiltin(ctx, previous, record.Settings)
		if err != nil {
			return err
		}
		if err := m.registry.Register(entry); err != nil {
			closeEntry(entry)
			return err
		}
		if err := store.CompleteRestore(ctx, record.ID); err != nil {
			return err
		}
		m.emitStatus(record.ID, string(entry.Status))
		return nil
	}
	if manifest.Transport.Type != TransportHTTP {
		return errors.New("恢复外部模块仅支持 HTTP")
	}
	if manifest.Runtime != nil {
		// Restores never carry executable files. Only reuse a locally installed release.
		dir, err := m.restoreDirectory(record.ID)
		if err != nil {
			return err
		}
		manifest.Directory = dir
	}
	candidate, err := m.prepare(ctx, manifest, record.Settings)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			closeEntry(candidate)
		}
	}()
	dir, err := m.restoreDirectory(record.ID)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := replaceManifest(filepath.Join(dir, "manifest.json"), encoded); err != nil {
		return err
	}
	if err := store.CompleteRestore(ctx, record.ID); err != nil {
		return err
	}
	candidate.Manifest.Directory = dir
	if err := m.registry.Register(candidate); err != nil {
		return err
	}
	canonical, _ := json.Marshal(manifest)
	for previousDir, old := range m.external {
		if old.id == record.ID {
			delete(m.external, previousDir)
		}
	}
	m.external[dir] = loadedModule{id: record.ID, fingerprint: sha256.Sum256(canonical)}
	m.syncEvents(record.ID)
	m.emitStatus(record.ID, string(candidate.Status))
	published = true
	return nil
}

// Never write through a linked directory or manifest supplied by an archive.
func (m *Manager) restoreDirectory(id string) (string, error) {
	if m.modulesDir == "" {
		return "", errors.New("模块目录未配置")
	}
	if err := os.MkdirAll(m.modulesDir, 0750); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(m.modulesDir)
	if err != nil {
		return "", err
	}
	dir := ""
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		candidateDir := filepath.Join(m.modulesDir, entry.Name())
		mf, err := LoadManifestFromDir(candidateDir)
		if err == nil && mf.ID == id {
			if dir != "" {
				return "", errors.New("多个目录使用相同模块 ID")
			}
			dir = candidateDir
		}
	}
	if dir == "" {
		dir = filepath.Join(m.modulesDir, id)
		if err := os.Mkdir(dir, 0750); err != nil {
			if !os.IsExist(err) {
				return "", err
			}
			contents, readErr := os.ReadDir(dir)
			if readErr != nil || len(contents) != 0 {
				return "", errors.New("模块目标目录已被其他文件或无效清单占用")
			}
		}
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("模块目录不能是链接")
	}
	root, err := filepath.EvalSymlinks(m.modulesDir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "." {
		return "", errors.New("模块目录越界")
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if info, err := os.Lstat(manifestPath); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return "", errors.New("模块清单不能是链接")
	}
	return dir, nil
}
