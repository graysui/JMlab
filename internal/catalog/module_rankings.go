package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/module"
	"net/http"
	"strings"
	"time"
)

type moduleRankingPayload struct {
	ModuleID    string `json:"moduleId"`
	RankingType string `json:"rankingType"`
	Period      string `json:"period"`
}
type rankingTarget struct {
	RankingType string `json:"rankingType"`
	Period      string `json:"period"`
}
type moduleRankingSchedule struct {
	UpdateTime    string          `json:"updateTime"`
	Enabled       bool            `json:"enabled"`
	IntervalHours int             `json:"intervalHours"`
	Targets       []rankingTarget `json:"targets"`
	LastRunAt     *string         `json:"lastRunAt,omitempty"`
}

func (s *Service) registerModuleRankingRoutes(router chi.Router) {
	read := router.With(s.authService.RequireAuth)
	read.Get("/rankings/modules", s.rankingSources)
	read.Get("/rankings/modules/{moduleID}/{rankingType}", s.readModuleRanking)
	admin := router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin"))
	admin.Get("/admin/modules/{moduleID}/rankings", s.moduleRankings)
	admin.With(s.authService.RequireCSRF).Post("/admin/modules/{moduleID}/rankings", s.generateModuleRanking)
	admin.With(s.authService.RequireCSRF).Put("/admin/modules/{moduleID}/rankings/schedule", s.saveModuleRankingSchedule)
}

