package module

import (
	"context"
	"github.com/graysui/JMlab/internal/stream"
	"time"
)

// ModuleType 标识模块的职能类型
type ModuleType string

const (
	TypeMetadata  ModuleType = "metadata"  // 元数据提供模块
	TypeStream    ModuleType = "stream"    // 媒体流提供模块（如 内置预告片, WebDAV, 在线解析）
	TypeComposite ModuleType = "composite" // 复合模块（同时提供元数据与流）
)

// ItemType 统一通用媒体项类型
type ItemType string

const (
	ItemMovie   ItemType = "movie"
	ItemSeries  ItemType = "series"
	ItemSeason  ItemType = "season"
	ItemEpisode ItemType = "episode"
	ItemVideo   ItemType = "video"
)

// EntityRole 关联实体角色
type EntityRole string

const (
	RoleActor    EntityRole = "actor"
	RoleDirector EntityRole = "director"
	RoleWriter   EntityRole = "writer"
	RoleStudio   EntityRole = "studio"
	RoleGenre    EntityRole = "genre"
	RoleTag      EntityRole = "tag"
)

// EntityRelation 实体关联（演职员、片商、流派等）
type EntityRelation struct {
	SourceID     string            `json:"sourceId,omitempty"`
	ProviderIDs  map[string]string `json:"providerIds,omitempty"`
	Name         string            `json:"name"`
	OriginalName string            `json:"originalName,omitempty"`
	Role         EntityRole        `json:"role"`
	AvatarURL    string            `json:"avatarUrl,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// MediaImage 媒体图片（海报、背景、剧照）
type MediaImage struct {
	Label    string `json:"label,omitempty"`
	Type     string `json:"type"` // poster, backdrop, gallery, preview, avatar
	URL      string `json:"url"`
	Priority int    `json:"priority,omitempty"`
}

// MediaSummary 搜索或列表检索时的简要媒体信息
type MediaSummary struct {
	SourceURL     string            `json:"sourceUrl,omitempty"`
	Rank          int               `json:"rank,omitempty"`
	SourceID      string            `json:"sourceId"`
	Title         string            `json:"title"`
	OriginalTitle string            `json:"originalTitle,omitempty"`
	ReleaseDate   string            `json:"releaseDate,omitempty"`
	PosterURL     string            `json:"posterUrl,omitempty"`
	ItemType      ItemType          `json:"itemType"`
	ProviderIDs   map[string]string `json:"providerIds,omitempty"` // {"catalog": "123"}
}

// MediaDetail 媒体完整详情
type MediaDetail struct {
	SourceURL       string            `json:"sourceUrl,omitempty"`
	SourceID        string            `json:"sourceId"`
	ItemType        ItemType          `json:"itemType"`
	Title           string            `json:"title"`
	OriginalTitle   string            `json:"originalTitle,omitempty"`
	Overview        string            `json:"overview,omitempty"`
	ReleaseDate     string            `json:"releaseDate,omitempty"`
	ProductionYear  *int              `json:"productionYear,omitempty"`
	RuntimeSeconds  *int              `json:"runtimeSeconds,omitempty"`
	Rating          *float64          `json:"rating,omitempty"`
	RatingCount     int               `json:"ratingCount,omitempty"`
	IsAdult         bool              `json:"isAdult"`
	Entities        []EntityRelation  `json:"entities,omitempty"`
	Images          []MediaImage      `json:"images,omitempty"`
	ProviderIDs     map[string]string `json:"providerIds,omitempty"`
	ExtraAttributes map[string]any    `json:"extraAttributes,omitempty"`
}

// RankingSnapshot 榜单或发现页快照
type RankingSnapshot struct {
	RankingType string         `json:"rankingType"`
	Period      string         `json:"period"`
	ObservedAt  string         `json:"observedAt"`
	Items       []MediaSummary `json:"items"`
}

// MetadataCaps 模块能力声明
type MetadataCaps struct {
	SupportsStreams       bool       `json:"supportsStreams,omitempty"`
	SupportedTypes        []ItemType `json:"supportedTypes"`
	SupportsSearch        bool       `json:"supportsSearch"`
	SupportsRankings      bool       `json:"supportsRankings"`
	AvailableRankingTypes []string   `json:"availableRankingTypes,omitempty"`
}

// MetadataProvider 元数据模块通用接口契约
type MetadataProvider interface {
	ModuleID() string
	Caps(ctx context.Context) (MetadataCaps, error)
	Search(ctx context.Context, query string, itemType ItemType, page, pageSize int) ([]MediaSummary, int, error)
	GetDetail(ctx context.Context, sourceID string, providerIDs map[string]string) (MediaDetail, error)
	GetRankings(ctx context.Context, rankingType, period string, page int) (RankingSnapshot, error)
}

type MediaDescription = stream.MediaDescription
type MediaTrack = stream.MediaTrack

// StreamCandidate 媒体候选源信息
type StreamCandidate struct {
	Language         string            `json:"language,omitempty"`
	Media            *MediaDescription `json:"media,omitempty"`
	CandidateID      string            `json:"candidateId"`
	Label            string            `json:"label"`
	Protocol         string            `json:"protocol"` // hls, http, direct, redirect
	Quality          string            `json:"quality,omitempty"`
	IsPreview        bool              `json:"isPreview"`
	EncryptedPayload string            `json:"encryptedPayload,omitempty"`
}

// SubtitleTrack 外挂字幕
type SubtitleTrack struct {
	URL      string `json:"url"`
	Language string `json:"language"`
	Label    string `json:"label"`
	Format   string `json:"format,omitempty"` // vtt, srt, ass, ssa; unknown omitted
}

// ResolvedStream 解析后的流媒体地址与参数
type ResolvedStream struct {
	Media           *MediaDescription `json:"media,omitempty"`
	StreamURL       string            `json:"streamUrl"`
	Protocol        string            `json:"protocol"` // hls, http, direct, redirect
	RequiredHeaders map[string]string `json:"requiredHeaders,omitempty"`
	ExpiresAt       *time.Time        `json:"expiresAt,omitempty"`
	DirectPlay      bool              `json:"directPlay"`
	RequiresProxy   bool              `json:"requiresProxy"`
	Subtitles       []SubtitleTrack   `json:"subtitles,omitempty"`
}

// HealthResult 播放源健康探测结果
type HealthResult struct {
	Healthy    bool   `json:"healthy"`
	StatusCode int    `json:"statusCode,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// StreamProvider 播放源模块通用接口契约
type StreamProvider interface {
	ModuleID() string
	DiscoverSources(ctx context.Context, itemID, title string, itemType ItemType, providerIDs map[string]string, extra map[string]any) ([]StreamCandidate, error)
	ResolveStream(ctx context.Context, candidateID, encryptedPayload string, clientHeaders map[string]string) (ResolvedStream, error)
	ProbeSource(ctx context.Context, candidateID, encryptedPayload string) (HealthResult, error)
}

// Event 通用系统事件结构体
type Event struct {
	Topic       string    `json:"topic"`
	EventID     string    `json:"eventId"`
	Timestamp   time.Time `json:"timestamp"`
	PayloadJSON string    `json:"payloadJson"`
}

// EventSubscription 事件订阅处理器函数
type EventHandler func(ctx context.Context, event Event) error
