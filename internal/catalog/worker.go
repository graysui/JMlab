package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/metadata"
	"github.com/graysui/JMlab/internal/ranking"
)

type taskRecord struct {
	ID            string              `json:"id"`
	AttemptNumber int                 `json:"-"`
	Type          string              `json:"type"`
	Status        string              `json:"status"`
	Progress      int                 `json:"progress"`
	RetryCount    int                 `json:"retryCount"`
	MaxRetries    int                 `json:"maxRetries"`
	ErrorCode     string              `json:"errorCode,omitempty"`
	ErrorMessage  string              `json:"errorMessage,omitempty"`
	StartedAt     *string             `json:"startedAt,omitempty"`
	FinishedAt    *string             `json:"finishedAt,omitempty"`
	CreatedAt     string              `json:"createdAt"`
	UpdatedAt     string              `json:"updatedAt"`
	Attempts      []taskAttemptRecord `json:"attempts,omitempty"`
	CanRetry      bool                `json:"canRetry"`
	CanCancel     bool                `json:"canCancel"`
}

type taskAttemptRecord struct {
	ID            string  `json:"id"`
	AttemptNumber int     `json:"attemptNumber"`
	Status        string  `json:"status"`
	ErrorCode     string  `json:"errorCode,omitempty"`
	ErrorMessage  string  `json:"errorMessage,omitempty"`
	StartedAt     string  `json:"startedAt"`
	FinishedAt    *string `json:"finishedAt,omitempty"`
}

func (s *Service) SetSourceAdapter(adapter metadata.SourceAdapter) {
	s.adapter = adapter
}

func (s *Service) SetSourceImageImporter(importer func(context.Context, string, string, string, string, int, int) error) {
	s.imageImporter = importer
}

// SetSourceSeeder wires the optional playback source matcher without making
// catalog depend on the playback package.
func (s *Service) SetSourceSeeder(seeder func(context.Context, Item) error) { s.sourceSeeder = seeder }

func (s *Service) seedSources(ctx context.Context, item Item) {
	if s.sourceSeeder == nil {
		return
	}
	if err := s.sourceSeeder(ctx, item); err != nil {
		s.logger.Warn("作品内置播放源匹配未完成", "itemId", item.ID, "error", redactTaskText(err.Error()))
	}
}

