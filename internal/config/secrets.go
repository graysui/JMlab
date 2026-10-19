package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maximumSecretFileBytes = 1024 * 1024

type systemSecret struct {
	name     string
	filename string
	value    string
	explicit bool
	saved    bool
}

// loadSystemSecrets imports explicit keys unchanged, or generates keys only for
// a demonstrably new database. Keeping keys with the data makes ordinary
// restarts independent of environment variables, without replacing old keys.
func (c *Config) loadSystemSecrets() error {
	directory := filepath.Join(c.DataDir, "keys")
	if err := checkSecretDirectory(directory); err != nil {
		return err
	}
	secrets := []systemSecret{
		{name: "JMLAB_MASTER_KEY", filename: "master.key", value: c.MasterKey, explicit: c.MasterKey != ""},
		{name: "JMLAB_SESSION_SECRET", filename: "session.secret", value: c.SessionSecret, explicit: c.SessionSecret != ""},
	}
	// Read and validate everything before persisting either key. A malformed
	// companion key must not cause a partial import or a replacement master key.
	for i := range secrets {
		secret := &secrets[i]
		if secret.explicit {
			if err := validateStoredSecret(secret.name, secret.value); err != nil {
				return err
			}
		}
		saved, exists, err := readSecret(filepath.Join(directory, secret.filename), secret.name)
		if err != nil {
			return err
		}
		secret.saved = exists
		if exists {
			if secret.explicit && secret.value != saved {
				return fmt.Errorf("%s 与已保存的系统密钥不一致，请使用原密钥；不会覆盖密钥文件", secret.name)
			}
			secret.value = saved
		}
	}
	if secrets[0].value == "" {
		if err := c.requireNewDatabase(); err != nil {
			return err
		}
	}
	for i := range secrets {
		if secrets[i].value == "" {
			value, err := randomSecret(rand.Reader)
			if err != nil {
				return fmt.Errorf("生成 %s 失败: %w", secrets[i].name, err)
			}
			secrets[i].value = value
		}
	}
	syncDirectories, err := secretDirectorySyncPaths(directory)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建系统密钥目录失败: %w", err)
	}
	if err := checkSecretDirectory(directory); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("设置系统密钥目录权限失败: %w", err)
	}
	for i := range secrets {
		secret := &secrets[i]
		if secret.saved {
			if err := os.Chmod(filepath.Join(directory, secret.filename), 0o600); err != nil {
				return fmt.Errorf("设置 %s 密钥文件权限失败: %w", secret.name, err)
			}
			continue
		}
		value, err := persistSecret(directory, *secret)
		if err != nil {
			return err
		}
		secret.value = value
	}
	// File.Sync protects key contents; directory Sync protects the published
	// filenames before the repository is allowed to create durable data.
	for _, path := range syncDirectories {
		if err := syncSecretDirectory(path); err != nil {
			return err
		}
	}
	c.MasterKey = secrets[0].value
	c.SessionSecret = secrets[1].value
	return nil
}

func secretDirectorySyncPaths(directory string) ([]string, error) {
	if runtime.GOOS == "windows" {
		// Windows does not support syncing directories through os.File.Sync.
		return nil, nil
	}
	paths := []string{directory}
	// Also sync each parent we may create, including the existing ancestor that
	// receives the first new directory entry when DataDir itself is new.
	for parent := filepath.Dir(directory); ; parent = filepath.Dir(parent) {
		paths = append(paths, parent)
		if _, err := os.Stat(parent); err == nil {
			return paths, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("检查系统密钥父目录失败: %w", err)
		}
		if filepath.Dir(parent) == parent {
			return nil, errors.New("找不到可用的系统密钥父目录")
		}
	}
}

func syncSecretDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开系统密钥目录以持久保存失败: %w", err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步系统密钥目录失败；数据库尚未打开: %w", err)
	}
	return nil
}

func checkSecretDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取系统密钥目录失败: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("系统密钥目录必须是普通目录，不能是符号链接")
	}
	return nil
}

func validateStoredSecret(name, value string) error {
	if err := validateSecret(name, value); err != nil {
		return err
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s 不能包含换行，请恢复原始系统密钥；不会自动替换", name)
	}
	if len(value) > maximumSecretFileBytes {
		return fmt.Errorf("%s 超出系统密钥文件大小限制", name)
	}
	return nil
}

