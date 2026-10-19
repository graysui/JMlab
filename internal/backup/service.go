package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

const (
	defaultRetentionCount = 7
	maxRetentionCount     = 3650
	backupRoot            = "/backup"
)

// Service owns backup target metadata and the Admin-only data-management
// boundary. Target credentials are encrypted at rest and are never included
// in a response. Backup execution is handled by the dedicated worker.
type Service struct {
	db           *repository.Database
	auth         *auth.Service
	masterKey    string
	databaseURL  string
	dataDir      string
	workerCancel context.CancelFunc
	workerDone   chan struct{}
	modules      *module.Manager
}

type targetConfig struct {
	Path          string `json:"path,omitempty"`
	URL           string `json:"url,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	Token         string `json:"token,omitempty"`
	Remote        string `json:"remote,omitempty"`
	Branch        string `json:"branch,omitempty"`
	AuthType      string `json:"authType,omitempty"`
	SSHPrivateKey string `json:"sshPrivateKey,omitempty"`
	KnownHosts    string `json:"knownHosts,omitempty"`
}

type targetMutation struct {
	TargetType     *string         `json:"targetType"`
	DisplayName    *string         `json:"displayName"`
	Enabled        *bool           `json:"enabled"`
	RetentionCount *int            `json:"retentionCount"`
	Config         json.RawMessage `json:"config"`
}

type TargetSummary struct {
	ID                   string         `json:"id"`
	TargetType           string         `json:"targetType"`
	DisplayName          string         `json:"displayName"`
	Enabled              bool           `json:"enabled"`
	RetentionCount       int            `json:"retentionCount"`
	Config               map[string]any `json:"config"`
	CredentialConfigured bool           `json:"credentialConfigured"`
	LastRunAt            string         `json:"lastRunAt,omitempty"`
	LastStatus           string         `json:"lastStatus,omitempty"`
	LastError            string         `json:"lastError,omitempty"`
	CreatedAt            string         `json:"createdAt"`
	UpdatedAt            string         `json:"updatedAt"`
}

type targetRecord struct {
	TargetSummary
	EncryptedConfig string
}

type targetTestResult struct {
	Healthy    bool   `json:"healthy"`
	StatusCode int    `json:"statusCode,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

func NewService(db *repository.Database, authService *auth.Service, masterKey, databaseURL, dataDir string) *Service {
	return &Service{db: db, auth: authService, masterKey: masterKey, databaseURL: databaseURL, dataDir: dataDir}
}

func (s *Service) SetModuleManager(manager *module.Manager) { s.modules = manager }

func (s *Service) RegisterRoutes(router chi.Router) {
	admin := router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"))
	adminWrite := router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"), s.auth.RequireCSRF)
	admin.Get("/backup/targets", s.listTargets)
	admin.Get("/backup/targets/{targetID}", s.getTarget)
	admin.Get("/backup/runs/{taskID}", s.getBackupRun)
	adminWrite.Post("/backup/targets", s.createTarget)
	adminWrite.Patch("/backup/targets/{targetID}", s.updateTarget)
	adminWrite.Delete("/backup/targets/{targetID}", s.deleteTarget)
	adminWrite.Post("/backup/targets/{targetID}/test", s.testTarget)
	adminWrite.Post("/backup/run", s.enqueueBackup)
	s.registerExportRoutes(router)
	s.registerRestoreRoutes(router)
}

func (s *Service) listTargets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(`SELECT id, target_type, display_name, enabled, retention_count, encrypted_config, COALESCE(last_run_at, ''), COALESCE(last_status, ''), COALESCE(last_error, ''), created_at, updated_at FROM backup_targets ORDER BY display_name, id`))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGETS_READ_FAILED", "读取备份目标失败")
		return
	}
	defer rows.Close()
	items := make([]TargetSummary, 0)
	for rows.Next() {
		record, err := s.scanTarget(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGETS_READ_FAILED", "读取备份目标失败")
			return
		}
		items = append(items, record.TargetSummary)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGETS_READ_FAILED", "读取备份目标失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getTarget(w http.ResponseWriter, r *http.Request) {
	record, err := s.loadTarget(r.Context(), chi.URLParam(r, "targetID"))
	if errors.Is(err, errTargetNotFound) {
		writeError(w, r, http.StatusNotFound, "BACKUP_TARGET_NOT_FOUND", "备份目标不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_READ_FAILED", "读取备份目标失败")
		return
	}
	writeJSON(w, http.StatusOK, record.TargetSummary)
}

func (s *Service) createTarget(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input targetMutation
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_TARGET", "备份目标格式不正确")
		return
	}
	if input.TargetType == nil || input.DisplayName == nil || input.Config == nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_TARGET", "targetType、displayName 和 config 都是必填项")
		return
	}
	targetType := strings.ToLower(strings.TrimSpace(*input.TargetType))
	displayName := strings.TrimSpace(*input.DisplayName)
	retention := defaultRetentionCount
	if input.RetentionCount != nil {
		retention = *input.RetentionCount
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	cfg, err := decodeTargetConfig(input.Config)
	if err == nil {
		cfg, err = validateTargetConfig(targetType, cfg)
	}
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_TARGET", err.Error())
		return
	}
	if err := validateTargetFields(targetType, displayName, retention); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_TARGET", err.Error())
		return
	}
	encrypted, err := s.encryptConfig(cfg)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_ENCRYPT_FAILED", "备份目标配置加密失败")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := newTargetID()
	_, err = s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO backup_targets (id, target_type, display_name, encrypted_config, enabled, retention_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"), id, targetType, displayName, encrypted, boolInt(enabled), retention, now, now)
	if err != nil {
		writeError(w, r, http.StatusConflict, "BACKUP_TARGET_CREATE_FAILED", "创建备份目标失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "create_backup_target", "backup_target", id, "{}")
	record, err := s.loadTarget(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_READ_FAILED", "读取备份目标失败")
		return
	}
	writeJSON(w, http.StatusCreated, record.TargetSummary)
}

func (s *Service) updateTarget(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id := chi.URLParam(r, "targetID")
	record, err := s.loadTarget(r.Context(), id)
	if errors.Is(err, errTargetNotFound) {
		writeError(w, r, http.StatusNotFound, "BACKUP_TARGET_NOT_FOUND", "备份目标不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_READ_FAILED", "读取备份目标失败")
		return
	}
	var input targetMutation
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_TARGET", "备份目标格式不正确")
		return
	}
	targetType := record.TargetType
	if input.TargetType != nil && strings.TrimSpace(*input.TargetType) != "" && !strings.EqualFold(strings.TrimSpace(*input.TargetType), targetType) {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_TARGET", "不能修改备份目标类型")
		return
	}
	displayName := record.DisplayName
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
	}
	retention := record.RetentionCount
	if input.RetentionCount != nil {
		retention = *input.RetentionCount
	}
	enabled := record.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	cfg, err := s.decryptConfig(record.EncryptedConfig)
	if err == nil && len(input.Config) > 0 && string(input.Config) != "null" {
		cfg, err = mergeTargetConfig(cfg, input.Config)
	}
	if err == nil {
		cfg, err = validateTargetConfig(targetType, cfg)
	}
	if err == nil {
		err = validateTargetFields(targetType, displayName, retention)
	}
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_TARGET", err.Error())
		return
	}
	encrypted, err := s.encryptConfig(cfg)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_ENCRYPT_FAILED", "备份目标配置加密失败")
		return
	}
	_, err = s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE backup_targets SET display_name = ?, encrypted_config = ?, enabled = ?, retention_count = ?, updated_at = ? WHERE id = ?"), displayName, encrypted, boolInt(enabled), retention, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		writeError(w, r, http.StatusConflict, "BACKUP_TARGET_UPDATE_FAILED", "更新备份目标失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "update_backup_target", "backup_target", id, "{}")
	record, err = s.loadTarget(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_READ_FAILED", "读取备份目标失败")
		return
	}
	writeJSON(w, http.StatusOK, record.TargetSummary)
}