func (s *Service) StartWorker(ctx context.Context) {
	if s.workerCancel != nil {
		return
	}
	// A process can stop while a task is running. Put those tasks back in the
	// queue so the next process can continue them instead of leaving them stuck.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE tasks SET status = 'queued', progress = 0, updated_at = ? WHERE status = 'running'"), now)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE task_attempts SET status = 'retrying', finished_at = ? WHERE status = 'running'"), now)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("DELETE FROM ranking_generation_locks WHERE task_id NOT IN (SELECT id FROM tasks WHERE status IN ('queued', 'running'))"))

	workerCtx, cancel := context.WithCancel(ctx)
	s.workerCancel = cancel
	s.workerDone = make(chan struct{})
	go func() {
		defer close(s.workerDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		scheduleTicker := time.NewTicker(30 * time.Second)
		defer scheduleTicker.Stop()

		for {
			select {
			case <-workerCtx.Done():
				return
			case <-scheduleTicker.C:
				s.checkRankingSchedule(workerCtx)
			case <-ticker.C:
				task, claimed, err := s.claimTask(workerCtx)
				if err != nil || !claimed {
					continue
				}
				s.processTask(workerCtx, task)
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

func (s *Service) claimTask(ctx context.Context) (taskRecord, bool, error) {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return taskRecord{}, false, err
	}
	defer tx.Rollback()
	var task taskRecord
	var started, finished sql.NullString
	var payload string
	var retryCount int
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id, type, payload_json, retry_count, max_retries, created_at, updated_at, started_at, finished_at FROM tasks WHERE status = 'queued' AND retry_count < max_retries AND type <> 'backup' ORDER BY created_at, id LIMIT 1")).Scan(&task.ID, &task.Type, &payload, &retryCount, &task.MaxRetries, &task.CreatedAt, &task.UpdatedAt, &started, &finished); errors.Is(err, sql.ErrNoRows) {
		return taskRecord{}, false, nil
	} else if err != nil {
		return taskRecord{}, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE tasks SET status = 'running', progress = 5, started_at = ?, updated_at = ?, error_code = NULL, error_message = NULL WHERE id = ? AND status = 'queued'"), now, now, task.ID)
	if err != nil {
		return taskRecord{}, false, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return taskRecord{}, false, nil
	}
	var latestAttempt int
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COALESCE(MAX(attempt_number), 0) FROM task_attempts WHERE task_id = ?"), task.ID).Scan(&latestAttempt); err != nil {
		return taskRecord{}, false, err
	}
	attempt := latestAttempt + 1
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO task_attempts (id, task_id, attempt_number, status, started_at) VALUES (?, ?, ?, 'running', ?)"), newID("attempt"), task.ID, attempt, now); err != nil {
		return taskRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return taskRecord{}, false, err
	}
	task.Status = "running"
	task.Progress = 5
	task.RetryCount = retryCount
	task.AttemptNumber = attempt
	if started.Valid {
		task.StartedAt = &started.String
	}
	if finished.Valid {
		task.FinishedAt = &finished.String
	}
	_ = payload
	return task, true, nil
}

func (s *Service) processTask(ctx context.Context, task taskRecord) {
	if task.Type == "generate_ranking" {
		s.processRankingTask(ctx, task)
		return
	}
	if task.Type != "sync_item" {
		s.failTask(ctx, task, errors.New("不支持的任务类型"), "UNSUPPORTED_TASK")
		return
	}
	var payload struct {
		ItemID string `json:"itemId"`
	}
	var rawPayload string
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT payload_json FROM tasks WHERE id = ?"), task.ID).Scan(&rawPayload); err != nil || json.Unmarshal([]byte(rawPayload), &payload) != nil || strings.TrimSpace(payload.ItemID) == "" {
		s.failTask(ctx, task, errors.New("任务载荷缺少 itemId"), "INVALID_TASK_PAYLOAD")
		return
	}
	item, err := s.getByID(ctx, payload.ItemID, "", true)
	if err != nil {
		s.failTask(ctx, task, fmt.Errorf("读取作品失败: %w", err), "ITEM_NOT_FOUND")
		return
	}
	if item.MetadataModuleID != "" {
		entry, err := s.metadataModule(item.MetadataModuleID)
		if err != nil {
			s.failTask(ctx, task, err, "MODULE_UNAVAILABLE")
			return
		}
		detail, err := entry.MetadataInstance.GetDetail(ctx, item.SourceID, item.ProviderIDs)
		if err != nil {
			s.failTask(ctx, task, errors.New("来源详情读取失败"), "SOURCE_FETCH_FAILED")
			return
		}
		updated, _, err := s.saveModuleMedia(ctx, item.MetadataModuleID, detail, item.ParentID, item.IndexNumber)
		if err != nil {
			s.failTask(ctx, task, err, "CATALOG_UPDATE_FAILED")
			return
		}
		s.seedSources(ctx, updated)
		s.succeedTask(ctx, task)
		return
	}
	if s.adapter == nil {
		s.failTask(ctx, task, errors.New("元数据适配器未配置"), "ADAPTER_UNAVAILABLE")
		return
	}
	_ = s.updateTaskProgress(ctx, task.ID, task.AttemptNumber, 15)
	sourceItem, err := s.adapter.Fetch(ctx, item.SourceID, item.SourceURL)
	if err != nil {
		s.failTask(ctx, task, err, "SOURCE_FETCH_FAILED")
		return
	}
	_ = s.updateTaskProgress(ctx, task.ID, task.AttemptNumber, 65)
	if s.taskWasCancelled(ctx, task.ID, task.AttemptNumber) {
		return
	}
	input := ImportItem{Source: item.Source, SourceID: item.SourceID, Code: sourceItem.Code, RawCode: sourceItem.RawCode, Title: sourceItem.Title, OriginalTitle: sourceItem.OriginalTitle, Overview: sourceItem.Overview, ReleaseDate: sourceItem.ReleaseDate, Year: sourceItem.Year, RuntimeSeconds: sourceItem.RuntimeSeconds, SourceRating: sourceItem.Rating, RatingCount: sourceItem.RatingCount, SourceURL: sourceItem.URL}
	if input.Source == "" {
		input.Source = s.adapter.Name()
	}
	if input.SourceID == "" {
		input.SourceID = sourceItem.SourceID
	}
	if input.Code == "" {
		input.Code = item.Code
	}
	if input.RawCode == "" {
		input.RawCode = input.Code
	}
	if input.Title == "" {
		input.Title = item.Title
	}
	for _, person := range sourceItem.People {
		input.People = append(input.People, RelationInput{Name: person.Name, Role: person.Role})
	}
	for _, studio := range sourceItem.Studios {
		input.Studios = append(input.Studios, RelationInput{Name: studio.Name, Role: studio.Role})
	}
	for _, series := range sourceItem.Series {
		input.Series = append(input.Series, RelationInput{Name: series.Name, Role: series.Role})
	}
	for _, tag := range sourceItem.Tags {
		input.Tags = append(input.Tags, RelationInput{Name: tag.Name, Role: tag.Role})
	}
	updatedItem, _, err := s.upsert(ctx, input, "")
	if err != nil {
		s.failTask(ctx, task, err, "CATALOG_UPDATE_FAILED")
		return
	}
	s.importSourceImages(ctx, item.ID, sourceItem.Images)
	s.seedSources(ctx, updatedItem)
	s.succeedTask(ctx, task)
}

func (s *Service) processRankingTask(ctx context.Context, task taskRecord) {
	var modulePayload moduleRankingPayload
	var moduleRaw string
	if s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT payload_json FROM tasks WHERE id=?"), task.ID).Scan(&moduleRaw) == nil && json.Unmarshal([]byte(moduleRaw), &modulePayload) == nil && modulePayload.ModuleID != "" {
		s.processModuleRankingTask(ctx, task, modulePayload)
		return
	}

	if s.adapter == nil {
		s.failTask(ctx, task, errors.New("元数据适配器未配置"), "ADAPTER_UNAVAILABLE")
		return
	}
	var payload struct {
		RankingType string `json:"rankingType"`
		Period      string `json:"period"`
	}
	var rawPayload string
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT payload_json FROM tasks WHERE id = ?"), task.ID).Scan(&rawPayload); err != nil || json.Unmarshal([]byte(rawPayload), &payload) != nil {
		s.failTask(ctx, task, errors.New("任务载荷格式不正确"), "INVALID_TASK_PAYLOAD")
		return
	}
	payload.RankingType = strings.ToLower(strings.TrimSpace(payload.RankingType))
	payload.Period = strings.ToLower(strings.TrimSpace(payload.Period))
	if payload.RankingType == "hot" {
		payload.RankingType = "playback"
	}
	if payload.RankingType == "top250" && (payload.Period == "" || payload.Period == "daily") {
		payload.Period = "all"
	}
	if !validRankingType(payload.RankingType) || !validRankingPeriod(payload.RankingType, payload.Period) {
		s.failTask(ctx, task, errors.New("榜单类型或周期不合法"), "INVALID_TASK_PAYLOAD")
		return
	}
	_ = s.updateTaskProgress(ctx, task.ID, task.AttemptNumber, 10)
	snapshot, err := s.adapter.Ranking(ctx, payload.RankingType, payload.Period)
	if err != nil {
		s.failTask(ctx, task, err, "RANKING_FETCH_FAILED")
		return
	}
	if len(snapshot.Items) == 0 || len(snapshot.Items) > 250 {
		s.failTask(ctx, task, errors.New("榜单必须包含 1 到 250 部作品"), "INVALID_RANKING_SOURCE")
		return
	}
	if snapshot.RankingType != "" && snapshot.RankingType != payload.RankingType || snapshot.Period != "" && snapshot.Period != payload.Period {
		s.failTask(ctx, task, errors.New("来源返回的榜单类型或周期与请求不一致"), "INVALID_RANKING_SOURCE")
		return
	}
	observedAt := strings.TrimSpace(snapshot.ObservedAt)
	if observedAt == "" {
		observedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	seenCodes := make(map[string]struct{}, len(snapshot.Items))
	seenRanks := make(map[int]struct{}, len(snapshot.Items))
	prepared := make([]struct {
		itemID string
		rank   int
	}, 0, len(snapshot.Items))

	for index, entry := range snapshot.Items {
		if s.taskWasCancelled(ctx, task.ID, task.AttemptNumber) {
			return
		}
		rank := entry.Rank
		if rank == 0 {
			rank = index + 1
		}
		if rank < 1 || rank > 250 {
			s.failTask(ctx, task, fmt.Errorf("榜单第 %d 项排名不合法", index+1), "INVALID_RANKING_SOURCE")
			return
		}
		if _, ok := seenRanks[rank]; ok {
			s.failTask(ctx, task, fmt.Errorf("榜单重复排名 %d", rank), "INVALID_RANKING_SOURCE")
			return
		}
		seenRanks[rank] = struct{}{}
		_ = s.updateTaskProgress(ctx, task.ID, task.AttemptNumber, min(15+index*55/len(snapshot.Items), 75))
		item, err := s.adapter.Fetch(ctx, entry.SourceID, entry.URL)
		if err != nil {
			s.logger.WarnContext(ctx, "抓取榜单作品详情失败，跳过该作品", "rank", rank, "sourceId", entry.SourceID, "error", err)
			continue
		}
		if item.SourceID == "" {
			item.SourceID = entry.SourceID
		}
		if item.URL == "" {
			item.URL = entry.URL
		}
		if item.Code == "" {
			item.Code = entry.Code
		}
		if item.RawCode == "" {
			item.RawCode = item.Code
		}
		if item.Title == "" {
			item.Title = entry.Title
		}
		code, err := NormalizeCode(item.Code)
		if err != nil {
			cleaned := strings.ToUpper(strings.TrimSpace(item.Code))
			if cleaned != "" {
				code = cleaned
			} else {
				s.logger.WarnContext(ctx, "榜单作品缺少番号，跳过", "rank", rank, "sourceId", entry.SourceID)
				continue
			}
		}
		if strings.TrimSpace(item.Title) == "" {
			s.logger.WarnContext(ctx, "榜单作品缺少标题，跳过", "rank", rank, "sourceId", entry.SourceID)
			continue
		}
		if _, ok := seenCodes[code]; ok {
			continue
		}
		seenCodes[code] = struct{}{}
		input := sourceImportItem(s.adapter.Name(), entry, item)
		catalogItem, _, err := s.upsert(ctx, input, "")
		if err != nil {
			s.failTask(ctx, task, fmt.Errorf("保存作品 %s 失败: %w", code, err), "CATALOG_UPDATE_FAILED")
			return
		}
		s.importSourceImages(ctx, catalogItem.ID, item.Images)
		s.seedSources(ctx, catalogItem)

		prepared = append(prepared, struct {
			itemID string
			rank   int
		}{itemID: catalogItem.ID, rank: rank})
	}
	if len(prepared) == 0 {
		s.failTask(ctx, task, errors.New("该榜单未获取到有效作品元数据"), "NO_VALID_ITEMS")
		return
	}
	if s.taskWasCancelled(ctx, task.ID, task.AttemptNumber) {
		return
	}
	_ = s.updateTaskProgress(ctx, task.ID, task.AttemptNumber, 85)
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		s.failTask(ctx, task, err, "RANKING_SAVE_FAILED")
		return
	}
	defer tx.Rollback()
	failRankingWrite := func(writeErr error) {
		_ = tx.Rollback()
		s.failTask(ctx, task, writeErr, "RANKING_SAVE_FAILED")
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE item_rankings SET is_current = 0 WHERE ranking_type = ? AND period = ? AND is_current = 1"), payload.RankingType, payload.Period); err != nil {
		failRankingWrite(err)
		return
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	for _, item := range prepared {
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO item_rankings (item_id, ranking_type, period, rank, observed_at, is_current, created_at) VALUES (?, ?, ?, ?, ?, 1, ?)"), item.itemID, payload.RankingType, payload.Period, item.rank, observedAt, createdAt); err != nil {
			failRankingWrite(err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		failRankingWrite(err)
		return
	}
	_ = s.updateTaskProgress(ctx, task.ID, task.AttemptNumber, 95)
	s.releaseRankingLock(ctx, task.ID)
	_ = s.SyncRankingLibraryItems(ctx, payload.RankingType, payload.Period)
	if err := s.SyncLibraryMemberships(ctx, "", ""); err != nil {
		s.failTask(ctx, task, err, "LIBRARY_MEMBERSHIP_FAILED")
		return
	}
	s.succeedTask(ctx, task)
}

func sourceImportItem(source string, entry metadata.RankingEntry, item metadata.SourceItem) ImportItem {
	if source == "" {
		source = "javdb"
	}
	input := ImportItem{Source: source, SourceID: item.SourceID, Code: item.Code, RawCode: item.RawCode, Title: item.Title, OriginalTitle: item.OriginalTitle, Overview: item.Overview, ReleaseDate: item.ReleaseDate, Year: item.Year, RuntimeSeconds: item.RuntimeSeconds, SourceRating: item.Rating, RatingCount: item.RatingCount, SourceURL: item.URL}
	if input.SourceID == "" {
		input.SourceID = entry.SourceID
	}
	if input.SourceURL == "" {
		input.SourceURL = entry.URL
	}
	if input.Code == "" {
		input.Code = entry.Code
	}
	if input.RawCode == "" {
		input.RawCode = input.Code
	}
	if input.Title == "" {
		input.Title = entry.Title
	}
	for _, relation := range item.People {
		input.People = append(input.People, RelationInput{Name: relation.Name, Role: relation.Role})
	}
	for _, relation := range item.Studios {
		input.Studios = append(input.Studios, RelationInput{Name: relation.Name, Role: relation.Role})
	}
	for _, relation := range item.Series {
		input.Series = append(input.Series, RelationInput{Name: relation.Name, Role: relation.Role})
	}
	for _, relation := range item.Tags {
		input.Tags = append(input.Tags, RelationInput{Name: relation.Name, Role: relation.Role})
	}
	return input
}

func (s *Service) importSourceImages(ctx context.Context, itemID string, images []metadata.SourceImage) {
	if s.imageImporter == nil {
		return
	}
	galleryCount := 0
	imageCount := 0
	for _, image := range images {
		imgType := strings.ToLower(strings.TrimSpace(image.Type))
		if imgType == "still" || imgType == "preview" {
			imgType = "gallery"
		}
		if imgType != "poster" && imgType != "backdrop" && imgType != "gallery" {
			continue
		}
		if imgType == "gallery" {
			if galleryCount >= 12 {
				continue
			}
			galleryCount++
		}
		if err := s.imageImporter(ctx, itemID, imgType, image.URL, image.Label, image.Priority, imageCount); err != nil {
			s.logger.Warn("榜单作品图片导入失败", "itemId", itemID, "imageType", imgType, "error", redactTaskText(err.Error()))
			continue
		}
		imageCount++
	}
}

func validRankingType(value string) bool {
	return value == "ranking" || value == "playback" || value == "hot" || value == "top250"
}

func validRankingPeriod(rankingType, value string) bool {
	_, err := ranking.Period(rankingType, value)
	return err == nil
}

func (s *Service) releaseRankingLock(ctx context.Context, taskID string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("DELETE FROM ranking_generation_locks WHERE task_id = ?"), taskID)
}

