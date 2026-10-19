package playback

import (
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
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
	provider "github.com/graysui/JMlab/internal/stream"
)

const (
	maxBodyBytes         = 2 << 20
	maxPlaylistBytes     = 4 << 20
	maxRangeBytes        = 64 << 20
	dialTimeout          = 10 * time.Second
	firstByteTimeout     = 15 * time.Second
	playbackTokenVersion = "playback-v3"
	resourceTokenVersion = "resource-v3"
	playbackTokenTTL     = 10 * time.Minute
	refreshWindow        = 30 * time.Second
)

type Service struct {
	db          *repository.Database
	auth        *auth.Service
	catalog     *catalog.Service
	modules     *module.Registry
	discoveryMu sync.Mutex
	discoveries map[string]*discoveryFlight
	moduleCalls chan struct{}
	masterKey   []byte
	publicURL   string
	httpClient  *http.Client
}

type sourceRecord struct {
	ID                   string
	ItemID               string
	ModuleID             string
	ModuleName           string
	ProviderID           string
	ProviderType         provider.ProviderType
	ProviderName         string
	ProviderGroup        string
	ProviderPriority     int
	Label                string
	Protocol             string
	Quality              string
	Language             string
	CandidateEncrypted   string
	ResolvedEncrypted    string
	HeadersEncrypted     string
	DurationSeconds      *int
	SizeBytes            *int64
	IsPreview            bool
	Status               string
	Enabled              bool
	ExpiresAt            string
	LastResolvedAt       string
	LastError            string
	LastProbeStatus      string
	LastProbeAt          string
	LastProbeDurationMS  int64
	LastProbeError       string
	ProbeCount           int64
	ProbeSuccessCount    int64
	ProbeDurationTotalMS int64
	SortOrder            int
	CreatedAt            string
	UpdatedAt            string
	AllowedHosts         []string
}

type SourceView struct {
	ID                     string                `json:"id"`
	ItemID                 string                `json:"itemId"`
	ModuleID               string                `json:"moduleId"`
	ModuleName             string                `json:"moduleName,omitempty"`
	ProviderID             string                `json:"providerId"`
	ProviderType           provider.ProviderType `json:"providerType"`
	ProviderName           string                `json:"providerName,omitempty"`
	ProviderGroup          string                `json:"providerGroup,omitempty"`
	Label                  string                `json:"label"`
	Protocol               string                `json:"protocol"`
	Quality                string                `json:"quality,omitempty"`
	Language               string                `json:"language,omitempty"`
	DurationSeconds        *int                  `json:"durationSeconds,omitempty"`
	SizeBytes              *int64                `json:"sizeBytes,omitempty"`
	IsPreview              bool                  `json:"isPreview"`
	Status                 string                `json:"status"`
	Enabled                bool                  `json:"enabled"`
	ExpiresAt              string                `json:"expiresAt,omitempty"`
	LastResolvedAt         string                `json:"lastResolvedAt,omitempty"`
	LastError              string                `json:"lastError,omitempty"`
	LastProbeStatus        string                `json:"lastProbeStatus,omitempty"`
	LastProbeAt            string                `json:"lastProbeAt,omitempty"`
	LastProbeDurationMS    int64                 `json:"lastProbeDurationMs,omitempty"`
	LastProbeError         string                `json:"lastProbeError,omitempty"`
	ProbeCount             int64                 `json:"probeCount"`
	ProbeSuccessCount      int64                 `json:"probeSuccessCount"`
	AverageProbeDurationMS int64                 `json:"averageProbeDurationMs"`
	SortOrder              int                   `json:"sortOrder"`
	CreatedAt              string                `json:"createdAt"`
	UpdatedAt              string                `json:"updatedAt"`
}

const sourceSelectColumns = "SELECT ps.id, ps.item_id, ps.module_id, COALESCE(ps.provider_id, ''), COALESCE(op.provider_type, 'external-online'), COALESCE(op.display_name, ps.module_id), COALESCE(op.group_name, ''), COALESCE(op.priority, ps.priority), ps.label, ps.protocol, COALESCE(ps.quality, ''), COALESCE(ps.language, ''), COALESCE(ps.candidate_url_encrypted, ''), COALESCE(ps.resolved_url_encrypted, ''), COALESCE(ps.resolved_headers_encrypted, ''), ps.duration_seconds, ps.size_bytes, ps.is_preview, ps.status, ps.enabled, COALESCE(ps.expires_at, ''), COALESCE(ps.last_resolved_at, ''), COALESCE(ps.last_error, ''), COALESCE(ps.last_probe_status, ''), COALESCE(ps.last_probe_at, ''), COALESCE(ps.last_probe_duration_ms, 0), COALESCE(ps.last_probe_error, ''), COALESCE(ps.probe_count, 0), COALESCE(ps.probe_success_count, 0), COALESCE(ps.probe_duration_ms_total, 0), ps.sort_order, ps.created_at, ps.updated_at, COALESCE(op.allowed_hosts_json, '[]')"

type SelectionOptions struct {
	SourceID     string
	Protocol     string
	Quality      string
	Language     string
	AllowPreview bool
	Refresh      bool
}

type PlaybackResolution struct {
	Media         *provider.MediaDescription `json:"media,omitempty"`
	Source        SourceView                 `json:"source"`
	PlaybackURL   string                     `json:"playbackUrl"`
	Protocol      string                     `json:"protocol"`
	ExpiresAt     string                     `json:"expiresAt"`
	DirectPlay    bool                       `json:"directPlay"`
	RequiresProxy bool                       `json:"requiresProxy"`
	IsPreview     bool                       `json:"isPreview"`
	Subtitles     []provider.Subtitle        `json:"subtitles,omitempty"`
}

type boundPlaybackToken struct {
	ModuleID  string
	Revision  string
	SourceID  string
	ItemID    string
	UserID    string
	Target    string
	ExpiresAt time.Time
}

func NewService(db *repository.Database, authService *auth.Service, catalogService *catalog.Service, masterKey string) *Service {
	key := sha256.Sum256([]byte(masterKey))
	return &Service{
		db:        db,
		auth:      authService,
		catalog:   catalogService,
		masterKey: key[:],
		httpClient: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   dialTimeout,
			ResponseHeaderTimeout: firstByteTimeout,
			ExpectContinueTimeout: time.Second,
		}},
	}
}

func (s *Service) SetModuleRegistry(registry *module.Registry) { s.modules = registry }
func (s *Service) SetPublicURL(publicURL string) {
	s.publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
}

// DiscoverItemSources asks enabled stream modules for candidates without changing media identity.
func (s *Service) DiscoverItemSources(ctx context.Context, item catalog.Item) error {
	return s.discoverModuleSources(ctx, item, false)
}

type sourceDefinition struct {
	moduleID, label string
	isPreview       bool
}

func (s *Service) upsertDiscoveredSource(ctx context.Context, itemID string, definition sourceDefinition, candidate provider.Candidate, resolved provider.ResolvedStream, sourceKey string) error {
	if strings.TrimSpace(sourceKey) == "" {
		return errors.New("播放源缺少稳定标识")
	}
	if strings.TrimSpace(candidate.URL) == "" {
		candidate.URL = resolved.URL
	}
	if strings.TrimSpace(resolved.URL) == "" {
		return errors.New("播放源解析结果缺少地址")
	}
	if resolved.Protocol != "hls" && resolved.Protocol != "http" && resolved.Protocol != "redirect" {
		return errors.New("播放源解析结果协议无效")
	}
	if err := validateHeaders(resolved.Headers); err != nil {
		return errors.New("播放源请求头无效")
	}
	candidateEncrypted, err := s.encrypt(strings.TrimSpace(candidate.URL))
	if err != nil {
		return errors.New("保存播放源失败")
	}
	resolvedEncrypted, err := s.encrypt(strings.TrimSpace(resolved.URL))
	if err != nil {
		return errors.New("保存播放源失败")
	}
	encodedHeaders, err := json.Marshal(resolved.Headers)
	if err != nil {
		return errors.New("保存播放源失败")
	}
	headersEncrypted, err := s.encrypt(string(encodedHeaders))
	if err != nil {
		return errors.New("保存播放源失败")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	expiresAt := strings.TrimSpace(resolved.ExpiresAt)
	if parsed, parseErr := time.Parse(time.RFC3339, expiresAt); parseErr != nil || !parsed.After(time.Now().UTC()) {
		expiresAt = time.Now().UTC().Add(playbackTokenTTL).Format(time.RFC3339)
	}
	isPreview := definition.isPreview || resolved.IsPreview
	var sourceID string
	err = s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id FROM stream_sources WHERE id = ?"), sourceKey).Scan(&sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		sourceID = sourceKey
		_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO stream_sources (id, item_id, module_id, provider_id, label, protocol, candidate_url_encrypted, resolved_url_encrypted, resolved_headers_encrypted, is_preview, status, last_resolved_at, expires_at, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)"), sourceID, itemID, definition.moduleID, nil, definition.label, resolved.Protocol, candidateEncrypted, resolvedEncrypted, headersEncrypted, boolInt(isPreview), now, expiresAt, boolInt(isPreview), now, now)
	} else if err == nil {
		_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE stream_sources SET protocol = ?, candidate_url_encrypted = ?, resolved_url_encrypted = ?, resolved_headers_encrypted = ?, status = 'active', last_resolved_at = ?, expires_at = ?, last_error = NULL, updated_at = ? WHERE id = ?"), resolved.Protocol, candidateEncrypted, resolvedEncrypted, headersEncrypted, now, expiresAt, now, sourceID)
	}
	if err != nil {
		return errors.New("保存播放源失败")
	}
	var duration *int
	var size *int64
	if resolved.Media != nil {
		duration, size = resolved.Media.DurationSeconds, resolved.Media.SizeBytes
	}
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE stream_sources SET label = CASE WHEN attributes_locked = 0 THEN ? ELSE label END, quality = CASE WHEN attributes_locked = 0 THEN ? ELSE quality END, language=CASE WHEN attributes_locked=0 THEN ? ELSE language END, duration_seconds=COALESCE(?,duration_seconds), size_bytes=COALESCE(?,size_bytes), updated_at = ? WHERE id = ?"), definition.label, nullable(candidate.Quality), nullable(candidate.Language), duration, size, now, sourceID)
	if err != nil {
		return errors.New("保存播放源属性失败")
	}
	return nil
}

