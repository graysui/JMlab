package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
)

const (
	maxRestoreArchiveBytes      = 64 << 20
	maxRestoreFileBytes         = 64 << 20
	maxRestoreUncompressedBytes = 128 << 20
	restoreConfirmation         = "RESTORE_BACKUP"
	resetConfirmation           = "RESET_DATABASE"
)

type restoreRequest struct {
	Archive                []byte
	Mode                   string
	IncludeUserState       *bool
	IncludeEncryptedConfig bool
	Confirmation           string
}

type restoreArchive struct {
	Manifest          backupManifest
	Document          ExportDocument
	EncryptedConfig   []byte
	ArchiveSHA256     string
	DatabaseSHA256    string
	EncryptedIncluded bool
}

type restorePreview struct {
	Valid                    bool                 `json:"valid"`
	FormatVersion            string               `json:"formatVersion"`
	ArchiveFormatVersion     string               `json:"archiveFormatVersion"`
	MigrationVersion         string               `json:"migrationVersion,omitempty"`
	CreatedAt                string               `json:"createdAt"`
	ArchiveSHA256            string               `json:"archiveSha256"`
	IncludeUserState         bool                 `json:"includeUserState"`
	IncludeEncryptedConfig   bool                 `json:"includeEncryptedConfig"`
	EncryptedConfigAvailable bool                 `json:"encryptedConfigAvailable"`
	Configuration            configSummary        `json:"configuration"`
	Mode                     string               `json:"mode"`
	Files                    []backupManifestFile `json:"files"`
	Summary                  ImportSummary        `json:"summary"`
	Conflicts                []ImportConflict     `json:"conflicts"`
	Errors                   []string             `json:"errors"`
}

type resetRequest struct {
	Confirmation string `json:"confirmation"`
	BackupTaskID string `json:"backupTaskId"`
}

type resetSummary struct {
	BackupTaskID    string `json:"backupTaskId"`
	VirtualItems    int    `json:"virtualItems"`
	People          int    `json:"people"`
	Studios         int    `json:"studios"`
	Series          int    `json:"series"`
	Tags            int    `json:"tags"`
	Images          int    `json:"images"`
	Libraries       int    `json:"libraries"`
	Collections     int    `json:"collections"`
	Playlists       int    `json:"playlists"`
	PlaybackSources int    `json:"playbackSources"`
	UserItemStates  int    `json:"userItemStates"`
	RankingLocks    int    `json:"rankingLocks"`
}

func (s *Service) registerRestoreRoutes(router chi.Router) {
	adminWrite := router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"), s.auth.RequireCSRF)
	adminWrite.Post("/backup/restore/preview", s.previewRestore)
	adminWrite.Post("/backup/restore", s.restoreBackup)
	adminWrite.Post("/admin/database/reset", s.resetDatabase)
	// Keep a short alias for clients that group data-management endpoints under
	// /database while the documented compatibility path remains /admin/database.
	adminWrite.Post("/database/reset", s.resetDatabase)
}