func (s *Service) EnqueueRankingTask(ctx context.Context, rankingType, period, requestedBy, requestKey string) (string, bool, error) {
	var err error
	rankingType, err = ranking.Type(rankingType)
	if err != nil || rankingType == "popular" {
		return "", false, errors.New("请选择排行榜、热播或 TOP 250")
	}
	period, err = ranking.Period(rankingType, period)
	if err != nil {
		return "", false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	taskID := newID("task")
	idempotencyKey := "generate_ranking:" + rankingType + ":" + period + ":" + taskID
	if requestKey != "" {
		idempotencyKey = "generate_ranking:" + rankingType + ":" + period + ":" + requestKey
	}
	payload, _ := json.Marshal(map[string]string{"rankingType": rankingType, "period": period, "requestedBy": requestedBy})

	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()

	if requestKey != "" {
		var existingID, status string
		err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id, status FROM tasks WHERE idempotency_key = ?"), idempotencyKey).Scan(&existingID, &status)
		if err == nil {
			return existingID, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", false, err
		}
	}

	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO tasks (id, type, idempotency_key, status, payload_json, created_at, updated_at) VALUES (?, 'generate_ranking', ?, 'queued', ?, ?, ?)"), taskID, idempotencyKey, string(payload), now, now); err != nil {
		return "", false, err
	}

	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO ranking_generation_locks (ranking_type, period, task_id, created_at) VALUES (?, ?, ?, ?)"), rankingType, period, taskID, now); err != nil {
		_ = tx.Rollback()
		var existingID, existingStatus string
		if loadErr := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT l.task_id, t.status FROM ranking_generation_locks l JOIN tasks t ON t.id = l.task_id WHERE l.ranking_type = ? AND l.period = ?"), rankingType, period).Scan(&existingID, &existingStatus); loadErr == nil {
			return existingID, true, nil
		}
		return "", false, errors.New("该榜单周期已有任务运行")
	}

	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return taskID, false, nil
}