func (s *Service) RegisterRoutes(router chi.Router) {
	admin := router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin", "Editor"))
	admin.With(s.auth.RequireCSRF).Post("/playback/sources", s.createSource)
	admin.Get("/playback/sources/{sourceID}", s.getSource)
	admin.Get("/admin/items/{itemID}/sources", s.listAdminSources)
	admin.With(s.auth.RequireCSRF).Post("/admin/items/{itemID}/sources", s.createAdminSource)
	admin.With(s.auth.RequireCSRF).Patch("/admin/items/{itemID}/sources/{sourceID}", s.updateSource)
	admin.With(s.auth.RequireCSRF).Post("/admin/items/{itemID}/sources/{sourceID}/disable", s.disableSource)
	admin.With(s.auth.RequireCSRF).Post("/admin/items/{itemID}/sources/{sourceID}/enable", s.enableSource)
	admin.With(s.auth.RequireCSRF).Post("/admin/items/{itemID}/sources/{sourceID}/probe", s.probeSource)
	admin.With(s.auth.RequireCSRF).Post("/admin/items/{itemID}/sources/probe", s.probeSources)
	user := router.With(s.auth.RequireAuth)
	user.Get("/items/{itemID}/sources", s.listSources)
	user.Post("/items/{itemID}/sources/{sourceID}/resolve", s.resolveSource)
}

func (s *Service) RegisterProtocolRoutes(router chi.Router) {
	router.With(s.auth.TryAuth).Get("/Videos/{itemID}/stream", s.stream)
	router.With(s.auth.TryAuth).Head("/Videos/{itemID}/stream", s.stream)
	router.With(s.auth.TryAuth).Get("/Videos/{itemID}/master.m3u8", s.masterPlaylist)
	protected := router.With(s.auth.RequireAuth)
	protected.Get("/Items/{itemID}/PlaybackInfo", s.playbackInfo)
	protected.Post("/Items/{itemID}/PlaybackInfo", s.playbackInfo)
}

func unescapeParam(r *http.Request, key string) string {
	val := chi.URLParam(r, key)
	if unescaped, err := url.PathUnescape(val); err == nil {
		return unescaped
	}
	return val
}

