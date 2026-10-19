package config

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func loadTestEnvironment(t *testing.T) string {
	t.Helper()
	for _, suffix := range []string{
		"HTTP_ADDR", "DATA_DIR", "DATABASE_URL", "MIGRATIONS_DIR", "WEB_DIR", "MODULES_DIR",
		"MASTER_KEY", "PUBLIC_URL", "SESSION_SECRET", "BOOTSTRAP_ADMIN_USERNAME",
		"BOOTSTRAP_ADMIN_PASSWORD", "ENV", "LOG_LEVEL", "SHUTDOWN_TIMEOUT",
	} {
		t.Setenv("JMLAB_"+suffix, "")
		t.Setenv("JAVLAB_"+suffix, "")
	}
	directory := t.TempDir()
	t.Setenv("JMLAB_DATA_DIR", directory)
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD", "123456")
	return directory
}

func TestLoadGeneratesAndReusesSystemSecrets(t *testing.T) {
	directory := loadTestEnvironment(t)
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if first.MasterKey == first.SessionSecret {
		t.Fatal("independent keys must differ")
	}
	for filename, expected := range map[string]string{
		"master.key": first.MasterKey, "session.secret": first.SessionSecret,
	} {
		if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != 32 {
			t.Fatalf("%s is not a 32-byte random hex key", filename)
		}
		path := filepath.Join(directory, "keys", filename)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("%s was not persisted exactly: %v", filename, err)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("%s does not have private file permissions", filename)
			}
		}
	}
	writeTestFile(t, filepath.Join(directory, "jmlab.db"), "existing database fixture")
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD", "")
	restarted, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if restarted.MasterKey != first.MasterKey || restarted.SessionSecret != first.SessionSecret {
		t.Fatal("restart replaced persistent keys")
	}
	if restarted.BootstrapAdminUsername != "" || restarted.BootstrapAdminPassword != "" {
		t.Fatal("existing installation unexpectedly requires bootstrap credentials")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(directory, "keys"))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatal("key directory does not have private permissions")
		}
	}
}

func TestLoadDefaultsBootstrapUsernameAndRequiresFirstPassword(t *testing.T) {
	directory := loadTestEnvironment(t)
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "首次启动") {
		t.Fatalf("missing first password should give a setup error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "keys")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid first setup wrote keys")
	}
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD", "12345")
	if _, err := Load(); err == nil {
		t.Fatal("five-character password was accepted")
	}
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD", "123456")
	cfg, err := Load()
	if err != nil || cfg.BootstrapAdminUsername != "admin" {
		t.Fatalf("password-only setup should use admin: %v", err)
	}
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_USERNAME", "owner")
	cfg, err = Load()
	if err != nil || cfg.BootstrapAdminUsername != "owner" {
		t.Fatalf("explicit username was not preserved: %v", err)
	}
}

