package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/graysui/JMlab/internal/module"
)

type PlaybackLibrary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Selectable bool   `json:"selectable"`
	Reason     string `json:"reason,omitempty"`
}

func playbackLibraryReason(raw json.RawMessage) string {
	_, _, expr, err := parseLibraryFilter(raw)
	if err != nil {
		return "媒体库规则无效，请先修改规则"
	}
	if expressionHasField(expr, map[string]bool{"hasPlayableSource": true, "streamModuleId": true}) {
		return "此库按播放源筛选，请选择内容库，避免循环依赖"
	}
	if expressionHasField(expr, map[string]bool{"isFavorite": true, "isWatched": true}) {
		return "此库随用户状态变化，请选择普通内容库"
	}
	return ""
}

func (s *Service) PlaybackLibraries(ctx context.Context) ([]PlaybackLibrary, error) {
	rows, err := s.db.DB().QueryContext(ctx, "SELECT id,name,enabled,filter_json FROM virtual_libraries ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PlaybackLibrary{}
	for rows.Next() {
		var item PlaybackLibrary
		var enabled int
		var raw string
		if err := rows.Scan(&item.ID, &item.Name, &enabled, &raw); err != nil {
			return nil, err
		}
		item.Enabled = enabled == 1
		item.Reason = playbackLibraryReason(json.RawMessage(raw))
		item.Selectable = item.Reason == ""
		items = append(items, item)
	}
	return items, rows.Err()
}

// Scope validation and library filter edits share this lock, so a selected
// content library cannot become a source-dependent or personal rule mid-save.
func (s *Service) WithPlaybackLibraryScope(ctx context.Context, scope *module.LibraryScope, save func() error) error {
	s.libraryPolicyMu.Lock()
	defer s.libraryPolicyMu.Unlock()
	if scope != nil && scope.Mode == "selected" {
		items, err := s.PlaybackLibraries(ctx)
		if err != nil {
			return errors.New("读取媒体库失败，请重试")
		}
		choices := map[string]PlaybackLibrary{}
		for _, item := range items {
			choices[item.ID] = item
		}
		for _, id := range scope.LibraryIDs {
			item, ok := choices[id]
			if !ok {
				return errors.New("所选媒体库已删除，请重新选择")
			}
			if !item.Selectable {
				return errors.New(item.Reason)
			}
		}
	}
	return save()
}

func (s *Service) checkPlaybackLibraryChange(id string, filter json.RawMessage) error {
	if playbackLibraryReason(filter) == "" || s.modules == nil {
		return nil
	}
	for _, entry := range s.modules.List() {
		if entry.LibraryScope == nil || entry.LibraryScope.Mode != "selected" {
			continue
		}
		for _, selected := range entry.LibraryScope.LibraryIDs {
			if selected == id {
				return errors.New("此库用于播放模块的适用范围，请先在模块设置中移除，再改为播放源或个人状态规则")
			}
		}
	}
	return nil
}

// PlaybackScopeSQL uses materialized, non-personal content membership. Library
// visibility/play permissions are checked separately for the requesting user.
func PlaybackScopeSQL(scope *module.LibraryScope, itemAlias string) (string, []any) {
	if scope == nil || scope.Mode == "all" {
		return "1=1", nil
	}
	if scope.Mode != "selected" || len(scope.LibraryIDs) == 0 {
		return "1=0", nil
	}
	marks := make([]string, len(scope.LibraryIDs))
	args := make([]any, len(scope.LibraryIDs))
	for i, id := range scope.LibraryIDs {
		marks[i] = "?"
		args[i] = id
	}
	return "EXISTS (SELECT 1 FROM library_items scope_member JOIN virtual_libraries scope_lib ON scope_lib.id=scope_member.library_id WHERE scope_member.item_id=" + itemAlias + ".id AND scope_lib.enabled=1 AND scope_lib.id IN (" + strings.Join(marks, ",") + "))", args
}