func (s *Service) createSource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ItemID          string            `json:"itemId"`
		ProviderID      string            `json:"providerId"`
		Label           string            `json:"label"`
		Protocol        string            `json:"protocol"`
		Quality         string            `json:"quality"`
		Language        string            `json:"language"`
		CandidateURL    string            `json:"candidateUrl"`
		ResolvedURL     string            `json:"resolvedUrl"`
		ResolvedHeaders map[string]string `json:"resolvedHeaders"`
		DurationSeconds *int              `json:"durationSeconds"`
		SizeBytes       *int64            `json:"sizeBytes"`
		IsPreview       bool              `json:"isPreview"`
		ExpiresAt       string            `json:"expiresAt"`
		SortOrder       int               `json:"sortOrder"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "播放源格式不正确")
		return
	}
	input.ItemID, input.ProviderID, input.Label, input.Protocol = strings.TrimSpace(input.ItemID), strings.TrimSpace(input.ProviderID), strings.TrimSpace(input.Label), strings.ToLower(strings.TrimSpace(input.Protocol))
	if pathItemID := strings.TrimSpace(unescapeParam(r, "itemID")); pathItemID != "" {
		if input.ItemID != "" && input.ItemID != pathItemID {
			writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "路径作品与请求作品不一致")
			return
		}
		input.ItemID = pathItemID
	}
	if input.ItemID == "" || input.ProviderID == "" || input.Label == "" || input.Protocol == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "作品、Provider、标签和协议不能为空")
		return
	}
	if input.Protocol != "hls" && input.Protocol != "http" && input.Protocol != "redirect" {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "协议必须是 hls、http 或 redirect")
		return
	}
	if _, err := s.itemExists(r.Context(), input.ItemID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, r, http.StatusNotFound, "ITEM_NOT_FOUND", "作品不存在")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "ITEM_READ_FAILED", "读取作品失败")
		return
	}
	hosts, err := s.providerHosts(r.Context(), input.ProviderID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "PROVIDER_NOT_FOUND", "Provider 不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "PROVIDER_READ_FAILED", "读取 Provider 失败")
		return
	}
	resolvedURL := strings.TrimSpace(input.ResolvedURL)
	if resolvedURL == "" {
		resolvedURL = strings.TrimSpace(input.CandidateURL)
	}
	if strings.TrimSpace(input.CandidateURL) != "" {
		if err := provider.ValidateCandidateURL(input.CandidateURL, hosts); err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE_URL", "候选播放地址未通过 HTTPS 和域名校验")
			return
		}
	}
	if err := provider.ValidateCandidateURL(resolvedURL, hosts); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE_URL", "播放源地址未通过 HTTPS 和域名校验")
		return
	}
	if err := validateHeaders(input.ResolvedHeaders); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE_HEADERS", err.Error())
		return
	}
	if input.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "expiresAt 必须是 RFC3339 时间")
			return
		}
		if expires.Before(time.Now().UTC()) {
			writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "expiresAt 必须晚于当前时间")
			return
		}
	}
	candidateEncrypted, err := s.encrypt(strings.TrimSpace(input.CandidateURL))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_ENCRYPT_FAILED", "保存播放源失败")
		return
	}
	resolvedEncrypted, err := s.encrypt(resolvedURL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_ENCRYPT_FAILED", "保存播放源失败")
		return
	}
	headerJSON, _ := json.Marshal(input.ResolvedHeaders)
	headerEncrypted, err := s.encrypt(string(headerJSON))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_ENCRYPT_FAILED", "保存播放源失败")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := newID("source")
	_, err = s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO stream_sources (id, item_id, provider_id, label, protocol, quality, language, candidate_url_encrypted, resolved_url_encrypted, resolved_headers_encrypted, duration_seconds, size_bytes, is_preview, status, last_resolved_at, expires_at, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)"), id, input.ItemID, input.ProviderID, input.Label, input.Protocol, nullable(input.Quality), nullable(input.Language), candidateEncrypted, resolvedEncrypted, headerEncrypted, input.DurationSeconds, input.SizeBytes, boolInt(input.IsPreview), now, nullable(input.ExpiresAt), input.SortOrder, now, now)
	if err != nil {
		writeError(w, r, http.StatusConflict, "SOURCE_CREATE_FAILED", "创建播放源失败")
		return
	}
	source, err := s.getSourceRecord(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取新建播放源失败")
		return
	}
	writeJSON(w, http.StatusCreated, source.view())
}

func (s *Service) createAdminSource(w http.ResponseWriter, r *http.Request) {
	s.createSource(w, r)
}

func (s *Service) getSource(w http.ResponseWriter, r *http.Request) {
	source, err := s.getSourceRecord(r.Context(), chi.URLParam(r, "sourceID"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "SOURCE_NOT_FOUND", "播放源不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取播放源失败")
		return
	}
	writeJSON(w, http.StatusOK, source.view())
}

func (s *Service) listAdminSources(w http.ResponseWriter, r *http.Request) {
	itemID := strings.TrimSpace(unescapeParam(r, "itemID"))
	if _, err := s.itemForAdmin(r.Context(), itemID); errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "ITEM_NOT_FOUND", "作品不存在")
		return
	} else if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ITEM_READ_FAILED", "读取作品失败")
		return
	}
	sources, err := s.listAdminSourceRecords(r.Context(), itemID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCES_READ_FAILED", "读取作品播放源失败")
		return
	}
	items := make([]SourceView, 0, len(sources))
	for _, source := range sources {
		items = append(items, source.view())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"itemId":  itemID,
		"items":   items,
		"summary": map[string]int{"total": len(items), "enabled": countEnabledSources(sources), "healthy": countHealthySources(sources)},
	})
}

type sourcePatch struct {
	ProviderID      *string            `json:"providerId"`
	Label           *string            `json:"label"`
	Protocol        *string            `json:"protocol"`
	Quality         *string            `json:"quality"`
	Language        *string            `json:"language"`
	CandidateURL    *string            `json:"candidateUrl"`
	ResolvedURL     *string            `json:"resolvedUrl"`
	ResolvedHeaders *map[string]string `json:"resolvedHeaders"`
	DurationSeconds *int               `json:"durationSeconds"`
	SizeBytes       *int64             `json:"sizeBytes"`
	IsPreview       *bool              `json:"isPreview"`
	ExpiresAt       *string            `json:"expiresAt"`
	SortOrder       *int               `json:"sortOrder"`
	Enabled         *bool              `json:"enabled"`
	Status          *string            `json:"status"`
}

func (s *Service) updateSource(w http.ResponseWriter, r *http.Request) {
	itemID := strings.TrimSpace(unescapeParam(r, "itemID"))
	sourceID := strings.TrimSpace(chi.URLParam(r, "sourceID"))
	source, err := s.getSourceRecord(r.Context(), sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "SOURCE_NOT_FOUND", "播放源不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取播放源失败")
		return
	}
	if source.ItemID != itemID {
		writeError(w, r, http.StatusNotFound, "SOURCE_NOT_FOUND", "播放源不存在")
		return
	}
	var input sourcePatch
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "播放源格式不正确")
		return
	}
	if source.ProviderID == "" {
		s.updateModuleSource(w, r, source, input)
		return
	}
	providerID := source.ProviderID
	if input.ProviderID != nil {
		providerID = strings.TrimSpace(*input.ProviderID)
	}
	hosts, err := s.providerHostsForAdmin(r.Context(), providerID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "PROVIDER_NOT_FOUND", "Provider 不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "PROVIDER_READ_FAILED", "读取 Provider 失败")
		return
	}
	label, protocol, quality, language := source.Label, source.Protocol, source.Quality, source.Language
	if input.Label != nil {
		label = strings.TrimSpace(*input.Label)
	}
	if input.Protocol != nil {
		protocol = strings.ToLower(strings.TrimSpace(*input.Protocol))
	}
	if input.Quality != nil {
		quality = strings.TrimSpace(*input.Quality)
	}
	if input.Language != nil {
		language = strings.TrimSpace(*input.Language)
	}
	if label == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "播放源标签不能为空")
		return
	}
	if protocol != "hls" && protocol != "http" && protocol != "redirect" {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "协议必须是 hls、http 或 redirect")
		return
	}
	candidateURL, resolvedURL := "", ""
	if source.CandidateEncrypted != "" {
		candidateURL, _ = s.decrypt(source.CandidateEncrypted)
	}
	if source.ResolvedEncrypted != "" {
		resolvedURL, _ = s.decrypt(source.ResolvedEncrypted)
	}
	if input.CandidateURL != nil {
		candidateURL = strings.TrimSpace(*input.CandidateURL)
	}
	if input.ResolvedURL != nil {
		resolvedURL = strings.TrimSpace(*input.ResolvedURL)
	}
	if resolvedURL == "" {
		resolvedURL = candidateURL
	}
	if resolvedURL == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE_URL", "播放源地址不能为空")
		return
	}
	if candidateURL != "" {
		if err := provider.ValidateCandidateURL(candidateURL, hosts); err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE_URL", "候选播放地址未通过 HTTPS 和域名校验")
			return
		}
	}
	if err := provider.ValidateCandidateURL(resolvedURL, hosts); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE_URL", "播放源地址未通过 HTTPS 和域名校验")
		return
	}
	headers := map[string]string{}
	if source.HeadersEncrypted != "" {
		if encoded, decryptErr := s.decrypt(source.HeadersEncrypted); decryptErr == nil {
			_ = json.Unmarshal([]byte(encoded), &headers)
		}
	}
	if input.ResolvedHeaders != nil {
		headers = *input.ResolvedHeaders
	}
	if err := validateHeaders(headers); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE_HEADERS", err.Error())
		return
	}
	expiresAt := source.ExpiresAt
	if input.ExpiresAt != nil {
		expiresAt = strings.TrimSpace(*input.ExpiresAt)
	}
	if expiresAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, expiresAt)
		if parseErr != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "expiresAt 必须是 RFC3339 时间")
			return
		}
		if parsed.Before(time.Now().UTC()) && (input.ExpiresAt != nil) {
			writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "expiresAt 必须晚于当前时间")
			return
		}
	}
	if input.Status != nil && *input.Status != "active" && *input.Status != "unavailable" && *input.Status != "expired" {
		writeError(w, r, http.StatusBadRequest, "INVALID_SOURCE", "status 必须是 active、unavailable 或 expired")
		return
	}
	status := source.Status
	if input.Status != nil {
		status = strings.TrimSpace(*input.Status)
	}
	enabled := source.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.Enabled != nil && enabled && status == "unavailable" && source.LastError == "" {
		status = "active"
	}
	durationSeconds, sizeBytes, isPreview, sortOrder := source.DurationSeconds, source.SizeBytes, source.IsPreview, source.SortOrder
	if input.DurationSeconds != nil {
		durationSeconds = input.DurationSeconds
	}
	if input.SizeBytes != nil {
		sizeBytes = input.SizeBytes
	}
	if input.IsPreview != nil {
		isPreview = *input.IsPreview
	}
	if input.SortOrder != nil {
		sortOrder = *input.SortOrder
	}
	candidateEncrypted, err := s.encrypt(candidateURL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_ENCRYPT_FAILED", "保存播放源失败")
		return
	}
	resolvedEncrypted, err := s.encrypt(resolvedURL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_ENCRYPT_FAILED", "保存播放源失败")
		return
	}
	headerJSON, _ := json.Marshal(headers)
	headerEncrypted, err := s.encrypt(string(headerJSON))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_ENCRYPT_FAILED", "保存播放源失败")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE stream_sources SET provider_id = ?, label = ?, protocol = ?, quality = ?, language = ?, candidate_url_encrypted = ?, resolved_url_encrypted = ?, resolved_headers_encrypted = ?, duration_seconds = ?, size_bytes = ?, is_preview = ?, status = ?, enabled = ?, expires_at = ?, sort_order = ?, updated_at = ? WHERE id = ? AND item_id = ?"), providerID, label, protocol, nullable(quality), nullable(language), candidateEncrypted, resolvedEncrypted, headerEncrypted, durationSeconds, sizeBytes, boolInt(isPreview), status, boolInt(enabled), nullable(expiresAt), sortOrder, now, sourceID, itemID)
	if err != nil {
		writeError(w, r, http.StatusConflict, "SOURCE_UPDATE_FAILED", "更新播放源失败")
		return
	}
	updated, err := s.getSourceRecord(r.Context(), sourceID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取更新后的播放源失败")
		return
	}
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok {
		s.audit(r.Context(), principal.User.ID, "update_playback_source", "playback_source", sourceID, middleware.GetReqID(r.Context()), map[string]any{"itemId": itemID, "enabled": enabled})
	}
	writeJSON(w, http.StatusOK, updated.view())
}

func (s *Service) disableSource(w http.ResponseWriter, r *http.Request) {
	s.setSourceEnabled(w, r, false)
}
func (s *Service) enableSource(w http.ResponseWriter, r *http.Request) {
	s.setSourceEnabled(w, r, true)
}

func (s *Service) setSourceEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	itemID, sourceID := strings.TrimSpace(unescapeParam(r, "itemID")), strings.TrimSpace(chi.URLParam(r, "sourceID"))
	source, err := s.getSourceRecord(r.Context(), sourceID)
	if errors.Is(err, sql.ErrNoRows) || source.ItemID != itemID {
		writeError(w, r, http.StatusNotFound, "SOURCE_NOT_FOUND", "播放源不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取播放源失败")
		return
	}
	status := source.Status
	if enabled && status == "unavailable" {
		status = "active"
	}
	_, err = s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE stream_sources SET enabled = ?, status = ?, updated_at = ? WHERE id = ? AND item_id = ?"), boolInt(enabled), status, time.Now().UTC().Format(time.RFC3339Nano), sourceID, itemID)
	if err != nil {
		writeError(w, r, http.StatusConflict, "SOURCE_UPDATE_FAILED", "更新播放源状态失败")
		return
	}
	updated, err := s.getSourceRecord(r.Context(), sourceID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取播放源失败")
		return
	}
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok {
		action := "disable_playback_source"
		if enabled {
			action = "enable_playback_source"
		}
		s.audit(r.Context(), principal.User.ID, action, "playback_source", sourceID, middleware.GetReqID(r.Context()), map[string]any{"itemId": itemID})
	}
	writeJSON(w, http.StatusOK, updated.view())
}

type sourceProbeResponse struct {
	Source     SourceView `json:"source"`
	Healthy    bool       `json:"healthy"`
	StatusCode int        `json:"statusCode,omitempty"`
	Protocol   string     `json:"protocol,omitempty"`
	DurationMS int64      `json:"durationMs"`
	Error      string     `json:"error,omitempty"`
	CheckedAt  string     `json:"checkedAt"`
}

func (s *Service) probeSource(w http.ResponseWriter, r *http.Request) {
	itemID, sourceID := strings.TrimSpace(unescapeParam(r, "itemID")), strings.TrimSpace(chi.URLParam(r, "sourceID"))
	source, err := s.getSourceRecord(r.Context(), sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "SOURCE_NOT_FOUND", "播放源不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取播放源失败")
		return
	}
	if source.ItemID != itemID {
		writeError(w, r, http.StatusNotFound, "SOURCE_NOT_FOUND", "播放源不存在")
		return
	}
	result, itemErr := s.probeSourceRecord(r.Context(), source)
	if itemErr != nil && result.Error == "" {
		result.Error = "播放源探测失败"
	}
	updated, readErr := s.getSourceRecord(r.Context(), sourceID)
	if readErr != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCE_READ_FAILED", "读取探测结果失败")
		return
	}
	writeJSON(w, http.StatusOK, sourceProbeResponse{Source: updated.view(), Healthy: result.Healthy, StatusCode: result.StatusCode, Protocol: result.Protocol, DurationMS: result.DurationMS, Error: result.Error, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)})
}

func (s *Service) probeSources(w http.ResponseWriter, r *http.Request) {
	itemID := strings.TrimSpace(unescapeParam(r, "itemID"))
	if _, err := s.itemForAdmin(r.Context(), itemID); errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "ITEM_NOT_FOUND", "作品不存在")
		return
	} else if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ITEM_READ_FAILED", "读取作品失败")
		return
	}
	var input struct {
		SourceIDs []string `json:"sourceIds"`
	}
	if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "INVALID_PROBE_REQUEST", "批量探测请求格式不正确")
		return
	}
	ids := make(map[string]struct{}, len(input.SourceIDs))
	for _, id := range input.SourceIDs {
		if value := strings.TrimSpace(id); value != "" {
			ids[value] = struct{}{}
		}
	}
	sources, err := s.listAdminSourceRecords(r.Context(), itemID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCES_READ_FAILED", "读取作品播放源失败")
		return
	}
	results := make([]sourceProbeResponse, 0, len(sources))
	for _, source := range sources {
		if len(ids) > 0 {
			if _, ok := ids[source.ID]; !ok {
				continue
			}
		}
		result, probeErr := s.probeSourceRecord(r.Context(), source)
		if probeErr != nil && result.Error == "" {
			result.Error = "播放源探测失败"
		}
		updated, readErr := s.getSourceRecord(r.Context(), source.ID)
		if readErr != nil {
			continue
		}
		results = append(results, sourceProbeResponse{Source: updated.view(), Healthy: result.Healthy, StatusCode: result.StatusCode, Protocol: result.Protocol, DurationMS: result.DurationMS, Error: result.Error, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	healthy := 0
	for _, result := range results {
		if result.Healthy {
			healthy++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"itemId": itemID, "items": results, "summary": map[string]int{"total": len(results), "healthy": healthy, "failed": len(results) - healthy}})
}

func (s *Service) probeSourceRecord(ctx context.Context, source sourceRecord) (provider.HealthResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	item, err := s.itemForAdmin(ctx, source.ItemID)
	if err != nil {
		return provider.HealthResult{Healthy: false, Error: "作品不存在"}, err
	}
	entry, err := s.sourceModule(source)
	if err != nil {
		return provider.HealthResult{Error: "来源已停用或不可用"}, err
	}
	result, probeErr := s.probeModuleSource(ctx, catalog.Item{ID: item.ID, Code: item.Code, Title: item.Title, SourceURL: item.SourceURL}, source)
	if err := s.modules.WithActiveRevision(ctx, entry.Manifest.ID, entry.Revision, func() error {
		return s.recordSourceProbe(ctx, source.ID, result)
	}); err != nil {
		return result, err
	}
	return result, probeErr
}

func (s *Service) recordSourceProbe(ctx context.Context, sourceID string, result provider.HealthResult) error {
	status := "unhealthy"
	if result.Healthy {
		status = "healthy"
	}
	errText := strings.TrimSpace(result.Error)
	if len([]rune(errText)) > 240 {
		errText = string([]rune(errText)[:240])
	}
	activeStatus := "unavailable"
	if result.Healthy {
		activeStatus = "active"
	}
	_, err := s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE stream_sources SET last_probe_status = ?, last_probe_at = ?, last_probe_duration_ms = ?, last_probe_error = ?, probe_count = probe_count + 1, probe_success_count = probe_success_count + ?, probe_duration_ms_total = probe_duration_ms_total + ?, status = CASE WHEN enabled = 1 THEN ? ELSE status END, last_error = CASE WHEN ? = '' THEN last_error ELSE ? END, updated_at = ? WHERE id = ?"), status, time.Now().UTC().Format(time.RFC3339Nano), result.DurationMS, nullable(errText), boolInt(result.Healthy), result.DurationMS, activeStatus, errText, nullable(errText), time.Now().UTC().Format(time.RFC3339Nano), sourceID)
	return err
}

func (s *Service) listAdminSourceRecords(ctx context.Context, itemID string) ([]sourceRecord, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(sourceSelectColumns+" FROM stream_sources ps LEFT JOIN online_providers op ON op.id = ps.provider_id WHERE ps.item_id = ? ORDER BY ps.sort_order, COALESCE(op.priority, ps.priority) DESC, ps.id"), itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]sourceRecord, 0)
	for rows.Next() {
		source, scanErr := scanSource(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		sources = append(sources, s.describeSource(source))
	}
	return sources, rows.Err()
}

func countEnabledSources(sources []sourceRecord) int {
	count := 0
	for _, source := range sources {
		if source.Enabled {
			count++
		}
	}
	return count
}
func countHealthySources(sources []sourceRecord) int {
	count := 0
	for _, source := range sources {
		if source.Enabled && source.LastProbeStatus == "healthy" {
			count++
		}
	}
	return count
}

func (s *Service) listSources(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	itemID := unescapeParam(r, "itemID")
	item, err := s.authorizedItem(r.Context(), itemID, principal.User)
	if err != nil {
		s.writeSelectionError(w, r, err)
		return
	}
	sources, err := s.listSourceRecords(r.Context(), itemID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SOURCES_READ_FAILED", "读取播放源失败")
		return
	}
	if s.modules != nil || len(sources) <= 1 {
		_ = s.DiscoverItemSources(r.Context(), item)
		sources, err = s.listSourceRecords(r.Context(), itemID)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "SOURCES_READ_FAILED", "读取播放源失败")
			return
		}
	}
	items := make([]SourceView, 0, len(sources))
	for _, source := range sources {
		items = append(items, source.view())
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) resolveSource(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	options := SelectionOptions{SourceID: chi.URLParam(r, "sourceID"), Quality: strings.TrimSpace(r.URL.Query().Get("quality")), Language: strings.TrimSpace(r.URL.Query().Get("language")), AllowPreview: r.URL.Query().Get("allowPreview") == "1", Refresh: r.URL.Query().Get("refresh") == "1"}
	if r.ContentLength != 0 {
		var input struct {
			Quality      string `json:"quality"`
			Language     string `json:"language"`
			AllowPreview *bool  `json:"allowPreview"`
			Refresh      bool   `json:"refresh"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_PLAYBACK_REQUEST", "播放请求格式不正确")
			return
		}
		if strings.TrimSpace(input.Quality) != "" {
			options.Quality = strings.TrimSpace(input.Quality)
		}
		if strings.TrimSpace(input.Language) != "" {
			options.Language = strings.TrimSpace(input.Language)
		}
		if input.AllowPreview != nil {
			options.AllowPreview = *input.AllowPreview
		}
		options.Refresh = options.Refresh || input.Refresh
	}
	item, err := s.authorizedItem(r.Context(), unescapeParam(r, "itemID"), principal.User)
	if err != nil {
		s.writeSelectionError(w, r, err)
		return
	}
	selection, err := s.selectAndResolve(r.Context(), item, principal.User, options)
	if err != nil {
		s.writeSelectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, selection)
}