func TestLoadImportsLegacySecretsAndAllowsRemovingEnvironment(t *testing.T) {
	directory := loadTestEnvironment(t)
	master := " " + strings.Repeat("legacy-master", 4) + " "
	session := strings.Repeat("legacy-session", 4)
	writeTestFile(t, filepath.Join(directory, "jmlab.db"), "existing database fixture")
	for _, name := range []string{"JMLAB_MASTER_KEY", "JMLAB_SESSION_SECRET"} {
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("JAVLAB_MASTER_KEY", master)
	t.Setenv("JAVLAB_SESSION_SECRET", session)
	imported, err := Load()
	if err != nil || imported.MasterKey != master || imported.SessionSecret != session {
		t.Fatalf("legacy keys should be imported byte-for-byte: %v", err)
	}
	t.Setenv("JMLAB_MASTER_KEY", "")
	t.Setenv("JMLAB_SESSION_SECRET", "")
	restarted, err := Load()
	if err != nil || restarted.MasterKey != master || restarted.SessionSecret != session {
		t.Fatalf("imported keys were not reused after removing environment: %v", err)
	}
}

func TestLoadRefusesExistingDatabasesWithoutOriginalMasterKey(t *testing.T) {
	for _, kind := range []string{"default", "sqlite-query", "file-uri", "wrapped-file-uri"} {
		t.Run(kind, func(t *testing.T) {
			directory := loadTestEnvironment(t)
			path := filepath.Join(directory, "custom database.db")
			switch kind {
			case "default":
				path = filepath.Join(directory, "jmlab.db")
			case "sqlite-query":
				t.Setenv("JMLAB_DATABASE_URL", "sqlite://"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
			case "file-uri", "wrapped-file-uri":
				uriPath := filepath.ToSlash(path)
				if runtime.GOOS == "windows" {
					uriPath = "/" + uriPath
				}
				uri := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=rwc&_pragma=foreign_keys(1)"}).String()
				if kind == "wrapped-file-uri" {
					uri = "sqlite://" + uri
				}
				t.Setenv("JMLAB_DATABASE_URL", uri)
			}
			writeTestFile(t, path, "existing database fixture")
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "原 JMLAB_MASTER_KEY") {
				t.Fatalf("existing database was not protected: %v", err)
			}
			if _, err := os.Stat(filepath.Join(directory, "keys")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failure generated replacement keys")
			}
		})
	}
}

func TestLoadRequiresExplicitMasterKeyForUnknownDatabaseState(t *testing.T) {
	for _, dsn := range []string{
		"postgres://example.invalid/database", "postgresql://example.invalid/database",
		"file://other-host/data.db", "file:data.db?vfs=custom", "unsupported://database",
		"sqlite://absent.db?vfs=custom", "sqlite://:memory:?vfs=custom",
	} {
		t.Run(dsn, func(t *testing.T) {
			directory := loadTestEnvironment(t)
			t.Setenv("JMLAB_DATABASE_URL", dsn)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "首次提供 JMLAB_MASTER_KEY") {
				t.Fatalf("unconfirmed database state was not protected: %v", err)
			}
			if _, err := os.Stat(filepath.Join(directory, "keys")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failure generated replacement keys")
			}
		})
	}
	loadTestEnvironment(t)
	t.Setenv("JMLAB_DATABASE_URL", "postgres://example.invalid/database")
	t.Setenv("JMLAB_MASTER_KEY", strings.Repeat("original-key", 4))
	t.Setenv("JMLAB_BOOTSTRAP_ADMIN_PASSWORD", "")
	if _, err := Load(); err != nil {
		t.Fatalf("explicit master key should enable advanced database configuration: %v", err)
	}
}

func TestLoadRefusesInvalidKeysWithoutChangingFiles(t *testing.T) {
	for _, content := range []string{"", "short", strings.Repeat("valid", 8) + "\n", strings.Repeat("x", maximumSecretFileBytes+1)} {
		t.Run("invalid-saved", func(t *testing.T) {
			directory := loadTestEnvironment(t)
			path := filepath.Join(directory, "keys", "master.key")
			writeTestFile(t, path, content)
			t.Setenv("JMLAB_MASTER_KEY", strings.Repeat("explicit", 5))
			if _, err := Load(); err == nil {
				t.Fatal("invalid saved key was accepted")
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != content {
				t.Fatal("invalid saved key was overwritten")
			}
			if _, err := os.Stat(filepath.Join(directory, "keys", "session.secret")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failure persisted a partial key pair")
			}
		})
	}
	for _, content := range []string{"short", strings.Repeat("valid", 8) + "\r"} {
		t.Run("invalid-explicit", func(t *testing.T) {
			directory := loadTestEnvironment(t)
			t.Setenv("JMLAB_SESSION_SECRET", content)
			if _, err := Load(); err == nil {
				t.Fatal("invalid explicit key was accepted")
			}
			if _, err := os.Stat(filepath.Join(directory, "keys")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid configuration persisted keys")
			}
		})
	}
}

func TestLoadRefusesKeyConflictAndNonRegularFiles(t *testing.T) {
	directory := loadTestEnvironment(t)
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JMLAB_MASTER_KEY", strings.Repeat("different", 5))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("conflicting explicit key was accepted: %v", err)
	}
	actual, err := os.ReadFile(filepath.Join(directory, "keys", "master.key"))
	if err != nil || string(actual) != first.MasterKey {
		t.Fatal("conflict overwrote original master key")
	}
	for _, filename := range []string{"keys", "keys/master.key", "keys/session.secret"} {
		t.Run(filename, func(t *testing.T) {
			directory := loadTestEnvironment(t)
			path := filepath.Join(directory, filepath.FromSlash(filename))
			if filename == "keys" {
				writeTestFile(t, path, "not a directory")
			} else if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(); err == nil {
				t.Fatal("non-regular key location was accepted")
			}
		})
	}
}

