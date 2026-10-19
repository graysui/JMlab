package playback

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/module"
	provider "github.com/graysui/JMlab/internal/stream"
)

const builtinStreamModuleID = "reference-stream-builtin"

func (s *Service) describeSource(source sourceRecord) sourceRecord {
	source.ModuleName = source.ModuleID
	if s.modules != nil {
		if entry, ok := s.modules.Get(source.ModuleID); ok {
			source.ModuleName = entry.Manifest.Name
		}
	}
	if source.ProviderID == "" {
		source.ProviderName, source.ProviderGroup = source.ModuleName, "模块来源"
	}
	return source
}

type moduleCandidateRef struct {
	Media       *module.MediaDescription `json:"media,omitempty"`
	ModuleID    string                   `json:"moduleId"`
	CandidateID string                   `json:"candidateId"`
	Payload     string                   `json:"payload"`
	Revision    string                   `json:"revision"`
	Subtitles   []module.SubtitleTrack   `json:"subtitles,omitempty"`
}

func (s *Service) sourceModule(source sourceRecord) (*module.ModuleEntry, error) {
	if s.modules == nil {
		return nil, errors.New("播放源模块尚未配置")
	}
	entry, ok := s.modules.Get(source.ModuleID)
	if !ok || entry.Status != module.StatusActive || entry.StreamInstance == nil {
		return nil, errors.New("播放源模块已停用或不可用")
	}
	if source.ItemID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if !s.playbackScopeMatches(ctx, entry, source.ItemID) {
			return nil, errors.New("播放源模块不适用于此媒体所在的库")
		}
	}
	return entry, nil
}

func (s *Service) playbackScopeMatches(ctx context.Context, entry *module.ModuleEntry, itemID string) bool {
	if entry.LibraryScope == nil || entry.LibraryScope.Mode == "all" {
		return true
	}
	clause, args := catalog.PlaybackScopeSQL(entry.LibraryScope, "scope_item")
	args = append([]any{itemID}, args...)
	var count int
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM media_items scope_item WHERE scope_item.id=? AND scope_item.deleted_at IS NULL AND ("+clause+")"), args...).Scan(&count)
	return err == nil && count > 0
}
func (s *Service) moduleSourceEnabled(source sourceRecord) bool {
	if s.modules == nil {
		return false
	}
	_, err := s.sourceModule(source)
	return err == nil
}