func (s *Service) playbackInfo(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	item, err := s.authorizedItem(r.Context(), unescapeParam(r, "itemID"), principal.User)
	if err != nil {
		s.writeSelectionError(w, r, err)
		return
	}

	var postBody struct {
		MediaSourceId       string `json:"MediaSourceId"`
		SourceId            string `json:"SourceId"`
		Quality             string `json:"Quality"`
		Language            string `json:"Language"`
		AllowPreview        *bool  `json:"AllowPreview"`
		PlaySessionId       string `json:"PlaySessionId"`
		StartTimeTicks      *int64 `json:"StartTimeTicks"`
		AudioStreamIndex    *int   `json:"AudioStreamIndex"`
		SubtitleStreamIndex *int   `json:"SubtitleStreamIndex"`
		EnableDirectPlay    *bool  `json:"EnableDirectPlay"`
		EnableDirectStream  *bool  `json:"EnableDirectStream"`
	}
	if r.Method == http.MethodPost && r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&postBody); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, r, 400, "INVALID_PLAYBACK_REQUEST", "播放参数格式无效")
			return
		}
	}

	if raw := firstQuery(r, "StartTimeTicks", "startTimeTicks"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writeError(w, r, 400, "INVALID_PLAYBACK_REQUEST", "续播时间无效")
			return
		}
		postBody.StartTimeTicks = &value
	}
	if postBody.StartTimeTicks != nil && *postBody.StartTimeTicks < 0 {
		writeError(w, r, 400, "INVALID_PLAYBACK_REQUEST", "续播时间无效")
		return
	}
	if raw := firstQuery(r, "AudioStreamIndex", "audioStreamIndex"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, r, 400, "INVALID_PLAYBACK_REQUEST", "音轨序号无效")
			return
		}
		postBody.AudioStreamIndex = &value
	}
	if raw := firstQuery(r, "SubtitleStreamIndex", "subtitleStreamIndex"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, r, 400, "INVALID_PLAYBACK_REQUEST", "字幕序号无效")
			return
		}
		postBody.SubtitleStreamIndex = &value
	}
	directPlay, directStream := true, true
	if postBody.EnableDirectPlay != nil {
		directPlay = *postBody.EnableDirectPlay
	}
	if postBody.EnableDirectStream != nil {
		directStream = *postBody.EnableDirectStream
	}
	if raw := firstQuery(r, "EnableDirectPlay", "enableDirectPlay"); raw != "" {
		var err error
		directPlay, err = strconv.ParseBool(raw)
		if err != nil {
			writeError(w, r, 400, "INVALID_PLAYBACK_REQUEST", "直接播放选项无效")
			return
		}
	}
	if raw := firstQuery(r, "EnableDirectStream", "enableDirectStream"); raw != "" {
		var err error
		directStream, err = strconv.ParseBool(raw)
		if err != nil {
			writeError(w, r, 400, "INVALID_PLAYBACK_REQUEST", "直接流选项无效")
			return
		}
	}
	if !directPlay && !directStream {
		writeJSON(w, 200, map[string]any{"MediaSources": []any{}, "ErrorCode": "NoCompatibleStream"})
		return
	}

	sourceID := strings.TrimSpace(firstQuery(r, "MediaSourceId", "mediaSourceId", "sourceId"))
	if sourceID == "" {
		sourceID = strings.TrimSpace(postBody.MediaSourceId)
		if sourceID == "" {
			sourceID = strings.TrimSpace(postBody.SourceId)
		}
	}
	quality := strings.TrimSpace(firstQuery(r, "Quality", "quality"))
	if quality == "" {
		quality = strings.TrimSpace(postBody.Quality)
	}
	language := strings.TrimSpace(firstQuery(r, "Language", "language"))
	if language == "" {
		language = strings.TrimSpace(postBody.Language)
	}
	allowPreview := firstQuery(r, "AllowPreview", "allowPreview") == "1" || strings.EqualFold(firstQuery(r, "AllowPreview", "allowPreview"), "true")
	if !allowPreview && postBody.AllowPreview != nil {
		allowPreview = *postBody.AllowPreview
	}

	options := SelectionOptions{
		SourceID:     sourceID,
		Quality:      quality,
		Language:     language,
		AllowPreview: allowPreview,
		Refresh:      firstQuery(r, "Refresh", "refresh") == "1",
	}
	selection, err := s.selectAndResolve(r.Context(), item, principal.User, options)
	if err != nil {
		s.writeSelectionError(w, r, err)
		return
	}

	playSessionID := firstQuery(r, "PlaySessionId", "playSessionId")
	if playSessionID == "" {
		playSessionID = postBody.PlaySessionId
	}
	if playSessionID == "" {
		playSessionID = newID("play")
	}
	mediaSourcesList := s.jellyfinSources(r.Context(), item, principal.User, selection, allowPreview)
	for _, mediaSource := range mediaSourcesList {
		mediaSource["SupportsDirectPlay"] = directPlay
		mediaSource["SupportsDirectStream"] = directStream
	}
	if len(mediaSourcesList) > 0 {
		if !setRequestedTrack(mediaSourcesList[0], "Audio", postBody.AudioStreamIndex) || !setRequestedTrack(mediaSourcesList[0], "Subtitle", postBody.SubtitleStreamIndex) {
			writeError(w, r, 400, "INVALID_PLAYBACK_TRACK", "所选音轨或字幕不存在")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"MediaSources": mediaSourcesList, "PlaySessionId": playSessionID})
}