func TestReadSecretRefusesSymlinks(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "original")
	writeTestFile(t, target, strings.Repeat("original", 5))
	link := filepath.Join(directory, "master.key")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := readSecret(link, "JMLAB_MASTER_KEY"); err == nil {
		t.Fatal("symlink was accepted as a saved key")
	}
	if err := checkSecretDirectory(link); err == nil {
		t.Fatal("symlink was accepted as a key directory")
	}
}

func TestRandomAndPersistentKeyFailures(t *testing.T) {
	if _, err := randomSecret(bytes.NewReader(make([]byte, 31))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("failed random source must return an error: %v", err)
	}
	directory := filepath.Join(t.TempDir(), "missing")
	if _, err := persistSecret(directory, systemSecret{name: "master", filename: "master.key", value: strings.Repeat("x", 64)}); err == nil {
		t.Fatal("key write failure was ignored")
	}
}

func TestConcurrentStartsUseTheSameSavedKeys(t *testing.T) {
	directory := t.TempDir()
	const count = 6
	configs := make([]Config, count)
	errors := make([]error, count)
	var group sync.WaitGroup
	for i := range configs {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			configs[index] = Config{DataDir: directory, DatabaseURL: "sqlite://" + filepath.ToSlash(filepath.Join(directory, "jmlab.db"))}
			errors[index] = configs[index].loadSystemSecrets()
		}(i)
	}
	group.Wait()
	for i, err := range errors {
		if err != nil {
			t.Fatalf("concurrent start failed: %v", err)
		}
		if configs[i].MasterKey != configs[0].MasterKey || configs[i].SessionSecret != configs[0].SessionSecret {
			t.Fatal("concurrent starts selected different keys")
		}
	}
	files, err := os.ReadDir(filepath.Join(directory, "keys"))
	if err != nil || len(files) != 2 {
		t.Fatalf("temporary files were not cleaned: %v", err)
	}
}

func TestSQLiteDatabasePath(t *testing.T) {
	for _, test := range []struct {
		dsn      string
		path     string
		inMemory bool
		invalid  bool
	}{
		{dsn: "sqlite://relative.db?mode=rwc", path: "relative.db"},
		{dsn: "file:relative%20path.db?mode=rwc", path: "relative path.db"},
		{dsn: "sqlite://:memory:", inMemory: true},
		{dsn: "file::memory:?cache=shared", inMemory: true},
		{dsn: "file:named?mode=memory&cache=shared", inMemory: true},
		{dsn: "file:named?mode=memory&mode=ro", path: "named"},
		{dsn: "file:relative.db?mode=%xx", invalid: true},
		{dsn: "file://remote/data.db", invalid: true},
		{dsn: "file:data.db#fragment", invalid: true},
		{dsn: "file:data.db?vfs=custom", invalid: true},
		{dsn: "sqlite://absent.db?vfs=custom", invalid: true},
		{dsn: "sqlite://:memory:?vfs=custom", invalid: true},
		{dsn: "sqlite://absent.db?mode=%xx", invalid: true},
		{dsn: "sqlite://", invalid: true},
	} {
		path, inMemory, err := sqliteDatabasePath(test.dsn)
		if (err != nil) != test.invalid || path != filepath.FromSlash(test.path) || inMemory != test.inMemory {
			t.Errorf("%q: path=%q inMemory=%v err=%v", test.dsn, path, inMemory, err)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