func (s *Service) rankingSources(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	if s.modules != nil {
		for _, entry := range s.modules.List() {
			if entry.Status == module.StatusActive && entry.MetadataInstance != nil && len(entry.Manifest.RankingDefinitions()) > 0 {
				items = append(items, map[string]any{"id": entry.Manifest.ID, "name": entry.Manifest.Name, "rankings": entry.Manifest.RankingDefinitions()})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Service) readModuleRanking(w http.ResponseWriter, r *http.Request) {
	id, kind, period := chi.URLParam(r, "moduleID"), chi.URLParam(r, "rankingType"), r.URL.Query().Get("period")
	entry, err := s.metadataModule(id)
	if err != nil {
		writeError(w, r, 404, "MODULE_UNAVAILABLE", "榜单来源不可用")
		return
	}
	if _, ok := entry.Manifest.Ranking(kind, period); !ok {
		writeError(w, r, 400, "INVALID_RANKING", "请选择来源提供的榜单范围")
		return
	}
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT ir.item_id,ir.rank FROM item_rankings ir JOIN media_items mi ON mi.id=ir.item_id WHERE mi.metadata_module_id=? AND ir.ranking_type=? AND ir.period=? AND ir.is_current=1 ORDER BY ir.rank LIMIT 250"), id, kind, period)
	if err != nil {
		writeError(w, r, 500, "RANKING_READ_FAILED", "读取榜单失败")
		return
	}
	type rankedID struct {
		id   string
		rank int
	}
	ids := []rankedID{}
	for rows.Next() {
		var item rankedID
		if rows.Scan(&item.id, &item.rank) != nil {
			rows.Close()
			writeError(w, r, 500, "RANKING_READ_FAILED", "读取榜单失败")
			return
		}
		ids = append(ids, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, r, 500, "RANKING_READ_FAILED", "读取榜单失败")
		return
	}
	rows.Close()
	principal, _ := auth.PrincipalFromContext(r.Context())
	items := []map[string]any{}
	for _, row := range ids {
		if item, err := s.GetItemForUser(r.Context(), row.id, principal.User); err == nil {
			items = append(items, map[string]any{"rank": row.rank, "item": item})
		}
	}
	writeJSON(w, 200, map[string]any{"moduleId": id, "rankingType": kind, "period": period, "items": items})
}

func (s *Service) moduleRankings(w http.ResponseWriter, r *http.Request) {
	if s.modules == nil {
		writeError(w, r, 404, "MODULE_NOT_FOUND", "未配置模块")
		return
	}
	entry, ok := s.modules.Get(chi.URLParam(r, "moduleID"))
	if !ok {
		writeError(w, r, 404, "MODULE_NOT_FOUND", "模块不存在")
		return
	}
	schedule, err := s.moduleRankingSchedule(r.Context(), entry.Manifest.ID)
	if err != nil {
		writeError(w, r, 500, "SCHEDULE_READ_FAILED", "无法读取计划")
		return
	}
	writeJSON(w, 200, map[string]any{"items": entry.Manifest.RankingDefinitions(), "revision": entry.Revision, "schedule": schedule})
}

func (s *Service) generateModuleRanking(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RankingType string `json:"rankingType"`
		Period      string `json:"period"`
		Revision    string `json:"revision"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, r, 400, "INVALID_RANKING_REQUEST", "榜单参数无效")
		return
	}
	id := chi.URLParam(r, "moduleID")
	entry, err := s.metadataModule(id)
	if err != nil {
		writeError(w, r, 409, "MODULE_UNAVAILABLE", "模块未启用或不可用")
		return
	}
	if input.Revision == "" || entry.Revision != input.Revision {
		writeError(w, r, 409, "MODULE_CHANGED", "模块已更新，请刷新后重试")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 180 {
		writeError(w, r, 400, "INVALID_IDEMPOTENCY_KEY", "请求标识过长")
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	taskID, reused, err := s.EnqueueModuleRankingTask(r.Context(), id, input.RankingType, input.Period, principal.User.ID, key)
	if err != nil {
		writeError(w, r, 400, "RANKING_CREATE_FAILED", err.Error())
		return
	}
	s.audit(r.Context(), principal.User.ID, "generate_module_ranking", "module", id, "", "{}")
	writeJSON(w, 202, map[string]any{"taskId": taskID, "status": "queued", "reused": reused})
}

func (s *Service) EnqueueModuleRankingTask(ctx context.Context, id, kind, period, requestedBy, key string) (string, bool, error) {
	entry, err := s.metadataModule(id)
	if err != nil {
		return "", false, err
	}
	if entry.Capabilities != nil && !entry.Capabilities.SupportsRankings {
		return "", false, errors.New("模块不支持榜单")
	}
	if _, ok := entry.Manifest.Ranking(kind, period); !ok {
		return "", false, errors.New("请选择模块提供的榜单和范围")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	taskID := newID("task")
	idempotency := "module_ranking:" + id + ":" + kind + ":" + period + ":" + taskID
	if key != "" {
		idempotency = "module_ranking:" + id + ":" + kind + ":" + period + ":" + key
	}
	payload, _ := json.Marshal(moduleRankingPayload{ModuleID: id, RankingType: kind, Period: period})
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if key != "" {
		var existing string
		err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM tasks WHERE idempotency_key=?"), idempotency).Scan(&existing)
		if err == nil {
			return existing, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", false, err
		}
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO tasks(id,type,idempotency_key,status,payload_json,created_at,updated_at)VALUES(?,'generate_ranking',?,'queued',?,?,?)"), taskID, idempotency, string(payload), now, now); err != nil {
		return "", false, err
	}
	lockType := id + ":" + kind
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO ranking_generation_locks(ranking_type,period,task_id,created_at)VALUES(?,?,?,?)"), lockType, period, taskID, now); err != nil {
		_ = tx.Rollback()
		var existing string
		if loadErr := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT task_id FROM ranking_generation_locks WHERE ranking_type=? AND period=?"), lockType, period).Scan(&existing); loadErr == nil {
			return existing, true, nil
		}
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return taskID, false, nil
}

func (s *Service) processModuleRankingTask(ctx context.Context, task taskRecord, payload moduleRankingPayload) {
	entry, err := s.metadataModule(payload.ModuleID)
	if err != nil {
		s.failTask(ctx, task, err, "MODULE_UNAVAILABLE")
		return
	}
	def, ok := entry.Manifest.Ranking(payload.RankingType, payload.Period)
	if !ok {
		s.failTask(ctx, task, errors.New("模块已不再提供此榜单"), "INVALID_RANKING")
		return
	}
	maxPages := max(1, def.MaxPages)
	summaries := []module.MediaSummary{}
	seen := map[string]bool{}
	observedAt := ""
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for page := 1; page <= maxPages; page++ {
		if s.taskWasCancelled(ctx, task.ID, task.AttemptNumber) {
			return
		}
		snapshot, err := entry.MetadataInstance.GetRankings(callCtx, payload.RankingType, payload.Period, page)
		if err != nil {
			s.failTask(ctx, task, errors.New("模块榜单获取失败"), "RANKING_FETCH_FAILED")
			return
		}
		if snapshot.RankingType != "" && snapshot.RankingType != payload.RankingType || snapshot.Period != "" && snapshot.Period != payload.Period || len(snapshot.Items) > 250 {
			s.failTask(ctx, task, errors.New("模块返回的榜单不匹配或过大"), "INVALID_RANKING_SOURCE")
			return
		}
		if observedAt == "" {
			observedAt = snapshot.ObservedAt
		}
		if len(snapshot.Items) == 0 {
			break
		}
		added := 0
		for _, item := range snapshot.Items {
			if item.SourceID == "" || seen[item.SourceID] {
				continue
			}
			seen[item.SourceID] = true
			summaries = append(summaries, item)
			added++
			if len(summaries) >= 250 {
				break
			}
		}
		if added == 0 || len(summaries) >= 250 {
			break
		}
	}
	if len(summaries) == 0 {
		s.failTask(ctx, task, errors.New("榜单没有有效内容，保留上次快照"), "NO_VALID_ITEMS")
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, observedAt); err != nil {
		observedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	type ranked struct {
		id   string
		rank int
	}
	prepared := []ranked{}
	ranks := map[int]bool{}
	for i, summary := range summaries {
		if s.taskWasCancelled(ctx, task.ID, task.AttemptNumber) {
			return
		}
		detail, err := entry.MetadataInstance.GetDetail(callCtx, summary.SourceID, summary.ProviderIDs)
		if err != nil {
			s.failTask(ctx, task, errors.New("榜单详情获取失败，保留上次快照"), "SOURCE_FETCH_FAILED")
			return
		}
		current, ok := s.modules.Get(payload.ModuleID)
		if !ok || current.Status != module.StatusActive || current.Revision != entry.Revision {
			s.failTask(ctx, task, module.ErrRevisionChanged, "MODULE_CHANGED")
			return
		}
		if detail.SourceID != summary.SourceID || !containsItemType(entry.SupportedTypes(), detail.ItemType) {
			s.failTask(ctx, task, errors.New("榜单内容身份或类型不匹配"), "INVALID_RANKING_SOURCE")
			return
		}
		item, _, err := s.saveModuleMedia(ctx, payload.ModuleID, detail, "", nil)
		if err != nil {
			s.failTask(ctx, task, err, "CATALOG_UPDATE_FAILED")
			return
		}
		rank := summary.Rank
		if rank == 0 {
			rank = i + 1
		}
		if rank < 1 || rank > 250 || ranks[rank] {
			s.failTask(ctx, task, errors.New("榜单排名重复或超出范围"), "INVALID_RANKING_SOURCE")
			return
		}
		ranks[rank] = true
		prepared = append(prepared, ranked{item.ID, rank})
		s.seedSources(ctx, item)
		_ = s.updateTaskProgress(ctx, task.ID, task.AttemptNumber, 10+(i+1)*75/len(summaries))
	}
	if s.taskWasCancelled(ctx, task.ID, task.AttemptNumber) {
		return
	}
	err = s.modules.WithActiveRevision(ctx, payload.ModuleID, entry.Revision, func() error {
		tx, err := s.db.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE item_rankings SET is_current=0 WHERE ranking_type=? AND period=? AND is_current=1 AND item_id IN(SELECT id FROM media_items WHERE metadata_module_id=?)"), payload.RankingType, payload.Period, payload.ModuleID); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, item := range prepared {
			if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO item_rankings(item_id,ranking_type,period,rank,observed_at,is_current,created_at)VALUES(?,?,?,?,?,1,?) ON CONFLICT(item_id,ranking_type,period,observed_at)DO UPDATE SET rank=excluded.rank,is_current=1"), item.id, payload.RankingType, payload.Period, item.rank, observedAt, now); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		s.failTask(ctx, task, err, "RANKING_SAVE_FAILED")
		return
	}
	if err := s.SyncLibraryMemberships(ctx, "", ""); err != nil {
		s.failTask(ctx, task, err, "LIBRARY_MEMBERSHIP_FAILED")
		return
	}
	s.releaseRankingLock(ctx, task.ID)
	s.succeedTask(ctx, task)
}

func containsItemType(types []module.ItemType, kind module.ItemType) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if t == kind {
			return true
		}
	}
	return false
}

func (s *Service) moduleRankingSchedule(ctx context.Context, id string) (moduleRankingSchedule, error) {
	cfg := moduleRankingSchedule{IntervalHours: 24, Targets: []rankingTarget{}}
	var enabled int
	var raw string
	var last sql.NullString
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT enabled,interval_hours,targets_json,last_run_at,update_time FROM module_ranking_schedules WHERE module_id=?"), id).Scan(&enabled, &cfg.IntervalHours, &raw, &last, &cfg.UpdateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal([]byte(raw), &cfg.Targets); err != nil {
		return cfg, err
	}
	cfg.Enabled = enabled == 1
	if last.Valid {
		cfg.LastRunAt = &last.String
	}
	return cfg, nil
}
func (s *Service) saveModuleRankingSchedule(w http.ResponseWriter, r *http.Request) {
	var cfg moduleRankingSchedule
	if decodeJSON(r, &cfg) != nil || !module.ValidRankingUpdateTime(cfg.UpdateTime) || cfg.IntervalHours < 1 || cfg.IntervalHours > 720 || len(cfg.Targets) > 128 || cfg.Enabled && len(cfg.Targets) == 0 {
		writeError(w, r, 400, "INVALID_SCHEDULE", "请选择榜单并设置 1 至 720 小时的间隔")
		return
	}
	id := chi.URLParam(r, "moduleID")
	if s.modules == nil {
		writeError(w, r, 404, "MODULE_NOT_FOUND", "模块不存在")
		return
	}
	entry, ok := s.modules.Get(id)
	if !ok {
		writeError(w, r, 404, "MODULE_NOT_FOUND", "模块不存在")
		return
	}
	seen := map[string]bool{}
	for _, target := range cfg.Targets {
		key := target.RankingType + ":" + target.Period
		if _, ok := entry.Manifest.Ranking(target.RankingType, target.Period); !ok || seen[key] {
			writeError(w, r, 400, "INVALID_SCHEDULE", "榜单范围无效或重复")
			return
		}
		seen[key] = true
	}
	raw, _ := json.Marshal(cfg.Targets)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO module_ranking_schedules(module_id,enabled,interval_hours,targets_json,updated_at,update_time)VALUES(?,?,?,?,?,?) ON CONFLICT(module_id)DO UPDATE SET enabled=excluded.enabled,interval_hours=excluded.interval_hours,targets_json=excluded.targets_json,updated_at=excluded.updated_at,update_time=excluded.update_time"), id, boolInt(cfg.Enabled), cfg.IntervalHours, string(raw), now, cfg.UpdateTime)
	if err != nil {
		writeError(w, r, 500, "SCHEDULE_SAVE_FAILED", "保存自动更新失败")
		return
	}
	stored, _ := s.moduleRankingSchedule(r.Context(), id)
	cfg.LastRunAt = stored.LastRunAt
	writeJSON(w, 200, cfg)
}

func (s *Service) checkModuleRankingSchedules(ctx context.Context) {
	if s.modules == nil {
		return
	}
	for _, entry := range s.modules.List() {
		if entry.Status != module.StatusActive || entry.MetadataInstance == nil {
			continue
		}
		cfg, err := s.moduleRankingSchedule(ctx, entry.Manifest.ID)
		if err != nil || !cfg.Enabled {
			continue
		}
		if !moduleRankingScheduleDue(cfg, time.Now().Local()) {
			continue
		}

		complete := true
		for _, target := range cfg.Targets {
			if _, _, err := s.EnqueueModuleRankingTask(ctx, entry.Manifest.ID, target.RankingType, target.Period, "schedule", ""); err != nil {
				complete = false
			}
		}
		if complete {
			_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE module_ranking_schedules SET last_run_at=?,updated_at=? WHERE module_id=?"), time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), entry.Manifest.ID)
		}
	}
}

func moduleRankingScheduleDue(cfg moduleRankingSchedule, now time.Time) bool {
	var last time.Time
	if cfg.LastRunAt != nil {
		last, _ = time.Parse(time.RFC3339Nano, *cfg.LastRunAt)
	}
	if cfg.UpdateTime != "" {
		clock, err := time.Parse("15:04", cfg.UpdateTime)
		if err != nil {
			return false
		}
		target := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
		return !now.Before(target) && last.Before(target)
	}
	return last.IsZero() || now.Sub(last) >= time.Duration(cfg.IntervalHours)*time.Hour
}