func (s *Service) authorizedItem(ctx context.Context, itemID string, user auth.User) (catalog.Item, error) {
	item, err := s.catalog.GetItemForUser(ctx, itemID, user)
	if err != nil {
		return catalog.Item{}, errors.New("作品不可见")
	}
	if item.ItemType == "series" || item.ItemType == "season" {
		return catalog.Item{}, errors.New("请选择要播放的单集")
	}
	if !s.canPlay(ctx, itemID, user) {
		return catalog.Item{}, errors.New("没有播放权限")
	}
	return item, nil
}

func (s *Service) selectAndResolve(ctx context.Context, item catalog.Item, user auth.User, options SelectionOptions) (PlaybackResolution, error) {
	source, resolved, expiresAt, err := s.selectResolvedSource(ctx, item, options)
	if err != nil {
		return PlaybackResolution{}, err
	}
	token, err := s.encodePlaybackToken(source.ID, item.ID, user.ID, resolved.URL, expiresAt)
	if err != nil {
		return PlaybackResolution{}, err
	}
	return PlaybackResolution{
		Media:         resolved.Media,
		Source:        source.view(),
		PlaybackURL:   s.playbackURL(item.ID, resolved.Protocol, token),
		Protocol:      resolved.Protocol,
		ExpiresAt:     expiresAt.Format(time.RFC3339),
		DirectPlay:    resolved.DirectPlay,
		RequiresProxy: resolved.RequiresProxy,
		IsPreview:     resolved.IsPreview,
		Subtitles:     s.subtitleTracks(source, resolved.Subtitles, item.ID, user.ID, expiresAt),
	}, nil
}

func (s *Service) subtitleTracks(source sourceRecord, subtitles []provider.Subtitle, itemID, userID string, expiresAt time.Time) []provider.Subtitle {
	tracks := make([]provider.Subtitle, 0, len(subtitles))
	for _, subtitle := range subtitles {
		rawURL := strings.TrimSpace(subtitle.URL)
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Hostname() == "" || s.validateUpstreamURL(source, rawURL) != nil {
			continue
		}
		token, err := s.encodeResourceToken(source.ID, itemID, userID, parsed.String(), expiresAt)
		if err != nil {
			continue
		}
		subtitle.URL = s.resourceURL(itemID, source.ID, token)
		tracks = append(tracks, subtitle)
	}
	return tracks
}

func (s *Service) selectResolvedSource(ctx context.Context, item catalog.Item, options SelectionOptions) (sourceRecord, provider.ResolvedStream, time.Time, error) {
	if s.modules != nil && (options.SourceID == "" || options.Refresh) {
		_ = s.discoverModuleSources(ctx, item, options.Refresh)
	}
	ctx, cancel := context.WithTimeout(ctx, resolutionBudget)
	defer cancel()
	sources, err := s.listSourceRecords(ctx, item.ID)
	if err != nil {
		return sourceRecord{}, provider.ResolvedStream{}, time.Time{}, err
	}
	if len(sources) == 0 {
		_ = s.DiscoverItemSources(ctx, item)
		sources, err = s.listSourceRecords(ctx, item.ID)
		if err != nil {
			return sourceRecord{}, provider.ResolvedStream{}, time.Time{}, err
		}
	}
	if len(sources) == 0 {
		return sourceRecord{}, provider.ResolvedStream{}, time.Time{}, errors.New("没有可用播放源")
	}
	if options.SourceID != "" {
		filtered := sources[:0]
		for _, source := range sources {
			if source.ID == options.SourceID {
				filtered = append(filtered, source)
			}
		}
		sources = filtered
		if len(sources) == 0 {
			return sourceRecord{}, provider.ResolvedStream{}, time.Time{}, sql.ErrNoRows
		}
	}
	if options.Protocol != "" {
		filtered := sources[:0]
		for _, source := range sources {
			if strings.EqualFold(source.Protocol, options.Protocol) {
				filtered = append(filtered, source)
			}
		}
		sources = filtered
	}
	if !options.AllowPreview {
		filtered := make([]sourceRecord, 0, len(sources))
		for _, source := range sources {
			if !source.IsPreview {
				filtered = append(filtered, source)
			}
		}
		sources = filtered
	}
	if len(sources) == 0 {
		return sourceRecord{}, provider.ResolvedStream{}, time.Time{}, errors.New("没有符合条件的播放源")
	}
	sort.SliceStable(sources, func(i, j int) bool { return sourceScore(sources[i], options) > sourceScore(sources[j], options) })
	var lastErr error
	var fallbackSource sourceRecord
	var fallbackResolved provider.ResolvedStream
	var fallbackExpiry time.Time
	var hasFallback bool

	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		if source.Status == "unavailable" && !options.Refresh && !s.moduleSourceEnabled(source) {
			continue
		}
		resolved, err := s.resolveStoredSource(ctx, item, source, options.Refresh)
		if err != nil {
			lastErr = err
			if !errors.Is(err, module.ErrRevisionChanged) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				_ = s.markSourceFailure(ctx, source.ID, err)
			}
			continue
		}
		if !isPlaybackProtocol(resolved.Protocol) || strings.TrimSpace(resolved.URL) == "" {
			lastErr = errors.New("Provider 返回了无效播放协议")
			_ = s.markSourceFailure(ctx, source.ID, lastErr)
			continue
		}
		expiresAt := tokenExpiry(resolved.ExpiresAt)
		if !expiresAt.After(time.Now().UTC()) {
			lastErr = errors.New("播放源已过期")
			_ = s.markSourceFailure(ctx, source.ID, lastErr)
			continue
		}
		fresh, readErr := s.getSourceRecord(ctx, source.ID)
		if readErr != nil {
			lastErr = readErr
			continue
		}
		source = fresh
		now := time.Now().UTC().Format(time.RFC3339Nano)
		source.Protocol = resolved.Protocol
		source.IsPreview = resolved.IsPreview
		source.Status = "active"
		source.ExpiresAt = resolved.ExpiresAt
		source.LastResolvedAt = now
		source.LastError = ""

		if resolved.Protocol == "hls" || resolved.Protocol == "http" || options.Protocol == "redirect" {
			return source, resolved, expiresAt, nil
		}
		if !hasFallback {
			fallbackSource = source
			fallbackResolved = resolved
			fallbackExpiry = expiresAt
			hasFallback = true
		}
	}
	if hasFallback {
		return fallbackSource, fallbackResolved, fallbackExpiry, nil
	}
	if lastErr != nil {
		return sourceRecord{}, provider.ResolvedStream{}, time.Time{}, fmt.Errorf("播放源不可用: %w", lastErr)
	}
	return sourceRecord{}, provider.ResolvedStream{}, time.Time{}, errors.New("没有可用播放源")
}

func isPlaybackProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "hls", "http", "redirect":
		return true
	default:
		return false
	}
}

func sourceScore(source sourceRecord, options SelectionOptions) int {
	score := -source.SortOrder*100 + source.ProviderPriority*10
	if strings.EqualFold(source.Quality, options.Quality) && options.Quality != "" {
		score += 10000
	}
	if strings.EqualFold(source.Language, options.Language) && options.Language != "" {
		score += 5000
	}
	if source.Protocol == "hls" {
		score += 30
	}
	if source.Protocol == "http" {
		score += 20
	}
	if source.Protocol == "redirect" {
		score -= 10000
	}
	if source.IsPreview {
		score -= 1000
	}
	if source.Status == "expired" {
		score -= 50000
	}
	if source.Status == "unavailable" {
		score -= 100000
	}
	return score
}

func firstQuery(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := r.URL.Query().Get(name); value != "" {
			return value
		}
	}
	return ""
}

func nullableInt64Ticks(seconds *int) any {
	if seconds == nil {
		return nil
	}
	return int64(*seconds) * 10_000_000
}

func (s *Service) resolveStoredSource(ctx context.Context, item catalog.Item, source sourceRecord, forceRefresh bool) (provider.ResolvedStream, error) {
	return s.resolveModuleSource(ctx, item, source, forceRefresh)
}