func (s *Service) deleteTarget(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id := chi.URLParam(r, "targetID")
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM backup_targets WHERE id = ?"), id)
	if err != nil {
		writeError(w, r, http.StatusConflict, "BACKUP_TARGET_DELETE_FAILED", "删除备份目标失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "BACKUP_TARGET_NOT_FOUND", "备份目标不存在")
		return
	}
	s.audit(r.Context(), principal.User.ID, "delete_backup_target", "backup_target", id, "{}")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) testTarget(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id := chi.URLParam(r, "targetID")
	record, err := s.loadTarget(r.Context(), id)
	if errors.Is(err, errTargetNotFound) {
		writeError(w, r, http.StatusNotFound, "BACKUP_TARGET_NOT_FOUND", "备份目标不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_READ_FAILED", "读取备份目标失败")
		return
	}
	cfg, err := s.decryptConfig(record.EncryptedConfig)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TARGET_CONFIG_FAILED", "备份目标配置不可用")
		return
	}
	started := time.Now()
	result := s.testConnection(r.Context(), record.TargetType, cfg)
	result.DurationMS = time.Since(started).Milliseconds()
	status := "failed"
	lastError := result.Error
	if result.Healthy {
		status = "succeeded"
		lastError = ""
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE backup_targets SET last_run_at = ?, last_status = ?, last_error = ?, updated_at = ? WHERE id = ?"), now, status, nullable(lastError), now, id)
	s.audit(r.Context(), principal.User.ID, "test_backup_target", "backup_target", id, fmt.Sprintf(`{"status":%q,"healthy":%t}`, status, result.Healthy))
	writeJSON(w, http.StatusOK, map[string]any{"targetId": id, "targetType": record.TargetType, "result": result})
}

func (s *Service) scanTarget(rows interface{ Scan(...any) error }) (targetRecord, error) {
	var record targetRecord
	var enabled int
	if err := rows.Scan(&record.ID, &record.TargetType, &record.DisplayName, &enabled, &record.RetentionCount, &record.EncryptedConfig, &record.LastRunAt, &record.LastStatus, &record.LastError, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return targetRecord{}, err
	}
	record.Enabled = enabled == 1
	cfg, err := s.decryptConfig(record.EncryptedConfig)
	if err != nil {
		return targetRecord{}, err
	}
	record.Config, record.CredentialConfigured = summarizeTargetConfig(record.TargetType, cfg)
	record.LastError = redactSummary(record.LastError)
	return record, nil
}

var errTargetNotFound = errors.New("backup target not found")

func (s *Service) loadTarget(ctx context.Context, id string) (targetRecord, error) {
	row := s.db.DB().QueryRowContext(ctx, s.db.Rebind(`SELECT id, target_type, display_name, enabled, retention_count, encrypted_config, COALESCE(last_run_at, ''), COALESCE(last_status, ''), COALESCE(last_error, ''), created_at, updated_at FROM backup_targets WHERE id = ?`), id)
	record, err := s.scanTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return targetRecord{}, errTargetNotFound
	}
	if err != nil {
		return targetRecord{}, err
	}
	return record, nil
}