func (s *Service) previewRestore(w http.ResponseWriter, r *http.Request) {
	request, err := decodeRestoreRequest(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_ARCHIVE", err.Error())
		return
	}
	archive, err := parseRestoreArchive(request.Archive)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_ARCHIVE", err.Error())
		return
	}
	preview, validationErr := s.buildRestorePreview(r, archive, request)
	if validationErr != nil {
		writeJSON(w, http.StatusBadRequest, preview)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Service) restoreBackup(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	request, err := decodeRestoreRequest(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_ARCHIVE", err.Error())
		return
	}
	if strings.TrimSpace(request.Confirmation) != restoreConfirmation {
		writeError(w, r, http.StatusBadRequest, "RESTORE_CONFIRMATION_REQUIRED", "恢复前必须填写确认词 RESTORE_BACKUP")
		return
	}
	archive, err := parseRestoreArchive(request.Archive)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_ARCHIVE", err.Error())
		return
	}
	preview, err := s.buildRestorePreview(r, archive, request)
	if err != nil {
		writeJSON(w, http.StatusConflict, preview)
		return
	}
	options := importOptions{Mode: preview.Mode, IncludeUserState: preview.IncludeUserState}
	var bundle *configBundle
	if request.IncludeEncryptedConfig {
		decoded, configErr := s.decodeConfigBundle(archive.EncryptedConfig)
		if configErr != nil {
			writeError(w, r, http.StatusConflict, "INVALID_BACKUP_CONFIG", configErr.Error())
			return
		}
		bundle = &decoded
	}
	summary, reload, err := s.applyRestore(r.Context(), archive.Document, options, bundle)
	if err != nil {
		writeError(w, r, http.StatusConflict, "RESTORE_ROLLED_BACK", "恢复失败，现有数据未修改")
		return
	}
	metadata, _ := json.Marshal(map[string]any{
		"archiveSha256":          archive.ArchiveSHA256,
		"databaseSha256":         archive.DatabaseSHA256,
		"mode":                   options.Mode,
		"includeUserState":       options.IncludeUserState,
		"includeEncryptedConfig": request.IncludeEncryptedConfig,
		"configuration":          preview.Configuration,
		"moduleReloadPending":    len(reload.Errors) > 0,
		"summary":                summary,
	})
	auditID := s.writeAudit(r.Context(), principal.User.ID, "restore_backup", "database", "", requestID(r), string(metadata))
	writeJSON(w, http.StatusOK, map[string]any{
		"status":                 "restored",
		"summary":                summary,
		"auditId":                auditID,
		"archiveSha256":          archive.ArchiveSHA256,
		"databaseSha256":         archive.DatabaseSHA256,
		"includeEncryptedConfig": request.IncludeEncryptedConfig,
		"configuration":          preview.Configuration,
		"moduleReload":           reload,
		"moduleReloadPending":    len(reload.Errors) > 0,
	})
}

func (s *Service) buildRestorePreview(r *http.Request, archive restoreArchive, request restoreRequest) (restorePreview, error) {
	includeUserState := archive.Manifest.IncludeUserState
	if request.IncludeUserState != nil {
		if *request.IncludeUserState && !archive.Manifest.IncludeUserState {
			return restorePreview{Valid: false, Errors: []string{"归档未包含用户状态，不能恢复用户状态"}}, errors.New("归档未包含用户状态")
		}
		includeUserState = *request.IncludeUserState
	}
	options := importOptions{Mode: request.Mode, IncludeUserState: includeUserState}
	preview, err := s.previewImport(r.Context(), archive.Document, options)
	result := restorePreview{
		Valid:                    preview.Valid,
		FormatVersion:            preview.FormatVersion,
		ArchiveFormatVersion:     archive.Manifest.FormatVersion,
		MigrationVersion:         archive.Manifest.MigrationVersion,
		CreatedAt:                archive.Manifest.CreatedAt,
		ArchiveSHA256:            archive.ArchiveSHA256,
		IncludeUserState:         includeUserState,
		IncludeEncryptedConfig:   request.IncludeEncryptedConfig,
		EncryptedConfigAvailable: archive.EncryptedIncluded,
		Mode:                     options.Mode,
		Files:                    archive.Manifest.Files,
		Summary:                  preview.Summary,
		Conflicts:                preview.Conflicts,
		Errors:                   preview.Errors,
	}
	if err != nil {
		return result, err
	}
	if request.IncludeEncryptedConfig {
		if !archive.EncryptedIncluded {
			result.Valid = false
			result.Errors = append(result.Errors, "归档未包含加密配置")
			return result, errors.New("归档未包含加密配置")
		}
		bundle, configErr := s.decodeConfigBundle(archive.EncryptedConfig)
		if configErr != nil {
			result.Valid = false
			result.Errors = append(result.Errors, configErr.Error())
			return result, configErr
		}
		result.Configuration = bundle.summary()
	}
	return result, nil
}