func (s *Service) persistResolved(ctx context.Context, sourceID string, candidate provider.Candidate, resolved provider.ResolvedStream) error {
	candidateURL := strings.TrimSpace(candidate.URL)
	if candidateURL == "" {
		candidateURL = strings.TrimSpace(resolved.URL)
	}
	candidateEncrypted, err := s.encrypt(candidateURL)
	if err != nil {
		return errors.New("保存播放源失败")
	}
	resolvedEncrypted, err := s.encrypt(strings.TrimSpace(resolved.URL))
	if err != nil {
		return errors.New("保存播放源失败")
	}
	headerJSON, err := json.Marshal(resolved.Headers)
	if err != nil {
		return errors.New("保存播放源失败")
	}
	headerEncrypted, err := s.encrypt(string(headerJSON))
	if err != nil {
		return errors.New("保存播放源失败")
	}
	var duration *int
	var size *int64
	if resolved.Media != nil {
		duration, size = resolved.Media.DurationSeconds, resolved.Media.SizeBytes
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE stream_sources SET protocol = ?, candidate_url_encrypted = ?, resolved_url_encrypted = ?, resolved_headers_encrypted = ?, is_preview = ?, status = 'active', last_resolved_at = ?, expires_at = ?, last_error = NULL, duration_seconds=COALESCE(?,duration_seconds), size_bytes=COALESCE(?,size_bytes), updated_at = ? WHERE id = ?"), resolved.Protocol, candidateEncrypted, resolvedEncrypted, headerEncrypted, boolInt(resolved.IsPreview), now, nullable(resolved.ExpiresAt), duration, size, now, sourceID)
	if err != nil {
		return errors.New("保存播放源失败")
	}
	return nil
}

func (s *Service) markSourceFailure(ctx context.Context, sourceID string, _ error) error {
	_, err := s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE stream_sources SET status = 'unavailable', last_error = ?, updated_at = ? WHERE id = ?"), "播放源刷新失败", time.Now().UTC().Format(time.RFC3339Nano), sourceID)
	return err
}

func (s *Service) listSourceRecords(ctx context.Context, itemID string) ([]sourceRecord, error) {
	query := sourceSelectColumns + " FROM stream_sources ps LEFT JOIN online_providers op ON op.id = ps.provider_id WHERE ps.item_id = ? AND (ps.provider_id IS NULL OR op.enabled = 1) AND ps.enabled = 1 ORDER BY ps.sort_order, COALESCE(op.priority, ps.priority) DESC, ps.id"
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(query), itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]sourceRecord, 0)
	for rows.Next() {
		source, scanErr := scanSource(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	available := make([]sourceRecord, 0, len(sources))
	for _, source := range sources {
		if s.moduleSourceEnabled(source) {
			available = append(available, s.describeSource(source))
		}
	}
	return available, nil
}

func tokenExpiry(sourceExpiresAt string) time.Time {
	expires := time.Now().UTC().Add(playbackTokenTTL)
	if sourceExpiresAt != "" {
		if sourceExpires, err := time.Parse(time.RFC3339, sourceExpiresAt); err == nil && sourceExpires.Before(expires) {
			expires = sourceExpires
		}
	}
	return expires
}

func (s *Service) playbackURL(itemID, protocol, token string) string {
	path := "/Videos/" + url.PathEscape(itemID) + "/stream"
	if protocol == "hls" {
		path = "/Videos/" + url.PathEscape(itemID) + "/master.m3u8"
	}
	return path + "?token=" + url.QueryEscape(token)
}

func (s *Service) resourceURL(itemID, sourceID, token string) string {
	return "/Videos/" + url.PathEscape(itemID) + "/stream?sourceId=" + url.QueryEscape(sourceID) + "&resource=" + url.QueryEscape(token)
}

func (s *Service) writeSelectionError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadGateway
	code := "PLAYBACK_SOURCE_UNAVAILABLE"
	message := "当前没有可用播放源"
	if errors.Is(err, sql.ErrNoRows) {
		status = http.StatusNotFound
		code = "PLAYBACK_SOURCE_NOT_FOUND"
		message = "播放源不存在"
	} else if strings.Contains(err.Error(), "作品不可见") {
		status = http.StatusForbidden
		code = "ITEM_NOT_VISIBLE"
		message = "作品不可见"
	} else if strings.Contains(err.Error(), "播放权限") {
		status = http.StatusForbidden
		code = "PLAYBACK_FORBIDDEN"
		message = "没有播放权限"
	} else if strings.Contains(err.Error(), "需要登录") {
		status = http.StatusUnauthorized
		code = "AUTH_REQUIRED"
		message = "请先登录"
	}
	writeError(w, r, status, code, message)
}

func (s *Service) stream(w http.ResponseWriter, r *http.Request) {
	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	_ = resource
	source, _, user, target, _, err := s.authorizedPlaybackSource(r, SelectionOptions{
		SourceID:     strings.TrimSpace(r.URL.Query().Get("sourceId")),
		Quality:      strings.TrimSpace(r.URL.Query().Get("quality")),
		Language:     strings.TrimSpace(r.URL.Query().Get("language")),
		AllowPreview: r.URL.Query().Get("allowPreview") == "1",
		Refresh:      r.URL.Query().Get("refresh") == "1",
	})
	if err != nil {
		s.sourceError(w, r, err)
		return
	}
	s.proxyResource(w, r, source, target, false, user.ID)
}

func (s *Service) masterPlaylist(w http.ResponseWriter, r *http.Request) {
	source, item, user, target, _, err := s.authorizedPlaybackSource(r, SelectionOptions{
		SourceID:     strings.TrimSpace(r.URL.Query().Get("sourceId")),
		Protocol:     "hls",
		AllowPreview: r.URL.Query().Get("allowPreview") == "1",
		Refresh:      r.URL.Query().Get("refresh") == "1",
	})
	if err != nil {
		s.sourceError(w, r, err)
		return
	}
	if source.Protocol != "hls" {
		s.sourceError(w, r, errors.New("播放源不是 HLS 协议"))
		return
	}
	_ = item
	s.proxyResource(w, r, source, target, true, user.ID)
}

func (s *Service) authorizedPlaybackSource(r *http.Request, options SelectionOptions) (sourceRecord, catalog.Item, auth.User, string, time.Time, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	itemID := unescapeParam(r, "itemID")

	playbackToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if playbackToken != "" {
		bound, tokenErr := s.decodeBoundToken(playbackToken, playbackTokenVersion)
		if tokenErr == nil && bound.ItemID == itemID {
			if ok && principal.User.ID != bound.UserID {
				return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, errors.New("没有此播放链接的权限")
			}
			user, userErr := s.auth.GetUser(r.Context(), bound.UserID)
			if userErr == nil {
				item, itemErr := s.authorizedItem(r.Context(), itemID, user)
				if itemErr == nil {
					source, sourceErr := s.sourceForItem(r.Context(), itemID, bound.SourceID)
					if sourceErr == nil && source.ModuleID == bound.ModuleID {
						target := bound.Target
						expiresAt := bound.ExpiresAt
						if target == "" {
							_, resolved, exp, selErr := s.selectResolvedSource(r.Context(), item, SelectionOptions{SourceID: bound.SourceID})
							if selErr != nil {
								return sourceRecord{}, catalog.Item{}, user, "", time.Time{}, selErr
							}
							source, sourceErr = s.sourceForItem(r.Context(), itemID, bound.SourceID)
							if sourceErr != nil {
								return sourceRecord{}, catalog.Item{}, user, "", time.Time{}, sourceErr
							}
							target = resolved.URL
							expiresAt = exp
						}
						return source, item, user, target, expiresAt, nil
					}
				}
			}
		}
		return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, errors.New("播放令牌无效或已过期")
	}

	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	if resource != "" {
		sourceID := strings.TrimSpace(r.URL.Query().Get("sourceId"))
		if sourceID == "" {
			return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, errors.New("播放资源令牌无效")
		}
		bound, tokenErr := s.decodeBoundToken(resource, resourceTokenVersion)
		if tokenErr == nil && bound.ItemID == itemID && bound.SourceID == sourceID {
			if ok && principal.User.ID != bound.UserID {
				return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, errors.New("没有此播放链接的权限")
			}
			user, userErr := s.auth.GetUser(r.Context(), bound.UserID)
			if userErr == nil {
				item, itemErr := s.authorizedItem(r.Context(), itemID, user)
				if itemErr == nil {
					source, sourceErr := s.sourceForItem(r.Context(), itemID, sourceID)
					if sourceErr == nil && source.ModuleID == bound.ModuleID {
						return source, item, user, bound.Target, bound.ExpiresAt, nil
					}
				}
			}
		}
		return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, errors.New("播放资源令牌无效或已过期")
	}

	if !ok {
		return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, errors.New("需要登录")
	}
	item, err := s.authorizedItem(r.Context(), itemID, principal.User)
	if err != nil {
		return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, err
	}
	options.SourceID = strings.TrimSpace(r.URL.Query().Get("sourceId"))
	if options.Quality == "" {
		options.Quality = strings.TrimSpace(r.URL.Query().Get("quality"))
	}
	if options.Language == "" {
		options.Language = strings.TrimSpace(r.URL.Query().Get("language"))
	}
	if !options.AllowPreview {
		options.AllowPreview = r.URL.Query().Get("allowPreview") == "1"
	}
	options.Refresh = options.Refresh || r.URL.Query().Get("refresh") == "1"
	source, resolved, expiresAt, selectErr := s.selectResolvedSource(r.Context(), item, options)
	if selectErr != nil {
		return sourceRecord{}, catalog.Item{}, auth.User{}, "", time.Time{}, selectErr
	}
	return source, item, principal.User, resolved.URL, expiresAt, nil
}