func (s *Service) encryptConfig(cfg targetConfig) (string, error) {
	plain, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(s.masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	encoded := gcm.Seal(nonce, nonce, plain, nil)
	return "v1:" + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (s *Service) decryptConfig(value string) (targetConfig, error) {
	version, encoded, ok := strings.Cut(value, ":")
	if !ok || version != "v1" {
		return targetConfig{}, errors.New("密文版本无效")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return targetConfig{}, errors.New("密文无效")
	}
	key := sha256.Sum256([]byte(s.masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return targetConfig{}, errors.New("密文无效")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(data) < gcm.NonceSize() {
		return targetConfig{}, errors.New("密文无效")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return targetConfig{}, errors.New("密文无效")
	}
	return decodeTargetConfig(plain)
}

func decodeTargetConfig(raw []byte) (targetConfig, error) {
	var cfg targetConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return targetConfig{}, errors.New("config 必须是受支持的对象")
	}
	return cfg, nil
}

func mergeTargetConfig(existing targetConfig, raw []byte) (targetConfig, error) {
	var base map[string]json.RawMessage
	encoded, _ := json.Marshal(existing)
	if err := json.Unmarshal(encoded, &base); err != nil {
		return targetConfig{}, errors.New("config 无法读取")
	}
	var incoming map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&incoming); err != nil || incoming == nil {
		return targetConfig{}, errors.New("config 必须是对象")
	}
	for key, value := range incoming {
		base[key] = value
	}
	merged, err := json.Marshal(base)
	if err != nil {
		return targetConfig{}, errors.New("config 无法保存")
	}
	return decodeTargetConfig(merged)
}

func validateTargetFields(targetType, displayName string, retention int) error {
	if targetType != "local" && targetType != "webdav" && targetType != "git" {
		return errors.New("targetType 只能是 local、webdav 或 git")
	}
	if displayName == "" || len([]rune(displayName)) > 200 {
		return errors.New("displayName 必须为 1 到 200 个字符")
	}
	if retention < 1 || retention > maxRetentionCount {
		return fmt.Errorf("retentionCount 必须在 1 到 %d 之间", maxRetentionCount)
	}
	return nil
}

func validateTargetConfig(targetType string, cfg targetConfig) (targetConfig, error) {
	cfg.AuthType = strings.ToLower(strings.TrimSpace(cfg.AuthType))
	switch targetType {
	case "local":
		path, err := normalizeBackupPath(cfg.Path)
		if err != nil {
			return targetConfig{}, err
		}
		cfg.Path = path
		return cfg, nil
	case "webdav":
		parsed, err := url.Parse(strings.TrimSpace(cfg.URL))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return targetConfig{}, errors.New("WebDAV URL 必须是无用户信息的 HTTPS URL")
		}
		cfg.URL = parsed.String()
		if cfg.AuthType == "" {
			switch {
			case cfg.Token != "":
				cfg.AuthType = "bearer"
			case cfg.Password != "":
				cfg.AuthType = "basic"
			default:
				cfg.AuthType = "none"
			}
		}
		if cfg.AuthType != "none" && cfg.AuthType != "basic" && cfg.AuthType != "bearer" {
			return targetConfig{}, errors.New("WebDAV authType 只能是 none、basic 或 bearer")
		}
		if cfg.AuthType == "basic" && (strings.TrimSpace(cfg.Username) == "" || cfg.Password == "") {
			return targetConfig{}, errors.New("WebDAV basic 认证需要 username 和 password")
		}
		if cfg.AuthType == "bearer" && cfg.Token == "" {
			return targetConfig{}, errors.New("WebDAV bearer 认证需要 token")
		}
		return cfg, nil
	case "git":
		remote := strings.TrimSpace(cfg.Remote)
		if err := validateGitRemote(remote); err != nil {
			return targetConfig{}, err
		}
		cfg.Remote = remote
		cfg.Branch = strings.TrimSpace(cfg.Branch)
		if cfg.Branch == "" {
			cfg.Branch = "main"
		}
		if len(cfg.Branch) > 200 || strings.ContainsAny(cfg.Branch, "\r\n") {
			return targetConfig{}, errors.New("Git branch 无效")
		}
		if cfg.AuthType == "" {
			if cfg.SSHPrivateKey != "" {
				cfg.AuthType = "ssh"
			} else if cfg.Token != "" {
				cfg.AuthType = "token"
			} else {
				cfg.AuthType = "none"
			}
		}
		if cfg.AuthType != "none" && cfg.AuthType != "token" && cfg.AuthType != "ssh" {
			return targetConfig{}, errors.New("Git authType 只能是 none、token 或 ssh")
		}
		if cfg.AuthType == "token" && cfg.Token == "" {
			return targetConfig{}, errors.New("Git token 认证需要 token")
		}
		if cfg.AuthType == "ssh" && (cfg.SSHPrivateKey == "" || cfg.KnownHosts == "") {
			return targetConfig{}, errors.New("Git SSH 认证需要 sshPrivateKey 和 knownHosts")
		}
		return cfg, nil
	default:
		return targetConfig{}, errors.New("targetType 不受支持")
	}
}

func validateGitRemote(remote string) error {
	if remote == "" || strings.ContainsAny(remote, "\r\n") {
		return errors.New("Git remote 不能为空")
	}
	if strings.Contains(remote, "@") && strings.HasPrefix(remote, "http") {
		return errors.New("Git HTTPS remote 不得包含用户信息或令牌")
	}
	if filepath.IsAbs(remote) || strings.HasPrefix(remote, "file://") || strings.HasPrefix(remote, "https://") || strings.HasPrefix(remote, "ssh://") || (strings.Contains(remote, ":") && !strings.Contains(remote, "://")) {
		return nil
	}
	return errors.New("Git remote 只允许 HTTPS 或 SSH 地址")
}

func normalizeBackupPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("本地备份路径不能为空或包含换行")
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", errors.New("本地备份路径不允许包含 ..")
		}
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(backupRoot, raw)
	}
	abs, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", errors.New("本地备份路径无效")
	}
	root, _ := filepath.Abs(backupRoot)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("本地备份路径必须位于 /backup 内")
	}
	probe := abs
	for {
		info, statErr := os.Lstat(probe)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, resolveErr := filepath.EvalSymlinks(probe)
				if resolveErr != nil {
					return "", errors.New("本地备份路径符号链接无法解析")
				}
				resolvedRel, _ := filepath.Rel(root, resolved)
				if resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(filepath.Separator)) {
					return "", errors.New("本地备份路径不允许逃逸 /backup")
				}
			}
			break
		}
		next := filepath.Dir(probe)
		if next == probe {
			break
		}
		probe = next
	}
	return abs, nil
}

