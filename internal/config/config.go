package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultHTTPAddr         = ":8097"
	defaultDataDir          = "/data"
	defaultPublicURL        = "http://localhost:8097"
	defaultEnvironment      = "development"
	defaultLogLevel         = "INFO"
	defaultShutdownTimeout  = 10 * time.Second
	minimumSecretByteLength = 32
)

type Config struct {
	HTTPAddr               string
	DataDir                string
	DatabaseURL            string
	MigrationsDir          string
	WebDir                 string
	ModulesDir             string
	MasterKey              string
	PublicURL              string
	SessionSecret          string
	BootstrapAdminUsername string
	BootstrapAdminPassword string
	Environment            string
	LogLevel               string
	ShutdownTimeout        time.Duration
}

func Load() (Config, error) {
	dataDir := valueOrDefault("JMLAB_DATA_DIR", defaultDataDir)
	databaseURL := strings.TrimSpace(environment("JMLAB_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = "sqlite://" + filepath.ToSlash(filepath.Join(dataDir, "jmlab.db"))
	}

	cfg := Config{
		HTTPAddr:               valueOrDefault("JMLAB_HTTP_ADDR", defaultHTTPAddr),
		DataDir:                dataDir,
		DatabaseURL:            databaseURL,
		MigrationsDir:          valueOrDefault("JMLAB_MIGRATIONS_DIR", "/app/migrations"),
		WebDir:                 valueOrDefault("JMLAB_WEB_DIR", "/app/web"),
		ModulesDir:             valueOrDefault("JMLAB_MODULES_DIR", filepath.Join(dataDir, "modules")),
		MasterKey:              environment("JMLAB_MASTER_KEY"),
		PublicURL:              valueOrDefault("JMLAB_PUBLIC_URL", defaultPublicURL),
		SessionSecret:          environment("JMLAB_SESSION_SECRET"),
		BootstrapAdminUsername: strings.TrimSpace(environment("JMLAB_BOOTSTRAP_ADMIN_USERNAME")),
		BootstrapAdminPassword: environment("JMLAB_BOOTSTRAP_ADMIN_PASSWORD"),
		Environment:            valueOrDefault("JMLAB_ENV", defaultEnvironment),
		LogLevel:               strings.ToUpper(valueOrDefault("JMLAB_LOG_LEVEL", defaultLogLevel)),
		ShutdownTimeout:        defaultShutdownTimeout,
	}
	if raw := strings.TrimSpace(environment("JMLAB_SHUTDOWN_TIMEOUT")); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("JMLAB_SHUTDOWN_TIMEOUT 必须是正的 duration: %q", raw)
		}
		cfg.ShutdownTimeout = duration
	}
	if cfg.BootstrapAdminPassword != "" && cfg.BootstrapAdminUsername == "" {
		cfg.BootstrapAdminUsername = "admin"
	}
	// Check ordinary settings before creating persistent keys. Load must finish
	// before the repository can create or migrate a database.
	if err := cfg.validateSettings(); err != nil {
		return Config{}, err
	}
	if cfg.BootstrapAdminPassword == "" {
		if isNew, err := cfg.isNewSQLiteDatabase(); err == nil && isNew {
			return Config{}, errors.New("首次启动请设置 JMLAB_BOOTSTRAP_ADMIN_PASSWORD，至少 6 位英文、数字或符号；默认管理员用户名为 admin")
		}
	}
	if err := cfg.loadSystemSecrets(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := c.validateSettings(); err != nil {
		return err
	}
	if err := validateSecret("JMLAB_MASTER_KEY", c.MasterKey); err != nil {
		return err
	}
	return validateSecret("JMLAB_SESSION_SECRET", c.SessionSecret)
}

func (c Config) validateSettings() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return errors.New("JMLAB_HTTP_ADDR 不能为空")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("JMLAB_DATA_DIR 不能为空")
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("JMLAB_DATABASE_URL 不能为空")
	}
	if (c.BootstrapAdminUsername == "") != (c.BootstrapAdminPassword == "") {
		return errors.New("初始化管理员时，请同时填写 JMLAB_BOOTSTRAP_ADMIN_USERNAME 和 JMLAB_BOOTSTRAP_ADMIN_PASSWORD（密码至少 6 位英文、数字或符号）")
	}
	if c.BootstrapAdminUsername != "" {
		if len([]byte(c.BootstrapAdminPassword)) < 6 {
			return errors.New("JMLAB_BOOTSTRAP_ADMIN_PASSWORD 必须至少包含 6 个字节")
		}
		if len(c.BootstrapAdminUsername) < 3 || len(c.BootstrapAdminUsername) > 64 {
			return errors.New("JMLAB_BOOTSTRAP_ADMIN_USERNAME 长度必须在 3 到 64 个字符之间")
		}
	}
	publicURL, err := url.Parse(c.PublicURL)
	if err != nil || publicURL.Scheme == "" || publicURL.Host == "" || (publicURL.Scheme != "http" && publicURL.Scheme != "https") {
		return fmt.Errorf("JMLAB_PUBLIC_URL 必须是 http 或 https URL: %q", c.PublicURL)
	}
	if c.ShutdownTimeout <= 0 {
		return errors.New("JMLAB_SHUTDOWN_TIMEOUT 必须为正数")
	}
	return nil
}

func validateSecret(name, value string) error {
	if len([]byte(value)) < minimumSecretByteLength {
		return fmt.Errorf("%s 必须至少包含 %d 个字节", name, minimumSecretByteLength)
	}
	return nil
}

func (c Config) EnsureDirectories() error {
	root, err := filepath.Abs(c.DataDir)
	if err != nil {
		return fmt.Errorf("解析数据目录失败: %w", err)
	}
	directories := []string{root, filepath.Join(root, "cache"), filepath.Join(root, "backup")}
	if c.ModulesDir != "" {
		directories = append(directories, c.ModulesDir)
	}
	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("创建数据目录 %q 失败: %w", dir, err)
		}
	}
	return nil
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(environment(name)); value != "" {
		return value
	}
	return fallback
}

// environment gives the current prefix priority, including an explicitly empty value.
func environment(name string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return os.Getenv(strings.Replace(name, "JMLAB_", "JAVLAB_", 1))
}