func (s *Service) generateRanking(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input struct {
		ModuleID    string `json:"moduleId"`
		RankingType string `json:"rankingType"`
		Period      string `json:"period"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_REQUEST", "请求参数无效")
		return
	}
	var err error
	if s.modules != nil {
		if input.ModuleID == "" {
			writeError(w, r, 400, "MODULE_REQUIRED", "请先选择来源模块")
			return
		}
		taskID, reused, err := s.EnqueueModuleRankingTask(r.Context(), input.ModuleID, input.RankingType, input.Period, principal.User.ID, strings.TrimSpace(r.Header.Get("Idempotency-Key")))
		if err != nil {
			writeError(w, r, 400, "RANKING_CREATE_FAILED", err.Error())
			return
		}
		writeJSON(w, 202, map[string]any{"taskId": taskID, "reused": reused, "status": "queued"})
		return
	}
	input.RankingType, err = ranking.Type(input.RankingType)
	if err == nil {
		input.Period, err = ranking.Period(input.RankingType, input.Period)
	}
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_RANKING_REQUEST", err.Error())
		return
	}
	requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(requestKey) > 180 {
		writeError(w, r, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key 不能超过 180 个字符")
		return
	}
	taskID, reused, err := s.EnqueueRankingTask(r.Context(), input.RankingType, input.Period, principal.User.ID, requestKey)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "RANKING_TASK_CREATE_FAILED", err.Error())
		return
	}
	s.audit(r.Context(), principal.User.ID, "generate_ranking", "ranking", input.RankingType+":"+input.Period, middleware.GetReqID(r.Context()), "{}")
	status := "queued"
	if reused {
		_ = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT status FROM tasks WHERE id = ?"), taskID).Scan(&status)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": taskID, "status": status, "rankingType": input.RankingType, "period": input.Period, "reused": reused})
}

// Initial content is administrator initiated; no source/account assumptions.
func (s *Service) EnsureInitialRankings(ctx context.Context) {}
func (s *Service) TriggerAllRankingsUpdate(ctx context.Context, requestedBy string) ([]string, error) {
	ids := []string{}
	if s.modules == nil {
		return ids, nil
	}
	for _, entry := range s.modules.List() {
		if entry.Status != "active" || entry.MetadataInstance == nil {
			continue
		}
		for _, def := range entry.Manifest.RankingDefinitions() {
			for _, period := range def.Periods {
				if id, _, err := s.EnqueueModuleRankingTask(ctx, entry.Manifest.ID, def.ID, period.Value, requestedBy, ""); err == nil {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids, nil
}
func (s *Service) checkRankingSchedule(ctx context.Context) { s.checkModuleRankingSchedules(ctx) }

func (s *Service) taskWasCancelled(ctx context.Context, taskID string, attemptNumber int) bool {
	var cancelled bool
	query := `SELECT CASE WHEN t.status <> 'running' OR a.status <> 'running' THEN 1 ELSE 0 END
		FROM tasks t JOIN task_attempts a ON a.task_id = t.id AND a.attempt_number = ?
		WHERE t.id = ?`
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), attemptNumber, taskID).Scan(&cancelled); err != nil {
		return true
	}
	return cancelled
}

func (s *Service) updateTaskProgress(ctx context.Context, taskID string, attemptNumber, progress int) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 99 {
		progress = 99
	}
	_, err := s.db.DB().ExecContext(ctx, s.db.Rebind(`UPDATE tasks SET progress = ?, updated_at = ?
		WHERE id = ? AND status = 'running' AND EXISTS (
			SELECT 1 FROM task_attempts WHERE task_id = ? AND attempt_number = ? AND status = 'running'
		)`), progress, time.Now().UTC().Format(time.RFC3339Nano), taskID, taskID, attemptNumber)
	return err
}

func (s *Service) succeedTask(ctx context.Context, task taskRecord) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.DB().ExecContext(ctx, s.db.Rebind(`UPDATE tasks SET status = 'succeeded', progress = 100, finished_at = ?, updated_at = ?
		WHERE id = ? AND status = 'running' AND EXISTS (
			SELECT 1 FROM task_attempts WHERE task_id = ? AND attempt_number = ? AND status = 'running'
		)`), now, now, task.ID, task.ID, task.AttemptNumber)
	if err != nil {
		return
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE task_attempts SET status = 'succeeded', finished_at = ? WHERE task_id = ? AND attempt_number = ? AND status = 'running'"), now, task.ID, task.AttemptNumber)
}

func (s *Service) failTask(ctx context.Context, task taskRecord, taskErr error, code string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	message := redactTaskText(taskErr.Error())
	var status string
	if task.RetryCount+1 < task.MaxRetries {
		status = "queued"
	} else {
		status = "failed"
	}
	result, err := s.db.DB().ExecContext(ctx, s.db.Rebind(`UPDATE tasks SET status = ?, retry_count = retry_count + 1, progress = 0, error_code = ?, error_message = ?, finished_at = CASE WHEN ? = 'failed' THEN ? ELSE NULL END, updated_at = ?
		WHERE id = ? AND status = 'running' AND EXISTS (
			SELECT 1 FROM task_attempts WHERE task_id = ? AND attempt_number = ? AND status = 'running'
		)`), status, code, message, status, now, now, task.ID, task.ID, task.AttemptNumber)
	if err != nil {
		return
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return
	}
	attemptStatus := "failed"
	if status == "queued" {
		attemptStatus = "retrying"
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE task_attempts SET status = ?, error_code = ?, error_message = ?, finished_at = ? WHERE task_id = ? AND attempt_number = ? AND status = 'running'"), attemptStatus, code, message, now, task.ID, task.AttemptNumber)
	if task.Type == "generate_ranking" && status == "failed" {
		s.releaseRankingLock(ctx, task.ID)
	}
}

func (s *Service) listTasks(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !validTaskStatus(status) {
		writeError(w, r, http.StatusBadRequest, "INVALID_TASK_STATUS", "任务状态不合法")
		return
	}
	taskType := strings.TrimSpace(r.URL.Query().Get("type"))
	if len([]rune(taskType)) > 120 {
		writeError(w, r, http.StatusBadRequest, "INVALID_TASK_TYPE", "任务类型过长")
		return
	}
	limit := parseTaskLimit(r.URL.Query().Get("limit"), 50, 100)
	offset := parseTaskOffset(r.URL.Query().Get("offset"))
	where := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if taskType != "" {
		where = append(where, "type = ?")
		args = append(args, taskType)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM tasks"+whereSQL), args...).Scan(&total); err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASKS_READ_FAILED", "读取任务失败")
		return
	}
	query := "SELECT id, type, status, progress, retry_count, max_retries, COALESCE(error_code, ''), COALESCE(error_message, ''), started_at, finished_at, created_at, updated_at FROM tasks" + whereSQL + " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind(query), args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASKS_READ_FAILED", "读取任务失败")
		return
	}
	defer rows.Close()
	tasks := make([]taskRecord, 0)
	for rows.Next() {
		var task taskRecord
		var started, finished sql.NullString
		if err := rows.Scan(&task.ID, &task.Type, &task.Status, &task.Progress, &task.RetryCount, &task.MaxRetries, &task.ErrorCode, &task.ErrorMessage, &started, &finished, &task.CreatedAt, &task.UpdatedAt); err != nil {
			writeError(w, r, http.StatusInternalServerError, "TASKS_READ_FAILED", "读取任务失败")
			return
		}
		if started.Valid {
			task.StartedAt = &started.String
		}
		if finished.Valid {
			task.FinishedAt = &finished.String
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASKS_READ_FAILED", "读取任务失败")
		return
	}
	for index := range tasks {
		tasks[index] = decorateTask(tasks[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tasks, "total": total, "limit": limit, "offset": offset})
}

func (s *Service) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.loadTask(r.Context(), chi.URLParam(r, "taskID"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "任务不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_READ_FAILED", "读取任务失败")
		return
	}
	task, err = s.withTaskAttempts(r.Context(), task)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_READ_FAILED", "读取任务尝试记录失败")
		return
	}
	writeJSON(w, http.StatusOK, decorateTask(task))
}

func (s *Service) loadTask(ctx context.Context, taskID string) (taskRecord, error) {
	var task taskRecord
	var started, finished sql.NullString
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, type, status, progress, retry_count, max_retries, COALESCE(error_code, ''), COALESCE(error_message, ''), started_at, finished_at, created_at, updated_at FROM tasks WHERE id = ?"), taskID).Scan(&task.ID, &task.Type, &task.Status, &task.Progress, &task.RetryCount, &task.MaxRetries, &task.ErrorCode, &task.ErrorMessage, &started, &finished, &task.CreatedAt, &task.UpdatedAt)
	task.ErrorMessage = redactTaskText(task.ErrorMessage)
	task = decorateTask(task)
	if started.Valid {
		task.StartedAt = &started.String
	}
	if finished.Valid {
		task.FinishedAt = &finished.String
	}
	return task, err
}

func (s *Service) withTaskAttempts(ctx context.Context, task taskRecord) (taskRecord, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, attempt_number, status, COALESCE(error_code, ''), COALESCE(error_message, ''), started_at, finished_at FROM task_attempts WHERE task_id = ? ORDER BY attempt_number"), task.ID)
	if err != nil {
		return taskRecord{}, err
	}
	attempts := make([]taskAttemptRecord, 0)
	for rows.Next() {
		var attempt taskAttemptRecord
		var finished sql.NullString
		if err := rows.Scan(&attempt.ID, &attempt.AttemptNumber, &attempt.Status, &attempt.ErrorCode, &attempt.ErrorMessage, &attempt.StartedAt, &finished); err != nil {
			_ = rows.Close()
			return taskRecord{}, err
		}
		attempt.ErrorMessage = redactTaskText(attempt.ErrorMessage)
		if finished.Valid {
			attempt.FinishedAt = &finished.String
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return taskRecord{}, err
	}
	if err := rows.Close(); err != nil {
		return taskRecord{}, err
	}
	task.Attempts = attempts
	return task, nil
}

func (s *Service) retryTask(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	taskID := strings.TrimSpace(chi.URLParam(r, "taskID"))
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_RETRY_FAILED", "重试任务失败")
		return
	}
	defer tx.Rollback()
	var status, taskType, rawPayload string
	if err := tx.QueryRowContext(r.Context(), s.db.Rebind("SELECT status, type, payload_json FROM tasks WHERE id = ?"), taskID).Scan(&status, &taskType, &rawPayload); errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "任务不存在")
		return
	} else if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_RETRY_FAILED", "读取任务失败")
		return
	}
	if status != "failed" && status != "partial" && status != "cancelled" {
		writeError(w, r, http.StatusConflict, "TASK_NOT_RETRYABLE", "任务当前不能重试")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(r.Context(), s.db.Rebind("UPDATE tasks SET status = 'queued', progress = 0, retry_count = 0, error_code = NULL, error_message = NULL, started_at = NULL, finished_at = NULL, updated_at = ? WHERE id = ? AND status IN ('failed', 'partial', 'cancelled')"), now, taskID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_RETRY_FAILED", "重试任务失败")
		return
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		writeError(w, r, http.StatusConflict, "TASK_NOT_RETRYABLE", "任务当前不能重试")
		return
	}

	if taskType == "generate_ranking" {
		var payload moduleRankingPayload
		if json.Unmarshal([]byte(rawPayload), &payload) != nil {
			writeError(w, r, 409, "INVALID_TASK_PAYLOAD", "榜单任务内容无效")
			return
		}
		lockType := payload.RankingType
		if payload.ModuleID != "" {
			entry, err := s.metadataModule(payload.ModuleID)
			if err != nil {
				writeError(w, r, 409, "MODULE_UNAVAILABLE", "来源模块不可用")
				return
			}
			if _, ok := entry.Manifest.Ranking(payload.RankingType, payload.Period); !ok {
				writeError(w, r, 409, "INVALID_TASK_PAYLOAD", "模块已不再提供该榜单")
				return
			}
			lockType = payload.ModuleID + ":" + payload.RankingType
		} else if !validRankingType(payload.RankingType) || !validRankingPeriod(payload.RankingType, payload.Period) {
			writeError(w, r, 409, "INVALID_TASK_PAYLOAD", "榜单任务内容无效")
			return
		}
		if _, err := tx.ExecContext(r.Context(), s.db.Rebind("INSERT INTO ranking_generation_locks(ranking_type,period,task_id,created_at)VALUES(?,?,?,?)"), lockType, payload.Period, taskID, now); err != nil {
			writeError(w, r, 409, "RANKING_TASK_RUNNING", "该榜单已有任务运行")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_RETRY_FAILED", "重试任务失败")
		return
	}
	s.audit(r.Context(), principal.User.ID, "retry_task", "task", taskID, middleware.GetReqID(r.Context()), "{}")
	task, err := s.loadTask(r.Context(), taskID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_READ_FAILED", "读取重试任务失败")
		return
	}
	task, err = s.withTaskAttempts(r.Context(), task)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "TASK_READ_FAILED", "读取重试任务尝试记录失败")
		return
	}
	writeJSON(w, http.StatusAccepted, decorateTask(task))
}

func decorateTask(task taskRecord) taskRecord {
	task.ErrorMessage = redactTaskText(task.ErrorMessage)
	task.CanRetry = task.Status == "failed" || task.Status == "partial" || task.Status == "cancelled"
	task.CanCancel = task.Status == "queued" || task.Status == "running"
	return task
}

func validTaskStatus(status string) bool {
	switch status {
	case "queued", "running", "succeeded", "partial", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func parseTaskLimit(raw string, fallback, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func parseTaskOffset(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	if value > 100000 {
		return 100000
	}
	return value
}

var taskURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
var taskHeaderPattern = regexp.MustCompile(`(?im)(authorization|proxy-authorization|cookie)\s*[:=]\s*[^\r\n]+`)
var taskSensitivePattern = regexp.MustCompile(`(?i)(token|password|passwd|secret|api[_-]?key)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;]+)`)

func redactTaskText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = taskURLPattern.ReplaceAllString(value, "[上游地址已隐藏]")
	value = taskHeaderPattern.ReplaceAllString(value, "$1=[已隐藏]")
	value = taskSensitivePattern.ReplaceAllString(value, "$1=[已隐藏]")
	for _, key := range []string{"authorization", "proxy-authorization", "cookie", "token", "password", "passwd", "secret", "api_key", "apikey"} {
		lower := strings.ToLower(value)
		for {
			index := strings.Index(lower, key+"=")
			if index < 0 {
				break
			}
			end := strings.IndexAny(value[index+len(key)+1:], "& ,;\r\n")
			if end < 0 {
				end = len(value) - index - len(key) - 1
			}
			end += index + len(key) + 1
			value = value[:index+len(key)+1] + "[已隐藏]" + value[end:]
			lower = strings.ToLower(value)
		}
	}
	if len([]rune(value)) > 1000 {
		value = string([]rune(value)[:1000])
	}
	return value
}

func (s *Service) cancelTask(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	taskID := chi.URLParam(r, "taskID")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE tasks SET status = 'cancelled', finished_at = ?, updated_at = ? WHERE id = ? AND status IN ('queued', 'running')"), now, now, taskID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "TASK_CANCEL_FAILED", "取消任务失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		task, loadErr := s.loadTask(r.Context(), taskID)
		if errors.Is(loadErr, sql.ErrNoRows) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "任务不存在")
			return
		}
		if loadErr == nil && task.Status == "cancelled" {
			writeJSON(w, http.StatusOK, decorateTask(task))
			return
		}
		writeError(w, r, http.StatusConflict, "TASK_NOT_CANCELLABLE", "任务当前不能取消")
		return
	}
	_, _ = s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE task_attempts SET status = 'cancelled', finished_at = ? WHERE task_id = ? AND status = 'running'"), now, taskID)
	var taskType string
	if s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT type FROM tasks WHERE id = ?"), taskID).Scan(&taskType) == nil && taskType == "generate_ranking" {
		s.releaseRankingLock(r.Context(), taskID)
	}
	s.audit(r.Context(), principal.User.ID, "cancel_task", "task", taskID, middleware.GetReqID(r.Context()), "{}")
	task, _ := s.loadTask(r.Context(), taskID)
	writeJSON(w, http.StatusOK, decorateTask(task))
}