func summarizeTargetConfig(targetType string, cfg targetConfig) (map[string]any, bool) {
	result := map[string]any{}
	credential := false
	switch targetType {
	case "local":
		result["path"] = cfg.Path
	case "webdav":
		result["url"] = cfg.URL
		result["username"] = cfg.Username
		result["authType"] = cfg.AuthType
		credential = cfg.Password != "" || cfg.Token != ""
	case "git":
		result["remote"] = cfg.Remote
		result["branch"] = cfg.Branch
		result["authType"] = cfg.AuthType
		result["username"] = cfg.Username
		result["knownHostsConfigured"] = cfg.KnownHosts != ""
		credential = cfg.Token != "" || cfg.SSHPrivateKey != ""
	}
	return result, credential
}

func (s *Service) testConnection(ctx context.Context, targetType string, cfg targetConfig) targetTestResult {
	switch targetType {
	case "local":
		return testLocalTarget(cfg.Path)
	case "webdav":
		return testWebDAVTarget(ctx, cfg)
	case "git":
		return testGitTarget(ctx, cfg)
	default:
		return targetTestResult{Error: "目标类型不受支持"}
	}
}

func testLocalTarget(path string) targetTestResult {
	if err := os.MkdirAll(path, 0o750); err != nil {
		return targetTestResult{Error: "本地目录不可写"}
	}
	temporary, err := os.CreateTemp(path, ".jmlab-write-test-*")
	if err != nil {
		return targetTestResult{Error: "本地目录不可写"}
	}
	name := temporary.Name()
	_ = temporary.Close()
	_ = os.Remove(name)
	return targetTestResult{Healthy: true}
}

