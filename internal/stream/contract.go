package stream

import "context"

// Item is the minimum catalog context a Provider needs to find a source.
type Item struct {
	ID        string `json:"id,omitempty"`
	Code      string `json:"code"`
	Title     string `json:"title"`
	SourceURL string `json:"sourceUrl"`
}

// Candidate is an untrusted source discovered by a Provider. It is never
// considered playable until its URL has passed the Provider's allowlist.
type Candidate struct {
	ID        string `json:"id,omitempty"`
	Label     string `json:"label,omitempty"`
	URL       string `json:"url"`
	Protocol  string `json:"protocol"`
	Quality   string `json:"quality,omitempty"`
	Language  string `json:"language,omitempty"`
	IsPreview bool   `json:"isPreview"`
}

type MatchResult struct {
	Matched   bool      `json:"matched"`
	Score     int       `json:"score"`
	Candidate Candidate `json:"candidate"`
}

type Source struct {
	ID         string    `json:"id,omitempty"`
	ItemID     string    `json:"itemId,omitempty"`
	ProviderID string    `json:"providerId,omitempty"`
	Item       Item      `json:"item"`
	Candidate  Candidate `json:"candidate"`
}

type ResolveRequest struct {
	Item         Item   `json:"item"`
	CandidateURL string `json:"candidateUrl,omitempty"`
}

type Subtitle struct {
	Format   string `json:"format,omitempty"`
	URL      string `json:"url"`
	Language string `json:"language,omitempty"`
	Label    string `json:"label,omitempty"`
}

type ResolvedStream struct {
	Media         *MediaDescription `json:"media,omitempty"`
	URL           string            `json:"url"`
	Protocol      string            `json:"protocol"`
	Headers       map[string]string `json:"headers,omitempty"`
	ExpiresAt     string            `json:"expiresAt,omitempty"`
	DirectPlay    bool              `json:"directPlay"`
	RequiresProxy bool              `json:"requiresProxy"`
	IsPreview     bool              `json:"isPreview"`
	Subtitles     []Subtitle        `json:"subtitles,omitempty"`
}

type HealthResult struct {
	Healthy    bool   `json:"healthy"`
	StatusCode int    `json:"statusCode,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// ProviderImplementation is the common contract used by built-in and external source
// implementations. Source credentials stay on the server and are not part of
// any public request or response type.
type ProviderImplementation interface {
	Type() ProviderType
	Search(ctx context.Context, item Item, query string) ([]Candidate, error)
	Match(ctx context.Context, item Item, candidate Candidate) (MatchResult, error)
	Resolve(ctx context.Context, source Source, request ResolveRequest) (ResolvedStream, error)
	Probe(ctx context.Context, source Source) (HealthResult, error)
	Refresh(ctx context.Context, source Source) (Candidate, error)
}

type ProviderType string

const (
	TypeJavDBVideo     ProviderType = "javdb-video"
	TypeTrailer        ProviderType = "trailer"
	TypeExternalOnline ProviderType = "external-online"
)