func readSecret(path, name string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取 %s 密钥文件失败: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s 密钥文件必须是普通文件，不能是目录或符号链接", name)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("打开 %s 密钥文件失败: %w", name, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return "", false, fmt.Errorf("%s 密钥文件读取期间发生变化，请检查后重试", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumSecretFileBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("读取 %s 密钥文件失败: %w", name, err)
	}
	value := string(data)
	if err := validateStoredSecret(name, value); err != nil {
		return "", false, fmt.Errorf("已保存的系统密钥无效: %w", err)
	}
	return value, true, nil
}

func randomSecret(source io.Reader) (string, error) {
	var bytes [minimumSecretByteLength]byte
	if _, err := io.ReadFull(source, bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func persistSecret(directory string, secret systemSecret) (string, error) {
	file, err := os.CreateTemp(directory, ".key-*")
	if err != nil {
		return "", fmt.Errorf("创建 %s 临时密钥文件失败: %w", secret.name, err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("设置 %s 密钥文件权限失败: %w", secret.name, err)
	}
	if _, err := io.WriteString(file, secret.value); err != nil {
		return "", fmt.Errorf("写入 %s 密钥文件失败: %w", secret.name, err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("保存 %s 密钥文件失败: %w", secret.name, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("关闭 %s 密钥文件失败: %w", secret.name, err)
	}
	// Linking a complete, synced file publishes it atomically without ever
	// replacing another start's key or leaving a partially written final file.
	path := filepath.Join(directory, secret.filename)
	if err := os.Link(file.Name(), path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("持久保存 %s 失败（文件系统需支持硬链接）: %w", secret.name, err)
		}
		winner, exists, readErr := readSecret(path, secret.name)
		if readErr != nil {
			return "", readErr
		}
		if !exists || (secret.explicit && winner != secret.value) {
			return "", fmt.Errorf("%s 在保存期间发生冲突；不会覆盖原密钥", secret.name)
		}
		return winner, nil
	}
	return secret.value, nil
}

func (c Config) requireNewDatabase() error {
	isNew, err := c.isNewSQLiteDatabase()
	if err != nil {
		return fmt.Errorf("无法确认数据库为全新安装，请首次提供 JMLAB_MASTER_KEY（旧部署必须使用原密钥），之后将自动保存: %w", err)
	}
	if isNew {
		return nil
	}
	return errors.New("检测到已有 SQLite 数据库，但未找到已保存的主密钥；请首次提供原 JMLAB_MASTER_KEY，或恢复数据目录中的 keys/master.key；不会生成替代密钥")
}

func (c Config) isNewSQLiteDatabase() (bool, error) {
	path, inMemory, err := sqliteDatabasePath(c.DatabaseURL)
	if err != nil {
		return false, err
	}
	if inMemory {
		return true, nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, fmt.Errorf("无法检查 SQLite 数据库: %w", err)
	}
	return false, nil
}

// Match repository's accepted SQLite DSNs while treating unknown URI forms
// conservatively. Query parameters are never part of the filesystem path.
func sqliteDatabasePath(databaseURL string) (path string, inMemory bool, err error) {
	dsn := strings.TrimSpace(databaseURL)
	if strings.HasPrefix(dsn, "sqlite://") {
		dsn = strings.TrimPrefix(dsn, "sqlite://")
	} else if !strings.HasPrefix(dsn, "file:") {
		return "", false, errors.New("该数据库不支持自动判断是否已有数据")
	}
	// The driver selects a VFS from every DSN's query, including bare SQLite
	// filenames. A custom VFS may store data somewhere other than the local path.
	_, rawQuery, _ := strings.Cut(dsn, "?")
	query, parseErr := url.ParseQuery(rawQuery)
	if parseErr != nil {
		return "", false, errors.New("SQLite 数据库查询参数无效")
	}
	if _, customized := query["vfs"]; customized {
		return "", false, errors.New("自定义 SQLite VFS 无法自动判断是否已有数据")
	}
	if strings.HasPrefix(dsn, "file:") {
		parsed, parseErr := url.Parse(dsn)
		if parseErr != nil || (parsed.Host != "" && parsed.Host != "localhost") || parsed.Fragment != "" || parsed.User != nil {
			return "", false, errors.New("无法安全解析 SQLite 文件 URI")
		}
		if modes := query["mode"]; len(modes) == 1 && modes[0] == "memory" {
			return "", true, nil
		}
		path = parsed.Path
		if parsed.Opaque != "" {
			path, parseErr = url.PathUnescape(parsed.Opaque)
			if parseErr != nil {
				return "", false, errors.New("SQLite 文件 URI 路径无效")
			}
		}
	} else {
		path, _, _ = strings.Cut(dsn, "?")
	}
	if path == ":memory:" {
		return "", true, nil
	}
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return "", false, errors.New("SQLite 数据库路径无效")
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), false, nil
}