func testWebDAVTarget(ctx context.Context, cfg targetConfig) targetTestResult {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, cfg.URL, nil)
	if err != nil {
		return targetTestResult{Error: "WebDAV URL 无效"}
	}
	applyWebDAVAuth(request, cfg)
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return targetTestResult{Error: "WebDAV 连接失败"}
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return targetTestResult{StatusCode: response.StatusCode, Error: "WebDAV 禁止重定向"}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return targetTestResult{Healthy: true, StatusCode: response.StatusCode}
	}
	return targetTestResult{StatusCode: response.StatusCode, Error: fmt.Sprintf("WebDAV 返回 HTTP %d", response.StatusCode)}
}

func applyWebDAVAuth(request *http.Request, cfg targetConfig) {
	switch cfg.AuthType {
	case "basic":
		request.SetBasicAuth(cfg.Username, cfg.Password)
	case "bearer":
		request.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
}

func testGitTarget(ctx context.Context, cfg targetConfig) targetTestResult {
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(testCtx, "git", "ls-remote", "--heads", cfg.Remote)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cleanup := make([]string, 0, 3)
	defer func() {
		for _, path := range cleanup {
			_ = os.Remove(path)
		}
	}()
	if cfg.AuthType == "token" {
		askpass, err := os.CreateTemp("", "jmlab-git-askpass-*")
		if err != nil {
			return targetTestResult{Error: "Git 凭证临时文件创建失败"}
		}
		path := askpass.Name()
		cleanup = append(cleanup, path)
		_ = askpass.Chmod(0o700)
		_, _ = askpass.WriteString("#!/bin/sh\ncase \"$1\" in *Username*) printf '%s\\n' \"$JMLAB_BACKUP_GIT_USERNAME\" ;; *) printf '%s\\n' \"$JMLAB_BACKUP_GIT_TOKEN\" ;; esac\n")
		_ = askpass.Close()
		command.Env = append(command.Env, "GIT_ASKPASS="+path, "JMLAB_BACKUP_GIT_USERNAME="+cfg.Username, "JMLAB_BACKUP_GIT_TOKEN="+cfg.Token)
	}
	if cfg.AuthType == "ssh" {
		keyFile, err := os.CreateTemp("", "jmlab-git-key-*")
		if err != nil {
			return targetTestResult{Error: "Git SSH 私钥临时文件创建失败"}
		}
		keyPath := keyFile.Name()
		cleanup = append(cleanup, keyPath)
		_ = keyFile.Chmod(0o600)
		_, _ = keyFile.WriteString(cfg.SSHPrivateKey)
		_ = keyFile.Close()
		hostsFile, err := os.CreateTemp("", "jmlab-git-known-hosts-*")
		if err != nil {
			return targetTestResult{Error: "Git known_hosts 临时文件创建失败"}
		}
		hostsPath := hostsFile.Name()
		cleanup = append(cleanup, hostsPath)
		_ = hostsFile.Chmod(0o600)
		_, _ = hostsFile.WriteString(cfg.KnownHosts)
		_ = hostsFile.Close()
		command.Env = append(command.Env, "GIT_SSH_COMMAND=ssh -i "+keyPath+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="+hostsPath)
	}
	if err := command.Run(); err != nil {
		if errors.Is(testCtx.Err(), context.DeadlineExceeded) {
			return targetTestResult{Error: "Git 连接超时"}
		}
		return targetTestResult{Error: "Git 连接失败"}
	}
	return targetTestResult{Healthy: true}
}

func (s *Service) audit(ctx context.Context, actor, action, resource, resourceID, metadata string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, 'success', ?, ?)"), newTargetID(), actor, action, resource, resourceID, middleware.GetReqID(ctx), metadata, time.Now().UTC().Format(time.RFC3339Nano))
}

func newTargetID() string { return fmt.Sprintf("backup_%x", time.Now().UTC().UnixNano()) }

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func redactSummary(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, marker := range []string{"Authorization", "Bearer", "Cookie", "password", "token", "secret", "api_key"} {
		if strings.Contains(strings.ToLower(value), strings.ToLower(marker)) {
			return "[敏感错误已隐藏]"
		}
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": middleware.GetReqID(r.Context())}})
}