func moduleResolution(resolved module.ResolvedStream, preview bool) provider.ResolvedStream {
	protocol := resolved.Protocol
	if protocol == "direct" {
		protocol = "http"
	}
	result := provider.ResolvedStream{Media: resolved.Media, URL: resolved.StreamURL, Protocol: protocol, Headers: resolved.RequiredHeaders, DirectPlay: false, RequiresProxy: true, IsPreview: preview}
	if resolved.ExpiresAt != nil {
		result.ExpiresAt = resolved.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	for _, sub := range resolved.Subtitles {
		result.Subtitles = append(result.Subtitles, provider.Subtitle{URL: sub.URL, Language: sub.Language, Label: sub.Label, Format: sub.Format})
	}
	return result
}

func (s *Service) resolveModuleSource(ctx context.Context, item catalog.Item, source sourceRecord, force bool) (provider.ResolvedStream, error) {
	entry, err := s.sourceModule(source)
	if err != nil {
		return provider.ResolvedStream{}, err
	}
	ref, err := s.moduleReference(item, source)
	if err != nil {
		return provider.ResolvedStream{}, err
	}
	if ref.ModuleID != entry.Manifest.ID {
		return provider.ResolvedStream{}, errors.New("候选引用与模块不匹配")
	}
	if !supportsStreamItem(entry, module.ItemType(item.ItemType)) && item.ItemType != "" {
		return provider.ResolvedStream{}, errors.New("播放源模块不支持此媒体类型")
	}
	if source.ProviderID == "" && ref.Revision != entry.Revision {
		// Opaque references belong to an instance. Obtain a fresh candidate before
		// passing it to replacement code with a potentially different payload format.
		_ = s.discoverModuleSources(ctx, item, false)
		fresh, readErr := s.getSourceRecord(ctx, source.ID)
		if readErr != nil {
			return provider.ResolvedStream{}, readErr
		}
		freshRef, refErr := s.moduleReference(item, fresh)
		if refErr != nil || freshRef.Revision != entry.Revision {
			return provider.ResolvedStream{}, module.ErrRevisionChanged
		}
		source, ref = fresh, freshRef
	}
	expiry, expiryErr := time.Parse(time.RFC3339Nano, source.ExpiresAt)
	if !force && ref.Revision == entry.Revision && source.Status == "active" && expiryErr == nil && expiry.After(time.Now().Add(refreshWindow)) {
		target, urlErr := s.sourceURL(source)
		headers, headerErr := s.sourceHeaders(source)
		if urlErr == nil && headerErr == nil {
			result := moduleResolution(module.ResolvedStream{StreamURL: target, Protocol: source.Protocol, RequiredHeaders: headers, ExpiresAt: &expiry, Subtitles: ref.Subtitles, Media: ref.Media}, source.IsPreview)
			return result, s.modules.WithActiveRevision(ctx, entry.Manifest.ID, entry.Revision, func() error { return nil })
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, moduleCallTimeout)
	defer cancel()
	resolved, err := moduleCall(callCtx, s, func() (module.ResolvedStream, error) {
		return entry.StreamInstance.ResolveStream(callCtx, ref.CandidateID, ref.Payload, nil)
	})
	if changed := s.modules.WithActiveRevision(ctx, entry.Manifest.ID, entry.Revision, func() error { return nil }); changed != nil {
		return provider.ResolvedStream{}, changed
	}
	if err != nil {
		return provider.ResolvedStream{}, errors.Join(errors.New("模块播放源解析失败"), contextError(err))
	}
	if resolved.Media == nil {
		resolved.Media = ref.Media
	}
	if err := normalizeModuleMedia(&resolved); err != nil {
		return provider.ResolvedStream{}, err
	}
	result := moduleResolution(resolved, source.IsPreview)
	if !isPlaybackProtocol(result.Protocol) || s.validateUpstreamURL(source, result.URL) != nil || validateHeaders(result.Headers) != nil {
		return provider.ResolvedStream{}, errors.New("模块播放结果未通过校验")
	}
	if expiry, err := time.Parse(time.RFC3339Nano, result.ExpiresAt); err != nil || !expiry.After(time.Now()) {
		result.ExpiresAt = time.Now().UTC().Add(playbackTokenTTL).Format(time.RFC3339Nano)
	}
	ref.Revision, ref.Subtitles, ref.Media = entry.Revision, resolved.Subtitles, resolved.Media
	encoded, _ := json.Marshal(ref)
	if err := s.modules.WithActiveRevision(ctx, entry.Manifest.ID, entry.Revision, func() error {
		return s.persistResolved(ctx, source.ID, provider.Candidate{URL: string(encoded)}, result)
	}); err != nil {
		return provider.ResolvedStream{}, err
	}
	return result, nil
}

func (s *Service) moduleReference(item catalog.Item, source sourceRecord) (moduleCandidateRef, error) {
	payload, err := s.decrypt(source.CandidateEncrypted)
	if err != nil {
		return moduleCandidateRef{}, errors.New("候选引用无法解密")
	}
	var ref moduleCandidateRef
	if json.Unmarshal([]byte(payload), &ref) != nil || ref.ModuleID == "" {
		if source.ProviderID == "" {
			return moduleCandidateRef{}, errors.New("候选引用无效")
		}
		legacy, _ := json.Marshal(map[string]any{"providerId": source.ProviderID, "item": provider.Item{ID: item.ID, Code: item.Code, Title: item.Title, SourceURL: item.SourceURL}, "candidateUrl": payload})
		ref = moduleCandidateRef{ModuleID: builtinStreamModuleID, CandidateID: source.ID, Payload: string(legacy)}
	}
	if ref.ModuleID != source.ModuleID || ref.CandidateID == "" {
		return moduleCandidateRef{}, errors.New("候选引用与模块不匹配")
	}
	return ref, nil
}

func (s *Service) probeModuleSource(ctx context.Context, item catalog.Item, source sourceRecord) (provider.HealthResult, error) {
	entry, err := s.sourceModule(source)
	if err != nil {
		return provider.HealthResult{Error: "来源已停用或不可用"}, err
	}
	ref, err := s.moduleReference(item, source)
	if err != nil {
		return provider.HealthResult{Error: "候选引用无效"}, err
	}
	health, err := moduleCall(ctx, s, func() (module.HealthResult, error) {
		return entry.StreamInstance.ProbeSource(ctx, ref.CandidateID, ref.Payload)
	})
	result := provider.HealthResult{Healthy: health.Healthy && err == nil, StatusCode: health.StatusCode, DurationMS: health.DurationMS, Protocol: source.Protocol}
	if !result.Healthy {
		// Module errors may include upstream credentials or private URLs.
		result.Error = "来源探测未通过，请检查来源设置或稍后重试"
	}
	return result, err
}

func (s *Service) updateModuleSource(w http.ResponseWriter, r *http.Request, source sourceRecord, input sourcePatch) {
	if input.ProviderID != nil || input.Protocol != nil || input.CandidateURL != nil || input.ResolvedURL != nil || input.ResolvedHeaders != nil || input.ExpiresAt != nil || input.Status != nil {
		writeError(w, r, http.StatusBadRequest, "MODULE_SOURCE_MANAGED", "播放地址由来源自动管理，请到来源设置中修改连接")
		return
	}
	if input.Label != nil {
		source.Label = strings.TrimSpace(*input.Label)
	}
	if source.Label == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "播放源名称不能为空")
		return
	}
	if input.Quality != nil {
		source.Quality = strings.TrimSpace(*input.Quality)
	}
	if input.Language != nil {
		source.Language = strings.TrimSpace(*input.Language)
	}
	if input.SortOrder != nil {
		source.SortOrder = *input.SortOrder
	}
	if input.IsPreview != nil {
		source.IsPreview = *input.IsPreview
	}
	if input.Enabled != nil {
		source.Enabled = *input.Enabled
	}
	if input.DurationSeconds != nil {
		source.DurationSeconds = input.DurationSeconds
	}
	if input.SizeBytes != nil {
		source.SizeBytes = input.SizeBytes
	}
	locked := input.Label != nil || input.Quality != nil || input.Language != nil
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind(`UPDATE stream_sources SET label=?, quality=?, language=?, sort_order=?, is_preview=?, enabled=?, duration_seconds=?, size_bytes=?, attributes_locked=CASE WHEN ? = 1 THEN 1 ELSE attributes_locked END, updated_at=? WHERE id=? AND item_id=?`), source.Label, nullable(source.Quality), nullable(source.Language), source.SortOrder, boolInt(source.IsPreview), boolInt(source.Enabled), source.DurationSeconds, source.SizeBytes, boolInt(locked), time.Now().UTC().Format(time.RFC3339Nano), source.ID, source.ItemID)
	if err != nil {
		writeError(w, r, http.StatusConflict, "SOURCE_UPDATE_FAILED", "更新播放源失败")
		return
	}
	updated, err := s.getSourceRecord(r.Context(), source.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取更新后的播放源失败")
		return
	}
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok {
		s.audit(r.Context(), principal.User.ID, "update_playback_source", "playback_source", source.ID, middleware.GetReqID(r.Context()), map[string]any{"itemId": source.ItemID, "enabled": source.Enabled})
	}
	writeJSON(w, http.StatusOK, updated.view())
}