func decodeRestoreRequest(r *http.Request) (restoreRequest, error) {
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	request := restoreRequest{Mode: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))}
	switch contentType {
	case "multipart/form-data":
		if err := r.ParseMultipartForm(maxRestoreArchiveBytes + 1<<20); err != nil {
			return request, errors.New("恢复请求无法读取")
		}
		file, _, err := r.FormFile("archive")
		if err != nil {
			file, _, err = r.FormFile("file")
		}
		if err != nil {
			return request, errors.New("恢复请求必须包含 archive 文件")
		}
		defer file.Close()
		request.Archive, err = readLimited(file, maxRestoreArchiveBytes)
		if err != nil {
			return request, err
		}
		if value := strings.TrimSpace(r.FormValue("mode")); value != "" {
			request.Mode = strings.ToLower(value)
		}
		request.Confirmation = strings.TrimSpace(r.FormValue("confirmation"))
		request.IncludeEncryptedConfig = parseBool(r.FormValue("includeEncryptedConfig"))
		if value := strings.TrimSpace(r.FormValue("includeUserState")); value != "" {
			parsed := parseBool(value)
			request.IncludeUserState = &parsed
		}
	case "application/gzip", "application/x-gzip", "application/octet-stream":
		var err error
		request.Archive, err = readLimited(r.Body, maxRestoreArchiveBytes)
		if err != nil {
			return request, err
		}
		request.Confirmation = strings.TrimSpace(r.URL.Query().Get("confirmation"))
		request.IncludeEncryptedConfig = parseBool(r.URL.Query().Get("includeEncryptedConfig"))
	default:
		body, err := readLimited(r.Body, maxRestoreArchiveBytes*2)
		if err != nil {
			return request, err
		}
		var envelope struct {
			ArchiveBase64          string `json:"archiveBase64"`
			Archive                string `json:"archive"`
			Mode                   string `json:"mode"`
			IncludeUserState       *bool  `json:"includeUserState"`
			IncludeEncryptedConfig bool   `json:"includeEncryptedConfig"`
			Confirmation           string `json:"confirmation"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return request, errors.New("恢复请求必须是 multipart 文件或 JSON base64")
		}
		encoded := strings.TrimSpace(envelope.ArchiveBase64)
		if encoded == "" {
			encoded = strings.TrimSpace(envelope.Archive)
		}
		if encoded == "" {
			return request, errors.New("恢复请求缺少 archiveBase64")
		}
		request.Archive, err = decodeBase64Archive(encoded)
		if err != nil {
			return request, errors.New("archiveBase64 不是有效的备份归档")
		}
		if envelope.Mode != "" {
			request.Mode = strings.ToLower(strings.TrimSpace(envelope.Mode))
		}
		request.IncludeUserState = envelope.IncludeUserState
		request.IncludeEncryptedConfig = envelope.IncludeEncryptedConfig
		request.Confirmation = strings.TrimSpace(envelope.Confirmation)
	}
	if request.Mode == "" {
		request.Mode = "merge"
	}
	if len(request.Archive) == 0 {
		return request, errors.New("备份归档不能为空")
	}
	return request, nil
}

func readLimited(reader io.Reader, maximum int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, errors.New("读取备份归档失败")
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("备份归档不能超过 %d MB", maximum/(1<<20))
	}
	return data, nil
}

func decodeBase64Archive(encoded string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || int64(len(data)) > maxRestoreArchiveBytes {
		return nil, errors.New("归档大小或编码无效")
	}
	return data, nil
}

func parseRestoreArchive(data []byte) (restoreArchive, error) {
	result := restoreArchive{}
	if len(data) == 0 || int64(len(data)) > maxRestoreArchiveBytes {
		return result, fmt.Errorf("备份归档不能超过 %d MB", maxRestoreArchiveBytes/(1<<20))
	}
	archiveHash := sha256.Sum256(data)
	result.ArchiveSHA256 = hex.EncodeToString(archiveHash[:])
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return result, errors.New("备份归档不是有效的 gzip 文件")
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(io.LimitReader(gzipReader, maxRestoreUncompressedBytes+1))
	files := map[string][]byte{}
	var total int64
	for {
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return result, errors.New("备份归档目录损坏")
		}
		name := header.Name
		cleanName := path.Clean(name)
		if name == "" || cleanName != name || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || cleanName == "." || strings.HasPrefix(cleanName, "../") {
			return result, errors.New("备份归档包含不安全路径")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return result, errors.New("备份归档只允许普通文件")
		}
		if _, exists := files[name]; exists {
			return result, fmt.Errorf("备份归档包含重复文件: %s", name)
		}
		if header.Size < 0 || header.Size > maxRestoreFileBytes {
			return result, fmt.Errorf("备份文件过大: %s", name)
		}
		contents, readErr := io.ReadAll(io.LimitReader(tarReader, maxRestoreFileBytes+1))
		if readErr != nil || int64(len(contents)) != header.Size {
			return result, fmt.Errorf("备份文件读取失败: %s", name)
		}
		total += int64(len(contents))
		if total > maxRestoreUncompressedBytes {
			return result, errors.New("备份归档解压后超过大小限制")
		}
		files[name] = contents
	}
	manifestData, ok := files["manifest.json"]
	if !ok {
		return result, errors.New("备份归档缺少 manifest.json")
	}
	if err := json.Unmarshal(manifestData, &result.Manifest); err != nil || strings.Replace(result.Manifest.FormatVersion, "javlab.", "jmlab.", 1) != backupArchiveFormatVersion {
		return result, errors.New("备份 manifest 版本不受支持")
	}
	if len(result.Manifest.Files) == 0 {
		return result, errors.New("备份 manifest 缺少文件摘要")
	}
	listed := map[string]bool{}
	for _, entry := range result.Manifest.Files {
		if entry.Name != "database.json" && entry.Name != "encrypted-config.json" {
			return result, errors.New("备份 manifest 包含不受支持的文件")
		}
		if listed[entry.Name] {
			return result, errors.New("备份 manifest 包含重复文件")
		}
		listed[entry.Name] = true
		contents, exists := files[entry.Name]
		if !exists || int64(len(contents)) != int64(entry.Size) {
			return result, fmt.Errorf("备份文件大小校验失败: %s", entry.Name)
		}
		hash := sha256.Sum256(contents)
		if !strings.EqualFold(hex.EncodeToString(hash[:]), strings.TrimSpace(entry.SHA256)) {
			return result, fmt.Errorf("备份文件 SHA256 校验失败: %s", entry.Name)
		}
	}
	databaseData, ok := files["database.json"]
	if !ok || !listed["database.json"] {
		return result, errors.New("备份归档缺少 database.json")
	}
	if err := json.Unmarshal(databaseData, &result.Document); err != nil || !supportedExportVersion(result.Document.FormatVersion) {
		return result, errors.New("备份 database.json 格式不受支持")
	}
	if configData, exists := files["encrypted-config.json"]; exists {
		if !listed["encrypted-config.json"] || !result.Manifest.IncludeEncryptedConfig {
			return result, errors.New("备份加密配置摘要不一致")
		}
		result.EncryptedConfig = append([]byte(nil), configData...)
		result.EncryptedIncluded = true
	}
	for name := range files {
		if name != "manifest.json" && !listed[name] {
			return result, fmt.Errorf("备份归档包含未列入 manifest 的文件: %s", name)
		}
	}
	if result.Manifest.IncludeEncryptedConfig != result.EncryptedIncluded {
		return result, errors.New("备份 manifest 的加密配置标记不一致")
	}
	if result.Manifest.IncludeUserState != result.Document.IncludeUserState {
		return result, errors.New("备份 manifest 的用户状态标记不一致")
	}
	databaseHash := sha256.Sum256(databaseData)
	result.DatabaseSHA256 = hex.EncodeToString(databaseHash[:])
	return result, nil
}

func requestID(r *http.Request) string {
	return middleware.GetReqID(r.Context())
}

func (s *Service) resetDatabase(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	var input resetRequest
	if err := decodeBackupJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_DATABASE_RESET", "重置请求格式不正确")
		return
	}
	if strings.TrimSpace(input.Confirmation) != resetConfirmation {
		writeError(w, r, http.StatusBadRequest, "RESET_CONFIRMATION_REQUIRED", "重置前必须填写确认词 RESET_DATABASE")
		return
	}
	if strings.TrimSpace(input.BackupTaskID) == "" {
		writeError(w, r, http.StatusBadRequest, "BACKUP_REQUIRED", "重置前必须提供已成功完成的备份任务 ID")
		return
	}
	var status, artifact string
	err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT t.status, COALESCE(br.artifact_sha256, '') FROM tasks t JOIN backup_runs br ON br.task_id = t.id WHERE t.id = ? AND t.type = 'backup'"), input.BackupTaskID).Scan(&status, &artifact)
	if errors.Is(err, sql.ErrNoRows) || status != "succeeded" || strings.TrimSpace(artifact) == "" {
		writeError(w, r, http.StatusConflict, "BACKUP_REQUIRED", "只能使用已成功完成且包含校验值的备份任务进行重置保护")
		return
	}
	summary, err := s.clearCatalog(r.Context(), input.BackupTaskID)
	if err != nil {
		writeError(w, r, http.StatusConflict, "RESET_ROLLED_BACK", "数据库重置失败，现有数据未修改")
		return
	}
	metadata, _ := json.Marshal(summary)
	auditID := s.writeAudit(r.Context(), principal.User.ID, "reset_database", "database", "", requestID(r), string(metadata))
	writeJSON(w, http.StatusOK, map[string]any{"status": "reset", "summary": summary, "auditId": auditID})
}

func (s *Service) clearCatalog(ctx context.Context, backupTaskID string) (resetSummary, error) {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return resetSummary{}, err
	}
	defer tx.Rollback()
	summary := resetSummary{BackupTaskID: backupTaskID}
	counts := []struct {
		table string
		dest  *int
	}{
		{"media_items", &summary.VirtualItems}, {"people", &summary.People}, {"studios", &summary.Studios}, {"series", &summary.Series}, {"tags", &summary.Tags}, {"item_images", &summary.Images}, {"virtual_libraries", &summary.Libraries}, {"collections", &summary.Collections}, {"playlists", &summary.Playlists}, {"stream_sources", &summary.PlaybackSources}, {"user_item_states", &summary.UserItemStates}, {"ranking_generation_locks", &summary.RankingLocks},
	}
	for _, item := range counts {
		if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM "+item.table)).Scan(item.dest); err != nil {
			return resetSummary{}, err
		}
	}
	for _, statement := range []string{
		"DELETE FROM ranking_generation_locks",
		"DELETE FROM user_library_permissions",
		"DELETE FROM role_library_permissions",
		"DELETE FROM library_items",
		"DELETE FROM collection_items",
		"DELETE FROM playlist_items",
		"DELETE FROM user_item_state_events",
		"DELETE FROM image_source_attempts",
		"DELETE FROM user_image_states",
		"DELETE FROM user_item_states",
		"DELETE FROM user_person_states",
		"DELETE FROM user_entity_notes",
		"DELETE FROM metadata_field_states",
		"DELETE FROM item_entities",
		"DELETE FROM item_external_links",
		"DELETE FROM item_rankings",
		"DELETE FROM stream_sources",
		"DELETE FROM item_images",
		"DELETE FROM playlists",
		"DELETE FROM collections",
		"DELETE FROM virtual_libraries",
		"DELETE FROM media_items",
		"UPDATE entities SET parent_id = NULL",
		"DELETE FROM entities",
	} {
		if _, err := tx.ExecContext(ctx, s.db.Rebind(statement)); err != nil {
			return resetSummary{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return resetSummary{}, err
	}
	return summary, nil
}
