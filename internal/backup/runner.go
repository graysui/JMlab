package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/graysui/JMlab/internal/auth"
)

const backupArchiveFormatVersion = "jmlab.backup.v1"

type backupRunRequest struct {
	TargetIDs              []string `json:"targetIds"`
	IncludeUserState       bool     `json:"includeUserState"`
	IncludeEncryptedConfig bool     `json:"includeEncryptedConfig"`
}

type backupTaskPayload struct {
	RunID                  string   `json:"runId"`
	TargetIDs              []string `json:"targetIds"`
	IncludeUserState       bool     `json:"includeUserState"`
	IncludeEncryptedConfig bool     `json:"includeEncryptedConfig"`
	RequestedBy            string   `json:"requestedBy"`
}

type backupManifest struct {
	FormatVersion          string               `json:"formatVersion"`
	CreatedAt              string               `json:"createdAt"`
	ApplicationVersion     string               `json:"applicationVersion"`
	MigrationVersion       string               `json:"migrationVersion,omitempty"`
	IncludeUserState       bool                 `json:"includeUserState"`
	IncludeEncryptedConfig bool                 `json:"includeEncryptedConfig"`
	Files                  []backupManifestFile `json:"files"`
}

type backupManifestFile struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

type backupRunTargetView struct {
	TargetID     string  `json:"targetId"`
	TargetType   string  `json:"targetType"`
	DisplayName  string  `json:"displayName"`
	Status       string  `json:"status"`
	ArtifactPath string  `json:"artifactPath,omitempty"`
	BytesWritten int64   `json:"bytesWritten"`
	Checksum     string  `json:"checksum,omitempty"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
	StartedAt    *string `json:"startedAt,omitempty"`
	CompletedAt  *string `json:"completedAt,omitempty"`
}

type backupRunView struct {
	ID                     string                `json:"id"`
	TaskID                 string                `json:"taskId"`
	TaskStatus             string                `json:"taskStatus"`
	IncludeUserState       bool                  `json:"includeUserState"`
	IncludeEncryptedConfig bool                  `json:"includeEncryptedConfig"`
	ArtifactName           string                `json:"artifactName,omitempty"`
	ArtifactSHA256         string                `json:"artifactSha256,omitempty"`
	ArtifactSize           int64                 `json:"artifactSize,omitempty"`
	CreatedAt              string                `json:"createdAt"`
	CompletedAt            *string               `json:"completedAt,omitempty"`
	Targets                []backupRunTargetView `json:"targets"`
}

type backupTargetJob struct {
	ID             string
	TargetType     string
	DisplayName    string
	RetentionCount int
	Config         targetConfig
	Missing        bool
}

type backupTaskRecord struct {
	ID            string
	AttemptNumber int
	Type          string
	Status        string
	RetryCount    int
	MaxRetries    int
}

type backupArtifact struct {
	Name   string
	Data   []byte
	SHA256 string
}

type backupTargetResult struct {
	Status       string
	ArtifactPath string
	BytesWritten int64
	Checksum     string
	Error        string
}

func (s *Service) enqueueBackup(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	input := backupRunRequest{}
	if r.Body != nil {
		if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_RUN", "备份请求格式不正确")
			return
		}
	}
	ids, err := s.resolveBackupTargetIDs(r.Context(), input.TargetIDs)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_RUN", err.Error())
		return
	}
	requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len([]rune(requestKey)) > 200 || strings.ContainsAny(requestKey, "\r\n") {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_RUN", "Idempotency-Key 无效")
		return
	}
	runID := newBackupRunID()
	taskID := newTaskID()
	idempotencyKey := "backup:" + runID
	if requestKey != "" {
		idempotencyKey = "backup:" + requestKey
		var existingID, status string
		var existingRunID sql.NullString
		if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT t.id, t.status, br.id FROM tasks t LEFT JOIN backup_runs br ON br.task_id = t.id WHERE t.idempotency_key = ?"), idempotencyKey).Scan(&existingID, &status, &existingRunID); err == nil {
			response := map[string]any{"taskId": existingID, "status": status, "reused": true}
			if existingRunID.Valid {
				response["runId"] = existingRunID.String
			}
			writeJSON(w, http.StatusAccepted, response)
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, r, http.StatusInternalServerError, "BACKUP_TASK_CREATE_FAILED", "读取备份任务状态失败")
			return
		}
	}
	payload := backupTaskPayload{RunID: runID, TargetIDs: ids, IncludeUserState: input.IncludeUserState, IncludeEncryptedConfig: input.IncludeEncryptedConfig, RequestedBy: principal.User.ID}
	payloadJSON, _ := json.Marshal(payload)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TASK_CREATE_FAILED", "创建备份任务失败")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), s.db.Rebind("INSERT INTO tasks (id, type, idempotency_key, status, payload_json, created_at, updated_at) VALUES (?, 'backup', ?, 'queued', ?, ?, ?)"), taskID, idempotencyKey, string(payloadJSON), now, now); err != nil {
		writeError(w, r, http.StatusConflict, "BACKUP_TASK_CREATE_FAILED", "创建备份任务失败")
		return
	}
	if _, err = tx.ExecContext(r.Context(), s.db.Rebind("INSERT INTO backup_runs (id, task_id, requested_by, include_user_state, include_encrypted_config, created_at) VALUES (?, ?, ?, ?, ?, ?)"), runID, taskID, principal.User.ID, boolInt(input.IncludeUserState), boolInt(input.IncludeEncryptedConfig), now); err != nil {
		writeError(w, r, http.StatusConflict, "BACKUP_TASK_CREATE_FAILED", "创建备份运行记录失败")
		return
	}
	for _, targetID := range ids {
		var targetType, displayName string
		if err := tx.QueryRowContext(r.Context(), s.db.Rebind("SELECT target_type, display_name FROM backup_targets WHERE id = ? AND enabled = 1"), targetID).Scan(&targetType, &displayName); err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_RUN", "备份目标不存在或已停用")
			return
		}
		if _, err = tx.ExecContext(r.Context(), s.db.Rebind("INSERT INTO backup_run_targets (run_id, target_id, target_type, display_name, status) VALUES (?, ?, ?, ?, 'queued')"), runID, targetID, targetType, displayName); err != nil {
			writeError(w, r, http.StatusConflict, "BACKUP_TASK_CREATE_FAILED", "创建备份目标运行记录失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_TASK_CREATE_FAILED", "创建备份任务失败")
		return
	}
	metadata, _ := json.Marshal(map[string]any{"targetCount": len(ids), "includeUserState": input.IncludeUserState, "includeEncryptedConfig": input.IncludeEncryptedConfig})
	s.audit(r.Context(), principal.User.ID, "backup_started", "backup_run", runID, string(metadata))
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": taskID, "runId": runID, "status": "queued", "targetIds": ids, "reused": false})
}

func (s *Service) resolveBackupTargetIDs(ctx context.Context, requested []string) ([]string, error) {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(requested))
	if len(requested) == 0 {
		rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id FROM backup_targets WHERE enabled = 1 ORDER BY display_name, id"))
		if err != nil {
			return nil, errors.New("读取备份目标失败")
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, errors.New("读取备份目标失败")
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return nil, errors.New("读取备份目标失败")
		}
	} else {
		for _, raw := range requested {
			id := strings.TrimSpace(raw)
			if id == "" || len([]rune(id)) > 160 {
				return nil, errors.New("备份目标 ID 无效")
			}
			if _, ok := seen[id]; ok {
				continue
			}
			ids = append(ids, id)
			seen[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("至少需要一个启用的备份目标")
	}
	return ids, nil
}

func (s *Service) getBackupRun(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	view, err := s.loadBackupRun(r.Context(), taskID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "BACKUP_RUN_NOT_FOUND", "备份运行记录不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "BACKUP_RUN_READ_FAILED", "读取备份运行记录失败")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Service) loadBackupRun(ctx context.Context, taskID string) (backupRunView, error) {
	var view backupRunView
	var completed sql.NullString
	var includeUserState, includeEncryptedConfig int
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(`SELECT br.id, br.task_id, t.status, br.include_user_state, br.include_encrypted_config, COALESCE(br.artifact_name, ''), COALESCE(br.artifact_sha256, ''), COALESCE(br.artifact_size, 0), br.created_at, br.completed_at FROM backup_runs br JOIN tasks t ON t.id = br.task_id WHERE br.task_id = ?`), taskID).Scan(&view.ID, &view.TaskID, &view.TaskStatus, &includeUserState, &includeEncryptedConfig, &view.ArtifactName, &view.ArtifactSHA256, &view.ArtifactSize, &view.CreatedAt, &completed); err != nil {
		return backupRunView{}, err
	}
	view.IncludeUserState = includeUserState == 1
	view.IncludeEncryptedConfig = includeEncryptedConfig == 1
	if completed.Valid {
		view.CompletedAt = &completed.String
	}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(`SELECT target_id, target_type, display_name, status, COALESCE(artifact_path, ''), bytes_written, COALESCE(checksum, ''), COALESCE(error_message, ''), started_at, completed_at FROM backup_run_targets WHERE run_id = ? ORDER BY display_name, target_id`), view.ID)
	if err != nil {
		return backupRunView{}, err
	}
	defer rows.Close()
	view.Targets = make([]backupRunTargetView, 0)
	for rows.Next() {
		var target backupRunTargetView
		var started, ended sql.NullString
		if err := rows.Scan(&target.TargetID, &target.TargetType, &target.DisplayName, &target.Status, &target.ArtifactPath, &target.BytesWritten, &target.Checksum, &target.ErrorMessage, &started, &ended); err != nil {
			return backupRunView{}, err
		}
		target.ErrorMessage = redactSummary(target.ErrorMessage)
		if started.Valid {
			target.StartedAt = &started.String
		}
		if ended.Valid {
			target.CompletedAt = &ended.String
		}
		view.Targets = append(view.Targets, target)
	}
	return view, rows.Err()
}

func newBackupRunID() string { return fmt.Sprintf("backup_run_%x", time.Now().UTC().UnixNano()) }

func newTaskID() string { return fmt.Sprintf("task_%x", time.Now().UTC().UnixNano()) }

func (s *Service) StartWorker(ctx context.Context) {
	if s.workerCancel != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE tasks SET status = 'queued', progress = 0, updated_at = ? WHERE type = 'backup' AND status = 'running'"), now)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE task_attempts SET status = 'retrying', finished_at = ? WHERE status = 'running' AND task_id IN (SELECT id FROM tasks WHERE type = 'backup')"), now)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_run_targets SET status = 'queued', started_at = NULL WHERE status = 'running' AND run_id IN (SELECT id FROM backup_runs WHERE task_id IN (SELECT id FROM tasks WHERE type = 'backup'))"))
	workerCtx, cancel := context.WithCancel(ctx)
	s.workerCancel = cancel
	s.workerDone = make(chan struct{})
	go func() {
		defer close(s.workerDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				task, claimed, err := s.claimBackupTask(workerCtx)
				if err != nil || !claimed {
					continue
				}
				s.processBackupTask(workerCtx, task)
			}
		}
	}()
}

func (s *Service) StopWorker(ctx context.Context) {
	if s.workerCancel == nil || s.workerDone == nil {
		return
	}
	s.workerCancel()
	select {
	case <-s.workerDone:
	case <-ctx.Done():
	}
	s.workerCancel = nil
	s.workerDone = nil
}

func (s *Service) claimBackupTask(ctx context.Context) (backupTaskRecord, bool, error) {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return backupTaskRecord{}, false, err
	}
	defer tx.Rollback()
	var task backupTaskRecord
	var payload string
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id, type, payload_json, retry_count, max_retries FROM tasks WHERE type = 'backup' AND status = 'queued' AND retry_count < max_retries ORDER BY created_at, id LIMIT 1")).Scan(&task.ID, &task.Type, &payload, &task.RetryCount, &task.MaxRetries); errors.Is(err, sql.ErrNoRows) {
		return backupTaskRecord{}, false, nil
	} else if err != nil {
		return backupTaskRecord{}, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE tasks SET status = 'running', progress = 5, started_at = ?, updated_at = ?, error_code = NULL, error_message = NULL WHERE id = ? AND status = 'queued'"), now, now, task.ID)
	if err != nil {
		return backupTaskRecord{}, false, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return backupTaskRecord{}, false, nil
	}
	var latestAttempt int
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COALESCE(MAX(attempt_number), 0) FROM task_attempts WHERE task_id = ?"), task.ID).Scan(&latestAttempt); err != nil {
		return backupTaskRecord{}, false, err
	}
	task.AttemptNumber = latestAttempt + 1
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO task_attempts (id, task_id, attempt_number, status, started_at) VALUES (?, ?, ?, 'running', ?)"), fmt.Sprintf("attempt_%x", time.Now().UTC().UnixNano()), task.ID, task.AttemptNumber, now); err != nil {
		return backupTaskRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return backupTaskRecord{}, false, err
	}
	_ = payload
	task.Status = "running"
	return task, true, nil
}

func (s *Service) processBackupTask(ctx context.Context, task backupTaskRecord) {
	var rawPayload string
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT payload_json FROM tasks WHERE id = ?"), task.ID).Scan(&rawPayload); err != nil {
		s.failBackupTask(ctx, task, "INVALID_BACKUP_PAYLOAD", "备份任务载荷不可用")
		return
	}
	var payload backupTaskPayload
	if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil || payload.RunID == "" {
		s.failBackupTask(ctx, task, "INVALID_BACKUP_PAYLOAD", "备份任务载荷不可用")
		return
	}
	if s.backupTaskCancelled(ctx, task.ID, task.AttemptNumber) {
		s.cancelBackupTask(ctx, task, payload.RunID)
		return
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_run_targets SET status = 'queued', artifact_path = NULL, bytes_written = 0, checksum = NULL, error_message = NULL, started_at = NULL, completed_at = NULL WHERE run_id = ?"), payload.RunID)
	artifact, err := s.buildBackupArtifact(ctx, payload)
	if err != nil {
		s.markAllBackupTargetsFailed(ctx, payload.RunID, "备份归档生成失败")
		s.failBackupTask(ctx, task, "BACKUP_BUILD_FAILED", "备份归档生成失败")
		return
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE tasks SET progress = 15, updated_at = ? WHERE id = ? AND status = 'running'"), time.Now().UTC().Format(time.RFC3339Nano), task.ID)
	targets, err := s.loadBackupTargetJobs(ctx, payload.RunID)
	if err != nil || len(targets) == 0 {
		s.markAllBackupTargetsFailed(ctx, payload.RunID, "备份目标不可用")
		s.failBackupTask(ctx, task, "BACKUP_TARGETS_UNAVAILABLE", "备份目标不可用")
		return
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].TargetType == targets[j].TargetType {
			return targets[i].DisplayName < targets[j].DisplayName
		}
		return targets[i].TargetType == "local"
	})
	successCount, failureCount := 0, 0
	for index, target := range targets {
		if s.backupTaskCancelled(ctx, task.ID, task.AttemptNumber) {
			s.markRemainingBackupTargetsCancelled(ctx, payload.RunID, targets[index:])
			s.cancelBackupTask(ctx, task, payload.RunID)
			return
		}
		s.markBackupTargetRunning(ctx, payload.RunID, target.ID)
		result := backupTargetResult{}
		if target.Missing {
			result = backupTargetResult{Status: "failed", Error: "备份目标已删除"}
		} else {
			result = s.writeBackupTarget(ctx, target, artifact)
		}
		s.recordBackupTargetResult(ctx, payload.RunID, target, result)
		if result.Status == "succeeded" {
			successCount++
		} else {
			failureCount++
		}
		progress := 15 + int(float64(index+1)/float64(len(targets))*80)
		if progress > 99 {
			progress = 99
		}
		_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE tasks SET progress = ?, updated_at = ? WHERE id = ? AND status = 'running'"), progress, time.Now().UTC().Format(time.RFC3339Nano), task.ID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_runs SET artifact_name = ?, artifact_sha256 = ?, artifact_size = ?, completed_at = ? WHERE id = ?"), artifact.Name, artifact.SHA256, len(artifact.Data), now, payload.RunID)
	if failureCount == 0 && successCount > 0 {
		s.finishBackupTask(ctx, task, "succeeded", "", "")
		s.audit(ctx, payload.RequestedBy, "backup_completed", "backup_run", payload.RunID, s.backupOutcomeMetadata(ctx, payload.RunID, "succeeded"))
		return
	}
	if successCount > 0 {
		s.finishBackupTask(ctx, task, "partial", "BACKUP_TARGET_PARTIAL", s.backupFailureSummary(ctx, payload.RunID))
		s.audit(ctx, payload.RequestedBy, "backup_completed", "backup_run", payload.RunID, s.backupOutcomeMetadata(ctx, payload.RunID, "partial"))
		return
	}
	s.failBackupTask(ctx, task, "BACKUP_ALL_TARGETS_FAILED", s.backupFailureSummary(ctx, payload.RunID))
	s.audit(ctx, payload.RequestedBy, "backup_completed", "backup_run", payload.RunID, s.backupOutcomeMetadata(ctx, payload.RunID, "failed"))
}

func (s *Service) loadBackupTargetJobs(ctx context.Context, runID string) ([]backupTargetJob, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(`SELECT rt.target_id, rt.target_type, rt.display_name, bt.id, COALESCE(bt.retention_count, 0), COALESCE(bt.encrypted_config, '') FROM backup_run_targets rt LEFT JOIN backup_targets bt ON bt.id = rt.target_id WHERE rt.run_id = ? ORDER BY rt.display_name, rt.target_id`), runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]backupTargetJob, 0)
	for rows.Next() {
		var job backupTargetJob
		var currentID string
		var encrypted string
		if err := rows.Scan(&job.ID, &job.TargetType, &job.DisplayName, &currentID, &job.RetentionCount, &encrypted); err != nil {
			return nil, err
		}
		job.Missing = currentID == ""
		if job.Missing {
			jobs = append(jobs, job)
			continue
		}
		if encrypted == "" {
			job.Config = targetConfig{}
		} else {
			cfg, err := s.decryptConfig(encrypted)
			if err != nil {
				return nil, err
			}
			job.Config = cfg
		}
		if job.RetentionCount < 1 {
			job.RetentionCount = defaultRetentionCount
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Service) backupTaskCancelled(ctx context.Context, taskID string, attempt int) bool {
	var cancelled int
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(`SELECT CASE WHEN t.status <> 'running' OR a.status <> 'running' THEN 1 ELSE 0 END FROM tasks t JOIN task_attempts a ON a.task_id = t.id AND a.attempt_number = ? WHERE t.id = ?`), attempt, taskID).Scan(&cancelled)
	return err != nil || cancelled == 1
}

func (s *Service) markBackupTargetRunning(ctx context.Context, runID, targetID string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_run_targets SET status = 'running', started_at = ?, error_message = NULL WHERE run_id = ? AND target_id = ?"), time.Now().UTC().Format(time.RFC3339Nano), runID, targetID)
}

func (s *Service) recordBackupTargetResult(ctx context.Context, runID string, target backupTargetJob, result backupTargetResult) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_run_targets SET status = ?, artifact_path = ?, bytes_written = ?, checksum = ?, error_message = ?, completed_at = ? WHERE run_id = ? AND target_id = ?"), result.Status, nullable(result.ArtifactPath), result.BytesWritten, nullable(result.Checksum), nullable(result.Error), now, runID, target.ID)
	lastStatus := result.Status
	lastError := result.Error
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_targets SET last_run_at = ?, last_status = ?, last_error = ?, updated_at = ? WHERE id = ?"), now, lastStatus, nullable(lastError), now, target.ID)
}

func (s *Service) markAllBackupTargetsFailed(ctx context.Context, runID, message string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_run_targets SET status = 'failed', error_message = ?, completed_at = ? WHERE run_id = ? AND status IN ('queued', 'running')"), message, now, runID)
}

func (s *Service) markRemainingBackupTargetsCancelled(ctx context.Context, runID string, targets []backupTargetJob) {
	if len(targets) == 0 {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, target := range targets {
		_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_run_targets SET status = 'cancelled', error_message = ?, completed_at = ? WHERE run_id = ? AND target_id = ? AND status IN ('queued', 'running')"), "任务已取消", now, runID, target.ID)
	}
}

func (s *Service) finishBackupTask(ctx context.Context, task backupTaskRecord, status, code, message string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE tasks SET status = ?, progress = ?, error_code = ?, error_message = ?, finished_at = ?, updated_at = ? WHERE id = ? AND status = 'running' AND EXISTS (SELECT 1 FROM task_attempts WHERE task_id = ? AND attempt_number = ? AND status = 'running')"), status, backupProgressForStatus(status), nullable(code), nullable(message), now, now, task.ID, task.ID, task.AttemptNumber)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE task_attempts SET status = ?, error_code = ?, error_message = ?, finished_at = ? WHERE task_id = ? AND attempt_number = ? AND status = 'running'"), status, nullable(code), nullable(message), now, task.ID, task.AttemptNumber)
}

func backupProgressForStatus(status string) int {
	if status == "succeeded" {
		return 100
	}
	if status == "partial" {
		return 100
	}
	return 0
}

func (s *Service) failBackupTask(ctx context.Context, task backupTaskRecord, code, message string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	message = redactSummary(message)
	nextStatus := "failed"
	if task.RetryCount+1 < task.MaxRetries {
		nextStatus = "queued"
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE tasks SET status = ?, retry_count = retry_count + 1, progress = 0, error_code = ?, error_message = ?, finished_at = CASE WHEN ? = 'failed' THEN ? ELSE NULL END, updated_at = ? WHERE id = ? AND status = 'running' AND EXISTS (SELECT 1 FROM task_attempts WHERE task_id = ? AND attempt_number = ? AND status = 'running')"), nextStatus, code, message, nextStatus, now, now, task.ID, task.ID, task.AttemptNumber)
	attemptStatus := "failed"
	if nextStatus == "queued" {
		attemptStatus = "retrying"
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE task_attempts SET status = ?, error_code = ?, error_message = ?, finished_at = ? WHERE task_id = ? AND attempt_number = ? AND status = 'running'"), attemptStatus, code, message, now, task.ID, task.AttemptNumber)
}

func (s *Service) cancelBackupTask(ctx context.Context, task backupTaskRecord, runID string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE tasks SET status = 'cancelled', progress = 0, finished_at = ?, updated_at = ? WHERE id = ? AND status = 'running'"), now, now, task.ID)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE task_attempts SET status = 'cancelled', error_message = ?, finished_at = ? WHERE task_id = ? AND attempt_number = ? AND status = 'running'"), "任务已取消", now, task.ID, task.AttemptNumber)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE backup_run_targets SET status = 'cancelled', error_message = ?, completed_at = ? WHERE run_id = ? AND status IN ('queued', 'running')"), "任务已取消", now, runID)
}

func (s *Service) backupFailureSummary(ctx context.Context, runID string) string {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT display_name, COALESCE(error_message, '') FROM backup_run_targets WHERE run_id = ? AND status = 'failed' ORDER BY display_name"), runID)
	if err != nil {
		return "部分备份目标失败"
	}
	defer rows.Close()
	parts := make([]string, 0)
	for rows.Next() {
		var name, message string
		if rows.Scan(&name, &message) == nil {
			message = redactSummary(message)
			if message == "" {
				message = "目标不可用"
			}
			parts = append(parts, name+": "+message)
		}
	}
	if len(parts) == 0 {
		return "部分备份目标失败"
	}
	return strings.Join(parts, "; ")
}

func (s *Service) backupOutcomeMetadata(ctx context.Context, runID, status string) string {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT target_id, target_type, status, bytes_written, COALESCE(error_message, '') FROM backup_run_targets WHERE run_id = ? ORDER BY target_id"), runID)
	if err != nil {
		return fmt.Sprintf(`{"status":%q}`, status)
	}
	defer rows.Close()
	targets := make([]map[string]any, 0)
	for rows.Next() {
		var id, targetType, targetStatus, message string
		var bytesWritten int64
		if rows.Scan(&id, &targetType, &targetStatus, &bytesWritten, &message) == nil {
			targets = append(targets, map[string]any{"targetId": id, "targetType": targetType, "status": targetStatus, "bytesWritten": bytesWritten, "error": redactSummary(message)})
		}
	}
	metadata, _ := json.Marshal(map[string]any{"status": status, "targets": targets})
	return string(metadata)
}

func (s *Service) buildBackupArtifact(ctx context.Context, payload backupTaskPayload) (backupArtifact, error) {
	doc, err := s.buildExport(ctx, payload.IncludeUserState)
	if err != nil {
		return backupArtifact{}, err
	}
	databaseJSON, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return backupArtifact{}, err
	}
	files := []struct {
		name string
		data []byte
	}{
		{name: "database.json", data: append(databaseJSON, '\n')},
	}
	if payload.IncludeEncryptedConfig {
		configData, err := s.buildEncryptedConfigExport(ctx, payload.TargetIDs)
		if err != nil {
			return backupArtifact{}, err
		}
		files = append(files, struct {
			name string
			data []byte
		}{name: "encrypted-config.json", data: append(configData, '\n')})
	}
	manifest := backupManifest{
		FormatVersion:          backupArchiveFormatVersion,
		CreatedAt:              time.Now().UTC().Format(time.RFC3339Nano),
		ApplicationVersion:     "jmlab",
		MigrationVersion:       s.currentMigrationVersion(ctx),
		IncludeUserState:       payload.IncludeUserState,
		IncludeEncryptedConfig: payload.IncludeEncryptedConfig,
		Files:                  make([]backupManifestFile, 0, len(files)),
	}
	for _, file := range files {
		hash := sha256.Sum256(file.data)
		manifest.Files = append(manifest.Files, backupManifestFile{Name: file.name, Size: len(file.data), SHA256: fmt.Sprintf("%x", hash[:])})
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return backupArtifact{}, err
	}
	manifestJSON = append(manifestJSON, '\n')
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	writeTarFile := func(name string, data []byte) error {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now().UTC()}); err != nil {
			return err
		}
		_, err := tarWriter.Write(data)
		return err
	}
	if err := writeTarFile("manifest.json", manifestJSON); err != nil {
		return backupArtifact{}, err
	}
	for _, file := range files {
		if err := writeTarFile(file.name, file.data); err != nil {
			return backupArtifact{}, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return backupArtifact{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return backupArtifact{}, err
	}
	data := archive.Bytes()
	hash := sha256.Sum256(data)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	return backupArtifact{Name: fmt.Sprintf("jmlab-backup-%s-%s.tar.gz", stamp, safeFilenamePart(payload.RunID)), Data: data, SHA256: fmt.Sprintf("%x", hash[:])}, nil
}

func (s *Service) currentMigrationVersion(ctx context.Context) string {
	var version sql.NullInt64
	if err := s.db.DB().QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version").Scan(&version); err != nil || !version.Valid {
		return ""
	}
	return fmt.Sprintf("%d", version.Int64)
}

func safeFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "run"
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "run"
	}
	return builder.String()
}

func (s *Service) writeBackupTarget(ctx context.Context, target backupTargetJob, artifact backupArtifact) backupTargetResult {
	switch target.TargetType {
	case "local":
		return s.writeLocalBackup(ctx, target, artifact)
	case "webdav":
		return s.writeWebDAVBackup(ctx, target, artifact)
	case "git":
		return s.writeGitBackup(ctx, target, artifact)
	default:
		return backupTargetResult{Status: "failed", Error: "备份目标类型不受支持"}
	}
}

func (s *Service) writeLocalBackup(_ context.Context, target backupTargetJob, artifact backupArtifact) backupTargetResult {
	path, err := normalizeBackupPath(target.Config.Path)
	if err != nil {
		return backupTargetResult{Status: "failed", Error: err.Error()}
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return backupTargetResult{Status: "failed", Error: "本地备份目录不可写"}
	}
	temporary, err := os.CreateTemp(path, ".jmlab-backup-*.tmp")
	if err != nil {
		return backupTargetResult{Status: "failed", Error: "本地备份文件创建失败"}
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return backupTargetResult{Status: "failed", Error: "本地备份文件权限设置失败"}
	}
	if _, err := temporary.Write(artifact.Data); err != nil {
		_ = temporary.Close()
		return backupTargetResult{Status: "failed", Error: "本地备份写入失败"}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return backupTargetResult{Status: "failed", Error: "本地备份完整性校验失败"}
	}
	if err := temporary.Close(); err != nil {
		return backupTargetResult{Status: "failed", Error: "本地备份写入失败"}
	}
	finalPath := filepath.Join(path, artifact.Name)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return backupTargetResult{Status: "failed", Error: "本地备份提交失败"}
	}
	if err := removeLocalBackupOverflow(path, target.RetentionCount); err != nil {
		return backupTargetResult{Status: "failed", ArtifactPath: finalPath, BytesWritten: int64(len(artifact.Data)), Checksum: artifact.SHA256, Error: "本地备份保留清理失败"}
	}
	return backupTargetResult{Status: "succeeded", ArtifactPath: finalPath, BytesWritten: int64(len(artifact.Data)), Checksum: artifact.SHA256}
}

func removeLocalBackupOverflow(path string, retention int) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	files := make([]os.FileInfo, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "jmlab-backup-") || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, info)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime().Equal(files[j].ModTime()) {
			return files[i].Name() > files[j].Name()
		}
		return files[i].ModTime().After(files[j].ModTime())
	})
	if retention < 1 {
		retention = 1
	}
	if len(files) <= retention {
		return nil
	}
	for _, info := range files[retention:] {
		if err := os.Remove(filepath.Join(path, info.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Service) writeWebDAVBackup(ctx context.Context, target backupTargetJob, artifact backupArtifact) backupTargetResult {
	artifactURL, err := appendRemoteFilename(target.Config.URL, artifact.Name)
	if err != nil {
		return backupTargetResult{Status: "failed", Error: "WebDAV 地址无效"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, artifactURL, bytes.NewReader(artifact.Data))
	if err != nil {
		return backupTargetResult{Status: "failed", Error: "WebDAV 请求创建失败"}
	}
	request.Header.Set("Content-Type", "application/gzip")
	applyWebDAVAuth(request, target.Config)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return backupTargetResult{Status: "failed", Error: "WebDAV 上传失败"}
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return backupTargetResult{Status: "failed", Error: fmt.Sprintf("WebDAV 返回 HTTP %d", response.StatusCode)}
	}
	if err := removeWebDAVOverflow(ctx, target.Config, target.RetentionCount); err != nil {
		return backupTargetResult{Status: "failed", ArtifactPath: artifactURL, BytesWritten: int64(len(artifact.Data)), Checksum: artifact.SHA256, Error: "WebDAV 保留清理失败"}
	}
	return backupTargetResult{Status: "succeeded", ArtifactPath: artifactURL, BytesWritten: int64(len(artifact.Data)), Checksum: artifact.SHA256}
}

func appendRemoteFilename(rawURL, filename string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("invalid WebDAV URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(filename)
	return parsed.String(), nil
}

type webDAVMultiStatus struct {
	Responses []struct {
		Href string `xml:"href"`
	} `xml:"response"`
}

func removeWebDAVOverflow(ctx context.Context, cfg targetConfig, retention int) error {
	base, err := normalizedWebDAVBase(cfg.URL)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "PROPFIND", base, strings.NewReader(`<?xml version="1.0" encoding="utf-8" ?><d:propfind xmlns:d="DAV:"><d:propname/></d:propfind>`))
	if err != nil {
		return err
	}
	request.Header.Set("Depth", "1")
	request.Header.Set("Content-Type", "application/xml")
	applyWebDAVAuth(request, cfg)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("WebDAV 返回 HTTP %d", response.StatusCode)
	}
	var listing webDAVMultiStatus
	if err := xml.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&listing); err != nil {
		return err
	}
	files := make([]string, 0)
	for _, item := range listing.Responses {
		href, err := url.PathUnescape(item.Href)
		if err != nil {
			continue
		}
		if parsed, parseErr := url.Parse(href); parseErr == nil && parsed.Path != "" {
			href = parsed.Path
		}
		name := path.Base(strings.TrimRight(href, "/"))
		if strings.HasPrefix(name, "jmlab-backup-") && strings.HasSuffix(name, ".tar.gz") {
			files = append(files, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	if retention < 1 {
		retention = 1
	}
	if len(files) <= retention {
		return nil
	}
	for _, name := range files[retention:] {
		deleteURL, err := appendRemoteFilename(cfg.URL, name)
		if err != nil {
			return err
		}
		deleteRequest, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
		if err != nil {
			return err
		}
		applyWebDAVAuth(deleteRequest, cfg)
		deleteResponse, err := client.Do(deleteRequest)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, deleteResponse.Body)
		_ = deleteResponse.Body.Close()
		if deleteResponse.StatusCode < 200 || deleteResponse.StatusCode >= 300 {
			return fmt.Errorf("WebDAV 删除旧备份返回 HTTP %d", deleteResponse.StatusCode)
		}
	}
	return nil
}

func normalizedWebDAVBase(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("invalid WebDAV URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	return parsed.String(), nil
}

func (s *Service) writeGitBackup(ctx context.Context, target backupTargetJob, artifact backupArtifact) backupTargetResult {
	temporary, err := os.MkdirTemp("", "jmlab-backup-git-*")
	if err != nil {
		return backupTargetResult{Status: "failed", Error: "Git 临时目录创建失败"}
	}
	defer os.RemoveAll(temporary)
	env, cleanup, err := gitAuthEnvironment(target.Config)
	if err != nil {
		return backupTargetResult{Status: "failed", Error: err.Error()}
	}
	defer cleanup()
	if err := cloneOrInitGit(ctx, env, target.Config, temporary); err != nil {
		return backupTargetResult{Status: "failed", Error: "Git 仓库读取失败"}
	}
	artifactPath := filepath.Join(temporary, artifact.Name)
	if err := os.WriteFile(artifactPath, artifact.Data, 0o600); err != nil {
		return backupTargetResult{Status: "failed", Error: "Git 备份文件写入失败"}
	}
	if err := removeGitBackupOverflow(temporary, target.RetentionCount, artifact.Name); err != nil {
		return backupTargetResult{Status: "failed", Error: "Git 保留清理失败"}
	}
	commands := [][]string{
		{"git", "-C", temporary, "config", "user.name", "JMlab Backup"},
		{"git", "-C", temporary, "config", "user.email", "backup@jmlab.invalid"},
		{"git", "-C", temporary, "add", "--", "."},
		{"git", "-C", temporary, "commit", "-m", "JMlab backup"},
		{"git", "-C", temporary, "push", "origin", "HEAD:" + target.Config.Branch},
	}
	for _, command := range commands {
		if len(command) == 0 {
			return backupTargetResult{Status: "failed", Error: "Git 备份提交失败"}
		}
		if err := runCommand(ctx, env, command[0], command[1:]...); err != nil {
			return backupTargetResult{Status: "failed", Error: "Git 备份提交失败"}
		}
	}
	return backupTargetResult{Status: "succeeded", ArtifactPath: target.Config.Branch + "/" + artifact.Name, BytesWritten: int64(len(artifact.Data)), Checksum: artifact.SHA256}
}

func gitAuthEnvironment(cfg targetConfig) ([]string, func(), error) {
	env := append([]string{}, os.Environ()...)
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	cleanupPaths := make([]string, 0, 3)
	cleanup := func() {
		for _, path := range cleanupPaths {
			_ = os.Remove(path)
		}
	}
	if cfg.AuthType == "token" {
		file, err := os.CreateTemp("", "jmlab-git-askpass-*")
		if err != nil {
			return nil, cleanup, errors.New("Git 凭证临时文件创建失败")
		}
		path := file.Name()
		cleanupPaths = append(cleanupPaths, path)
		_ = file.Chmod(0o700)
		if _, err := file.WriteString("#!/bin/sh\ncase \"$1\" in *Username*) printf '%s\\n' \"$JMLAB_BACKUP_GIT_USERNAME\" ;; *) printf '%s\\n' \"$JMLAB_BACKUP_GIT_TOKEN\" ;; esac\n"); err != nil {
			_ = file.Close()
			return nil, cleanup, errors.New("Git 凭证临时文件写入失败")
		}
		_ = file.Close()
		env = append(env, "GIT_ASKPASS="+path, "JMLAB_BACKUP_GIT_USERNAME="+cfg.Username, "JMLAB_BACKUP_GIT_TOKEN="+cfg.Token)
	}
	if cfg.AuthType == "ssh" {
		key, err := os.CreateTemp("", "jmlab-git-key-*")
		if err != nil {
			return nil, cleanup, errors.New("Git SSH 私钥临时文件创建失败")
		}
		keyPath := key.Name()
		cleanupPaths = append(cleanupPaths, keyPath)
		_ = key.Chmod(0o600)
		if _, err := key.WriteString(cfg.SSHPrivateKey); err != nil {
			_ = key.Close()
			return nil, cleanup, errors.New("Git SSH 私钥写入失败")
		}
		_ = key.Close()
		hosts, err := os.CreateTemp("", "jmlab-git-known-hosts-*")
		if err != nil {
			return nil, cleanup, errors.New("Git known_hosts 临时文件创建失败")
		}
		hostsPath := hosts.Name()
		cleanupPaths = append(cleanupPaths, hostsPath)
		_ = hosts.Chmod(0o600)
		if _, err := hosts.WriteString(cfg.KnownHosts); err != nil {
			_ = hosts.Close()
			return nil, cleanup, errors.New("Git known_hosts 写入失败")
		}
		_ = hosts.Close()
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+keyPath+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="+hostsPath)
	}
	return env, cleanup, nil
}

func cloneOrInitGit(ctx context.Context, env []string, cfg targetConfig, path string) error {
	resetPath := func() error {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return os.MkdirAll(path, 0o700)
	}
	if err := runCommand(ctx, env, "git", "clone", "--branch", cfg.Branch, cfg.Remote, path); err == nil {
		return ensureGitBranch(ctx, env, path, cfg.Branch)
	}
	if err := resetPath(); err != nil {
		return err
	}
	if err := runCommand(ctx, env, "git", "clone", cfg.Remote, path); err == nil {
		return ensureGitBranch(ctx, env, path, cfg.Branch)
	}
	if err := resetPath(); err != nil {
		return err
	}
	if err := runCommand(ctx, env, "git", "init", "-b", cfg.Branch, path); err != nil {
		return err
	}
	if err := runCommand(ctx, env, "git", "-C", path, "remote", "add", "origin", cfg.Remote); err != nil {
		return err
	}
	return nil
}

func ensureGitBranch(ctx context.Context, env []string, path, branch string) error {
	if err := runCommand(ctx, env, "git", "-C", path, "checkout", "-B", branch); err != nil {
		return err
	}
	return nil
}

func removeGitBackupOverflow(path string, retention int, current string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "jmlab-backup-") && strings.HasSuffix(entry.Name(), ".tar.gz") {
			files = append(files, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	if retention < 1 {
		retention = 1
	}
	if len(files) <= retention {
		return nil
	}
	for _, name := range files[retention:] {
		if name == current {
			continue
		}
		if err := os.Remove(filepath.Join(path, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func runCommand(ctx context.Context, env []string, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = env
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}