func (s *Service) proxyResource(w http.ResponseWriter, r *http.Request, source sourceRecord, target string, forcePlaylist bool, userID string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")

	response, err := s.fetchStream(r.Context(), r, source, target)
	if err != nil {
		s.sourceError(w, r, err)
		return
	}
	defer response.Body.Close()
	contentType := response.Header.Get("Content-Type")
	playlistURL := target
	if response.Request != nil && response.Request.URL != nil {
		playlistURL = response.Request.URL.String()
	}
	isPlaylist := source.Protocol == "hls" && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && (forcePlaylist || strings.Contains(strings.ToLower(contentType), "mpegurl") || strings.HasSuffix(strings.ToLower(strings.Split(playlistURL, "?")[0]), ".m3u8"))
	if isPlaylist && r.Method != http.MethodHead {
		body, readErr := readPlaylist(response.Body)
		if readErr != nil {
			s.sourceError(w, r, readErr)
			return
		}
		rewritten, rewriteErr := s.rewritePlaylist(body, playlistURL, source, unescapeParam(r, "itemID"), userID)
		if rewriteErr != nil {
			s.sourceError(w, r, rewriteErr)
			return
		}
		if contentType == "" {
			contentType = "application/vnd.apple.mpegurl"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Del("Content-Length")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(rewritten)
		return
	}
	if response.StatusCode >= http.StatusBadRequest {
		copyStreamHeaders(w.Header(), response.Header)
		w.Header().Del("Content-Length")
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		return
	}
	copyStreamHeaders(w.Header(), response.Header)
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, response.Body)
	}
}

func (s *Service) fetchStream(ctx context.Context, r *http.Request, source sourceRecord, target string) (*http.Response, error) {
	return s.fetchModuleStream(ctx, r, source, target)
}

func (s *Service) rewritePlaylist(body []byte, playlistURL string, source sourceRecord, itemID, userID string) ([]byte, error) {
	base, err := url.Parse(playlistURL)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			rewritten, rewriteErr := rewriteURIAttribute(line, func(raw string) (string, error) { return s.playlistURL(raw, base, source, itemID, userID) })
			if rewriteErr != nil {
				return nil, rewriteErr
			}
			lines[index] = rewritten
			continue
		}
		rewritten, rewriteErr := s.playlistURL(trimmed, base, source, itemID, userID)
		if rewriteErr != nil {
			return nil, rewriteErr
		}
		lines[index] = rewritten
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func (s *Service) playlistURL(raw string, base *url.URL, source sourceRecord, itemID, userID string) (string, error) {
	resolved, err := url.Parse(strings.Trim(strings.TrimSpace(raw), "\""))
	if err != nil {
		return "", errors.New("HLS 资源地址无效")
	}
	if resolved.Scheme == "" {
		resolved = base.ResolveReference(resolved)
	}
	if resolved.Hostname() == "" || s.validateUpstreamURL(source, resolved.String()) != nil {
		return "", errors.New("HLS 资源地址未通过校验")
	}
	token, err := s.encodeResourceToken(source.ID, itemID, userID, resolved.String(), tokenExpiry(source.ExpiresAt))
	if err != nil {
		return "", err
	}
	return "/Videos/" + url.PathEscape(itemID) + "/stream?sourceId=" + url.QueryEscape(source.ID) + "&resource=" + url.QueryEscape(token), nil
}

func rewriteURIAttribute(line string, rewrite func(string) (string, error)) (string, error) {
	for _, key := range []string{"URI=\"", "URI='"} {
		start := strings.Index(line, key)
		if start < 0 {
			continue
		}
		valueStart := start + len(key)
		end := strings.IndexAny(line[valueStart:], "\"'")
		if end < 0 {
			return "", errors.New("HLS URI 属性无效")
		}
		valueEnd := valueStart + end
		rewritten, err := rewrite(line[valueStart:valueEnd])
		if err != nil {
			return "", err
		}
		return line[:valueStart] + rewritten + line[valueEnd:], nil
	}
	return line, nil
}

func (s *Service) encodePlaybackToken(sourceID, itemID, userID, target string, expiresAt time.Time) (string, error) {
	return s.encodeBoundToken(playbackTokenVersion, sourceID, itemID, userID, target, expiresAt)
}

func (s *Service) decodePlaybackToken(token, itemID, userID string) (boundPlaybackToken, error) {
	bound, err := s.decodeBoundToken(token, playbackTokenVersion)
	if err != nil || bound.ItemID != itemID || bound.UserID != userID {
		return boundPlaybackToken{}, errors.New("播放令牌无效或已过期")
	}
	return bound, nil
}

func (s *Service) encodeResourceToken(sourceID, itemID, userID, target string, expiresAt time.Time) (string, error) {
	return s.encodeBoundToken(resourceTokenVersion, sourceID, itemID, userID, target, expiresAt)
}

func (s *Service) decodeResourceToken(token, sourceID, itemID, userID string) (string, time.Time, error) {
	bound, err := s.decodeBoundToken(token, resourceTokenVersion)
	if err != nil || bound.SourceID != sourceID || bound.ItemID != itemID || bound.UserID != userID {
		return "", time.Time{}, errors.New("播放资源令牌无效或已过期")
	}
	return bound.Target, bound.ExpiresAt, nil
}

func (s *Service) encodeBoundToken(version, sourceID, itemID, userID, target string, expiresAt time.Time) (string, error) {
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(itemID) == "" || strings.TrimSpace(userID) == "" || !expiresAt.After(time.Now().UTC()) || strings.ContainsAny(target, "\r\n") {
		return "", errors.New("播放令牌参数无效")
	}
	source, err := s.getSourceRecord(context.Background(), sourceID)
	if err != nil || source.ItemID != itemID {
		return "", errors.New("播放令牌来源无效")
	}
	entry, err := s.sourceModule(source)
	if err != nil {
		return "", err
	}
	ref, err := s.moduleReference(catalog.Item{ID: itemID}, source)
	if err != nil || ref.Revision != "" && ref.Revision != entry.Revision {
		return "", module.ErrRevisionChanged
	}
	plain := strings.Join([]string{version, sourceID, itemID, userID, expiresAt.UTC().Format(time.RFC3339Nano), source.ModuleID, entry.Revision, target}, "\n")
	return s.encryptToken(plain)
}

func (s *Service) decodeBoundToken(token, expectedVersion string) (boundPlaybackToken, error) {
	plain, err := s.decryptToken(token)
	if err != nil {
		return boundPlaybackToken{}, errors.New("播放令牌无效或已过期")
	}
	parts := strings.SplitN(plain, "\n", 8)
	if len(parts) != 8 || parts[0] != expectedVersion || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return boundPlaybackToken{}, errors.New("播放令牌无效或已过期")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, parts[4])
	if err != nil || !expiresAt.After(time.Now().UTC()) {
		return boundPlaybackToken{}, errors.New("播放令牌无效或已过期")
	}
	if s.modules == nil {
		return boundPlaybackToken{}, errors.New("播放令牌模块无效")
	}
	entry, ok := s.modules.Get(parts[5])
	if !ok || entry.Status != module.StatusActive || parts[6] == "" || entry.Revision != parts[6] {
		return boundPlaybackToken{}, errors.New("播放令牌模块已变化，请重新选择播放源")
	}
	return boundPlaybackToken{SourceID: parts[1], ItemID: parts[2], UserID: parts[3], ExpiresAt: expiresAt, ModuleID: parts[5], Revision: parts[6], Target: parts[7]}, nil
}

func (s *Service) sourceURL(source sourceRecord) (string, error) {
	value, err := s.decrypt(source.ResolvedEncrypted)
	if err != nil || s.validateUpstreamURL(source, value) != nil {
		return "", errors.New("播放源地址无效")
	}
	return value, nil
}

func (s *Service) sourceHeaders(source sourceRecord) (map[string]string, error) {
	if strings.TrimSpace(source.HeadersEncrypted) == "" {
		return map[string]string{}, nil
	}
	encoded, err := s.decrypt(source.HeadersEncrypted)
	if err != nil {
		return nil, errors.New("播放源请求头无效")
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(encoded), &headers); err != nil {
		return nil, errors.New("播放源请求头无效")
	}
	if err := validateHeaders(headers); err != nil {
		return nil, errors.New("播放源请求头无效")
	}
	return headers, nil
}

func (s *Service) sourceForItem(ctx context.Context, itemID, sourceID string) (sourceRecord, error) {
	query := sourceSelectColumns + " FROM stream_sources ps LEFT JOIN online_providers op ON op.id = ps.provider_id WHERE ps.item_id = ? AND (ps.provider_id IS NULL OR op.enabled = 1) AND ps.enabled = 1"
	args := []any{itemID}
	if sourceID != "" {
		query += " AND ps.id = ?"
		args = append(args, sourceID)
	}
	query += " ORDER BY ps.sort_order, COALESCE(op.priority, ps.priority) DESC, ps.id"
	row := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), args...)
	source, err := scanSource(row)
	if err == nil && !s.moduleSourceEnabled(source) {
		return sourceRecord{}, errors.New("播放源模块已停用")
	}
	return s.describeSource(source), err
}

func (s *Service) getSourceRecord(ctx context.Context, id string) (sourceRecord, error) {
	query := sourceSelectColumns + " FROM stream_sources ps LEFT JOIN online_providers op ON op.id = ps.provider_id WHERE ps.id = ?"
	source, err := scanSource(s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), id))
	return s.describeSource(source), err
}

