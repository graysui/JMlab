package module

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const MaxPackageBytes int64 = 64 << 20
const maxExpandedBytes int64 = 256 << 20
const packageTTL = 15 * time.Minute

type PackagePreview struct {
	Token           string    `json:"packageToken"`
	Manifest        *Manifest `json:"manifest"`
	SHA256          string    `json:"sha256"`
	CompressedBytes int64     `json:"compressedBytes"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Revision        string    `json:"revision"`
}
type stagedPackage struct {
	PackagePreview
	owner, directory string
}

type PackageInstallOptions struct {
	PackageToken string         `json:"packageToken"`
	ModuleID     string         `json:"moduleId"`
	Revision     string         `json:"revision"`
	Enabled      *bool          `json:"enabled"`
	Config       map[string]any `json:"config"`
	ClearSecrets []string       `json:"clearSecrets"`
	MediaAccess  *MediaAccess   `json:"mediaAccess"`
}

// Paths are portable across supported servers, including Windows. Reject aliases,
// device names, alternate streams, traversal, symlinks and case-folded duplicates.
func safePackagePath(name string) bool {
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.TrimRight(part, ". ") != part || strings.ContainsAny(part, "<>\"|?*") {
			return false
		}
		for _, ch := range part {
			if ch < 32 {
				return false
			}
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func regularPackageFile(root, relative string) error {
	if !safePackagePath(relative) {
		return errors.New("包内路径无效")
	}
	current := root
	if info, err := os.Lstat(current); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("模块目录无效")
	}
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return errors.New("模块包不能引用链接或特殊文件")
		}
	}
	return nil
}

func (m *Manager) cleanupPackagesLocked() {
	if m.modulesDir == "" {
		return
	}
	live := map[string]bool{}
	for token, staged := range m.stagedPackages {
		if time.Now().After(staged.ExpiresAt) {
			_ = os.RemoveAll(staged.directory)
			delete(m.stagedPackages, token)
		} else {
			live[staged.directory] = true
		}
	}
	stagingRoot := filepath.Join(m.modulesDir, ".staging")
	if info, err := os.Lstat(stagingRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	if entries, err := os.ReadDir(stagingRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "package-") && !live[filepath.Join(stagingRoot, entry.Name())] {
				if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > packageTTL {
					_ = os.RemoveAll(filepath.Join(stagingRoot, entry.Name()))
				}
			}
		}
	}
}

func (m *Manager) CancelPackage(owner, token string) {
	m.packageMu.Lock()
	defer m.packageMu.Unlock()
	if staged := m.stagedPackages[token]; staged != nil && staged.owner == owner {
		_ = os.RemoveAll(staged.directory)
		delete(m.stagedPackages, token)
	}
}

// InspectPackage validates and stages files without running any package code.
func (m *Manager) InspectPackage(ctx context.Context, owner string, input io.Reader, expectedSHA256 string) (*PackagePreview, error) {
	if m.modulesDir == "" {
		return nil, errors.New("模块目录尚未配置")
	}
	if expectedSHA256 != "" {
		decoded, err := hex.DecodeString(expectedSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("SHA256 应为 64 位十六进制字符")
		}
	}
	m.packageMu.Lock()
	m.cleanupPackagesLocked()
	if len(m.stagedPackages)+m.stagingCount >= 4 {
		m.packageMu.Unlock()
		return nil, errors.New("待安装模块包过多，请先完成或取消已有导入")
	}
	m.stagingCount++
	m.packageMu.Unlock()
	defer func() { m.packageMu.Lock(); m.stagingCount--; m.packageMu.Unlock() }()
	stagingRoot := filepath.Join(m.modulesDir, ".staging")
	if err := os.MkdirAll(stagingRoot, 0700); err != nil {
		return nil, errors.New("无法创建模块暂存目录")
	}
	if info, err := os.Lstat(stagingRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("模块暂存目录不能是链接或特殊文件")
	}
	dir, err := os.MkdirTemp(stagingRoot, "package-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	file, err := os.OpenFile(filepath.Join(dir, "download.zip"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(&contextReader{ctx, input}, MaxPackageBytes+1))
	if err != nil {
		return nil, errors.New("读取模块包失败或操作已取消")
	}
	if size > MaxPackageBytes {
		return nil, errors.New("模块 ZIP 包不能超过 64 MB")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if expectedSHA256 != "" && !strings.EqualFold(digest, expectedSHA256) {
		return nil, errors.New("模块包 SHA256 不匹配，请核对下载地址和校验值")
	}
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return nil, errors.New("文件不是有效的 ZIP 模块包")
	}
	if len(archive.File) == 0 || len(archive.File) > 2048 {
		return nil, errors.New("模块包文件数量必须为 1～2048")
	}
	payload := filepath.Join(dir, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var total int64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if !safePackagePath(name) || entry.Mode()&os.ModeSymlink != 0 || (!entry.Mode().IsRegular() && !entry.FileInfo().IsDir()) {
			return nil, errors.New("模块包包含不安全路径、链接或特殊文件")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, errors.New("模块包包含重名文件")
		}
		seen[key] = true
		if entry.UncompressedSize64 > uint64(maxExpandedBytes-total) {
			return nil, errors.New("模块包解压后不能超过 256 MB")
		}
		target := filepath.Join(payload, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, errors.New("模块包文件路径冲突")
		}
		source, err := entry.Open()
		if err != nil {
			return nil, errors.New("无法读取模块包文件")
		}
		mode := os.FileMode(0600)
		if entry.Mode().Perm()&0111 != 0 {
			mode = 0700
		}
		destination, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			source.Close()
			return nil, errors.New("模块包文件路径冲突")
		}
		n, copyErr := io.Copy(destination, io.LimitReader(&contextReader{ctx, source}, maxExpandedBytes-total+1))
		closeErr := destination.Close()
		source.Close()
		total += n
		if copyErr != nil || closeErr != nil || total > maxExpandedBytes || uint64(n) != entry.UncompressedSize64 {
			return nil, errors.New("模块包损坏或解压大小超限")
		}
	}
	// Accept a manifest at the root or inside one enclosing directory.
	if _, err := os.Stat(filepath.Join(payload, "manifest.json")); os.IsNotExist(err) {
		entries, err := os.ReadDir(payload)
		if err == nil && len(entries) == 1 && entries[0].IsDir() {
			payload = filepath.Join(payload, entries[0].Name())
		}
	}
	manifestPath := filepath.Join(payload, "manifest.json")
	info, err := os.Stat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errors.New("模块包需要不超过 64 KB 的 manifest.json")
	}
	manifest, err := LoadManifestFromDir(payload)
	if err != nil {
		return nil, errors.New("模块包清单无效，请检查模块声明")
	}
	if manifest.Runtime == nil || manifest.Runtime.Release != "" || manifest.Transport.Type != TransportHTTP || manifest.Transport.URL != "" || manifest.Entrypoint != "" {
		return nil, errors.New("ZIP 清单需要 runtime 运行声明；请省略服务地址、release 和旧 entrypoint")
	}
	if manifest.Runtime.OS != runtime.GOOS || manifest.Runtime.Arch != runtime.GOARCH {
		return nil, fmt.Errorf("模块包适用于 %s/%s，当前服务器为 %s/%s", manifest.Runtime.OS, manifest.Runtime.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if err := regularPackageFile(payload, manifest.Runtime.Executable); err != nil {
		return nil, errors.New("模块包缺少 runtime.executable 指定的程序")
	}
	preview := PackagePreview{Token: rand.Text(), Manifest: manifest, SHA256: digest, CompressedBytes: size, ExpiresAt: time.Now().Add(packageTTL)}
	if existing, ok := m.registry.Get(manifest.ID); ok {
		preview.Revision = existing.Revision
	}
	m.packageMu.Lock()
	m.stagedPackages[preview.Token] = &stagedPackage{PackagePreview: preview, owner: owner, directory: dir}
	m.packageMu.Unlock()
	keep = true
	return &preview, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// InstallPackage publishes one complete manifest only after its executable passes
// preflight. Releases live at stable paths so updates work with Windows executables.
func (m *Manager) InstallPackage(ctx context.Context, owner string, input PackageInstallOptions) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.packageMu.Lock()
	defer m.packageMu.Unlock()
	m.cleanupPackagesLocked()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	staged := m.stagedPackages[input.PackageToken]
	if staged == nil || staged.owner != owner {
		return "", errors.New("模块包已过期或不可用，请重新导入")
	}
	manifest := *staged.Manifest
	r := *manifest.Runtime
	manifest.Runtime = &r
	id := manifest.ID
	if input.ModuleID != id {
		return "", errors.New("模块 ID 不匹配，请重新读取模块包")
	}
	previous, exists := m.registry.Get(id)
	if m.builtin[id] || (exists && previous.Manifest.Runtime == nil) {
		return "", errors.New("此 ID 已被内置模块或外部连接使用，请使用不同的模块 ID")
	}
	if (exists && (input.Revision == "" || input.Revision != previous.Revision)) || (!exists && input.Revision != "") {
		return "", ErrRevisionChanged
	}
	pending, err := m.pendingRestore(ctx, id)
	if err != nil {
		return "", err
	}
	state := Settings{Enabled: true, Config: map[string]any{}}
	if exists {
		state = m.settingsFor(id)
	}
	if pending != nil {
		state = pending.Settings
	}
	// Snapshot before merging: a failed update must not mutate saved settings.
	encodedState, _ := json.Marshal(state)
	state = Settings{}
	_ = json.Unmarshal(encodedState, &state)
	if input.Enabled != nil {
		state.Enabled = *input.Enabled
	}
	if input.MediaAccess != nil {
		manifest.MediaAccess = input.MediaAccess
	} else if exists && manifest.MediaAccess == nil {
		manifest.MediaAccess = previous.Manifest.MediaAccess
	}
	config := map[string]any{}
	for key, value := range state.Config {
		if _, ok := manifest.ConfigSchema[key]; ok {
			config[key] = value
		}
	}
	for key, value := range input.Config {
		field, ok := manifest.ConfigSchema[key]
		if !ok {
			return "", errors.New("存在未声明的模块配置字段")
		}
		if text, ok := value.(string); ok && text == "" && (field.Secret || field.Type == "password") {
			continue
		}
		config[key] = value
	}
	for _, key := range input.ClearSecrets {
		field, ok := manifest.ConfigSchema[key]
		if !ok || (!field.Secret && field.Type != "password") {
			return "", errors.New("只能清除已声明的秘密字段")
		}
		delete(config, key)
	}
	state.Config, state.Token = config, ""
	state.Config, err = validateConfig(&manifest, state.Config, state.Enabled)
	if err != nil {
		return "", err
	}
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	dir := filepath.Join(m.modulesDir, id)
	if exists && previous.Manifest.Directory != "" {
		dir = previous.Manifest.Directory
	}
	if info, err := os.Lstat(dir); err == nil && ((!exists && pending == nil) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", errors.New("模块目录已存在或不是普通目录")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".releases"), 0700); err != nil {
		return "", errors.New("无法创建模块安装目录")
	}
	if info, err := os.Lstat(filepath.Join(dir, ".releases")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("模块版本目录不能是链接或特殊文件")
	}
	manifest.Directory = dir
	r.Release = rand.Text()
	releaseDir := filepath.Join(dir, ".releases", r.Release)
	// Copy rather than move: failed preflights can be retried with the same token.
	if err := copyPackageTree(staged.Manifest.Directory, releaseDir); err != nil {
		_ = os.RemoveAll(releaseDir)
		return "", errors.New("复制模块包失败，请检查磁盘空间和目录权限")
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(releaseDir)
			if !exists {
				_ = os.Remove(filepath.Join(dir, ".releases"))
				_ = os.Remove(dir)
			}
		}
	}()
	candidate, err := m.prepare(ctx, &manifest, state)
	if err != nil {
		return "", err
	}
	defer func() {
		if !published {
			closeEntry(candidate)
		}
	}()
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := ctx.Err(); err != nil {
		return "", err
	}
	original, readErr := os.ReadFile(manifestPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", errors.New("无法读取原模块清单")
	}
	encoded, err := json.MarshalIndent(&manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := replaceManifest(manifestPath, encoded); err != nil {
		return "", errors.New("保存模块清单失败，原版本保持不变")
	}
	rollback := func() error {
		if original != nil {
			return replaceManifest(manifestPath, original)
		}
		return os.Remove(manifestPath)
	}
	if m.store != nil {
		if pending != nil {
			err = m.store.(RestoreStore).SaveRestore(ctx, RestoreRecord{ID: id, Manifest: &manifest, Settings: state})
		} else {
			err = m.store.Save(ctx, id, state)
		}
		if err != nil {
			if rollback() != nil {
				return "", errors.New("配置保存失败且原清单恢复失败，请检查模块目录权限")
			}
			return "", errors.New("保存模块配置失败，原版本保持不变")
		}
	}
	if pending != nil {
		if err := m.store.(RestoreStore).CompleteRestore(ctx, id); err != nil {
			_ = m.store.(RestoreStore).SaveRestore(ctx, *pending)
			if rollback() != nil {
				return "", errors.New("确认恢复失败且原清单恢复失败，请检查模块目录权限")
			}
			return "", errors.New("确认模块恢复失败，请重新导入")
		}
	}
	if err := m.registry.Register(candidate); err != nil {
		_ = rollback()
		return "", err
	}
	canonical, _ := json.Marshal(&manifest)
	m.external[dir] = loadedModule{id: id, fingerprint: sha256.Sum256(canonical)}
	m.settings[id] = state
	m.syncEvents(id)
	m.emitStatus(id, string(candidate.Status))
	closeEntry(previous)
	delete(m.runtimeRetries, id)
	published = true
	delete(m.stagedPackages, input.PackageToken)
	_ = os.RemoveAll(staged.directory)
	// Old executables are stopped before their files are removed.
	if releases, err := os.ReadDir(filepath.Join(dir, ".releases")); err == nil {
		for _, old := range releases {
			if old.IsDir() && releasePattern.MatchString(old.Name()) && old.Name() != r.Release {
				_ = os.RemoveAll(filepath.Join(dir, ".releases", old.Name()))
			}
		}
	}
	return id, nil
}

func copyPackageTree(source, destination string) error {
	return filepath.WalkDir(source, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filename)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("模块包不能包含链接")
		}
		in, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|(info.Mode().Perm()&0100))
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