func scanSource(scanner interface{ Scan(...any) error }) (sourceRecord, error) {
	var source sourceRecord
	var preview int
	var enabled int
	var hostsJSON string
	var providerType string
	if err := scanner.Scan(&source.ID, &source.ItemID, &source.ModuleID, &source.ProviderID, &providerType, &source.ProviderName, &source.ProviderGroup, &source.ProviderPriority, &source.Label, &source.Protocol, &source.Quality, &source.Language, &source.CandidateEncrypted, &source.ResolvedEncrypted, &source.HeadersEncrypted, &source.DurationSeconds, &source.SizeBytes, &preview, &source.Status, &enabled, &source.ExpiresAt, &source.LastResolvedAt, &source.LastError, &source.LastProbeStatus, &source.LastProbeAt, &source.LastProbeDurationMS, &source.LastProbeError, &source.ProbeCount, &source.ProbeSuccessCount, &source.ProbeDurationTotalMS, &source.SortOrder, &source.CreatedAt, &source.UpdatedAt, &hostsJSON); err != nil {
		return sourceRecord{}, err
	}
	source.ProviderType = provider.ProviderType(providerType)
	source.IsPreview = preview == 1
	source.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(hostsJSON), &source.AllowedHosts); err != nil {
		return sourceRecord{}, err
	}
	return source, nil
}

func (source sourceRecord) view() SourceView {
	average := int64(0)
	if source.ProbeCount > 0 {
		average = source.ProbeDurationTotalMS / source.ProbeCount
	}
	return SourceView{ID: source.ID, ItemID: source.ItemID, ModuleID: source.ModuleID, ModuleName: source.ModuleName, ProviderID: source.ProviderID, ProviderType: source.ProviderType, ProviderName: source.ProviderName, ProviderGroup: source.ProviderGroup, Label: source.Label, Protocol: source.Protocol, Quality: source.Quality, Language: source.Language, DurationSeconds: source.DurationSeconds, SizeBytes: source.SizeBytes, IsPreview: source.IsPreview, Status: source.Status, Enabled: source.Enabled, ExpiresAt: source.ExpiresAt, LastResolvedAt: source.LastResolvedAt, LastError: source.LastError, LastProbeStatus: source.LastProbeStatus, LastProbeAt: source.LastProbeAt, LastProbeDurationMS: source.LastProbeDurationMS, LastProbeError: source.LastProbeError, ProbeCount: source.ProbeCount, ProbeSuccessCount: source.ProbeSuccessCount, AverageProbeDurationMS: average, SortOrder: source.SortOrder, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func (s *Service) providerHosts(ctx context.Context, providerID string) ([]string, error) {
	return s.providerHostsWithState(ctx, providerID, true)
}

func (s *Service) providerHostsForAdmin(ctx context.Context, providerID string) ([]string, error) {
	return s.providerHostsWithState(ctx, providerID, false)
}

func (s *Service) providerHostsWithState(ctx context.Context, providerID string, enabledOnly bool) ([]string, error) {
	var hostsJSON string
	query := "SELECT allowed_hosts_json FROM online_providers WHERE id = ?"
	if enabledOnly {
		query += " AND enabled = 1"
	}
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), providerID).Scan(&hostsJSON)
	if err != nil {
		return nil, err
	}
	var hosts []string
	if err := json.Unmarshal([]byte(hostsJSON), &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

func (s *Service) itemForAdmin(ctx context.Context, itemID string) (provider.Item, error) {
	var item provider.Item
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, code, title, source_url FROM media_items WHERE id = ? AND deleted_at IS NULL"), itemID).Scan(&item.ID, &item.Code, &item.Title, &item.SourceURL)
	return item, err
}

func (s *Service) audit(ctx context.Context, actor, action, resource, resourceID, requestID string, metadata map[string]any) {
	encoded, _ := json.Marshal(metadata)
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, 'success', ?, ?)"), newID("aud"), actor, action, resource, resourceID, requestID, string(encoded), time.Now().UTC().Format(time.RFC3339Nano))
}

func (s *Service) itemExists(ctx context.Context, itemID string) (string, error) {
	var id string
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id FROM media_items WHERE id = ? AND deleted_at IS NULL"), itemID).Scan(&id)
	return id, err
}

func (s *Service) canPlay(ctx context.Context, itemID string, user auth.User) bool {
	return auth.CanAccessMedia(ctx, s.db, user, itemID, true)
}

func (s *Service) encrypt(value string) (string, error) { return s.encryptToken(value) }
func (s *Service) decrypt(value string) (string, error) { return s.decryptToken(value) }

func (s *Service) encryptToken(value string) (string, error) {
	block, err := aes.NewCipher(s.masterKey)
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
	ciphertext := gcm.Seal(nil, nonce, []byte(value), nil)
	encoded := append(nonce, ciphertext...)
	return "v1:" + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (s *Service) decryptToken(value string) (string, error) {
	version, encoded, ok := strings.Cut(value, ":")
	if !ok || version != "v1" {
		return "", errors.New("密文版本无效")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(data) < gcm.NonceSize() {
		return "", errors.New("密文无效")
	}
	plaintext, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("密文无效")
	}
	return string(plaintext), nil
}

func validateHeaders(headers map[string]string) error {
	if len(headers) > 12 {
		return errors.New("请求头数量不能超过 12 个")
	}
	for name, value := range headers {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "\r\n") || strings.EqualFold(name, "Proxy-Authorization") || isHopByHopHeader(name) {
			return errors.New("请求头名称不允许为空、换行、代理认证或连接控制字段")
		}
		if len(name) > 80 || len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
			return errors.New("请求头长度或内容无效")
		}
	}
	return nil
}

func validateRange(value string) error {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return errors.New("只支持单个 bytes Range")
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "bytes="), "-", 2)
	if len(parts) != 2 {
		return errors.New("Range 格式无效")
	}
	startValue, endValue := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if startValue == "" && endValue == "" {
		return errors.New("Range 格式无效")
	}
	if startValue == "" {
		suffix, err := strconv.ParseInt(endValue, 10, 64)
		if err != nil || suffix <= 0 || suffix > maxRangeBytes {
			return errors.New("Range 范围过大或无效")
		}
		return nil
	}
	start, err := strconv.ParseInt(startValue, 10, 64)
	if err != nil || start < 0 {
		return errors.New("Range 起点无效")
	}
	if endValue == "" {
		return nil
	}
	end, err := strconv.ParseInt(endValue, 10, 64)
	if err != nil || end < start || end-start+1 > maxRangeBytes {
		return errors.New("Range 范围过大或无效")
	}
	return nil
}

func (s *Service) sourceError(w http.ResponseWriter, r *http.Request, err error) {
	message := err.Error()
	status := http.StatusBadGateway
	code := "PLAYBACK_FAILED"
	tokenError := strings.Contains(message, "令牌")
	if strings.Contains(message, "需要登录") {
		status = http.StatusUnauthorized
	}
	if tokenError {
		status = http.StatusUnauthorized
		code = "PLAYBACK_TOKEN_INVALID"
	}
	if strings.Contains(message, "没有") || strings.Contains(message, "权限") || strings.Contains(message, "不可见") {
		status = http.StatusForbidden
	}
	if strings.Contains(message, "无效") || strings.Contains(message, "不是 HLS") || strings.Contains(message, "过期") {
		status = http.StatusBadRequest
	}
	if strings.Contains(message, "Range") {
		status = http.StatusRequestedRangeNotSatisfiable
	}
	if tokenError {
		status = http.StatusUnauthorized
	}
	if errors.Is(err, sql.ErrNoRows) {
		status = http.StatusNotFound
		message = "播放源不存在"
	}
	writeError(w, r, status, code, message)
}

func copyStreamHeaders(dst, src http.Header) {
	for _, name := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}

func readPlaylist(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxPlaylistBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPlaylistBytes {
		return nil, errors.New("HLS 播放列表过大")
	}
	return data, nil
}

func isHopByHopHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "connection", "content-length", "host", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": middleware.GetReqID(r.Context())}})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func newID(prefix string) string { return prefix + "_" + fmt.Sprintf("%x", time.Now().UnixNano()) }
func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Service) jellyfinSources(ctx context.Context, item catalog.Item, user auth.User, selection PlaybackResolution, allowPreview bool) []map[string]any {
	mediaSource := jellyfinMediaSource(selection.Source, selection.Protocol, selection.PlaybackURL, selection.Media, selection.Subtitles)
	mediaSourcesList := []map[string]any{mediaSource}
	allSources, _ := s.listSourceRecords(ctx, item.ID)
	for _, alt := range allSources {
		if alt.ID == selection.Source.ID || !alt.Enabled || alt.Status != "active" || alt.IsPreview && !allowPreview || (alt.Protocol != "hls" && alt.Protocol != "http") || !s.moduleSourceEnabled(alt) {
			continue
		}
		ref, err := s.moduleReference(item, alt)
		if err != nil {
			continue
		}
		entry, err := s.sourceModule(alt)
		if err != nil || ref.Revision != entry.Revision {
			continue
		}
		altToken, err := s.encodePlaybackToken(alt.ID, item.ID, user.ID, "", tokenExpiry(alt.ExpiresAt))
		if err != nil {
			continue
		}
		altURL := s.playbackURL(item.ID, alt.Protocol, altToken)
		upstreamSubs := moduleResolution(module.ResolvedStream{Subtitles: ref.Subtitles}, alt.IsPreview).Subtitles
		altMedia := jellyfinMediaSource(alt.view(), alt.Protocol, altURL, ref.Media, s.subtitleTracks(alt, upstreamSubs, item.ID, user.ID, tokenExpiry(alt.ExpiresAt)))
		mediaSourcesList = append(mediaSourcesList, altMedia)
	}

	return mediaSourcesList
}

func (s *Service) JellyfinSources(ctx context.Context, item catalog.Item, user auth.User) ([]map[string]any, error) {
	if _, err := s.authorizedItem(ctx, item.ID, user); err != nil {
		return nil, err
	}
	selection, err := s.selectAndResolve(ctx, item, user, SelectionOptions{})
	if err != nil {
		return nil, err
	}
	return s.jellyfinSources(ctx, item, user, selection, false), nil
}
