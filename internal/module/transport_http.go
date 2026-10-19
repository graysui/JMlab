package module

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPModuleClient 通用 HTTP 模块通信客户端适配器
type HTTPModuleClient struct {
	hostBridge   HostBridge
	manifest     *Manifest
	baseURL      string
	httpClient   *http.Client
	configHeader string
	token        string
	semaphore    chan struct{}
	healthMu     sync.Mutex
	resultMu     sync.Mutex
	health       HealthSnapshot
	openUntil    time.Time
	backoff      time.Duration
	onHealth     func(HealthSnapshot)
}

func NewHTTPModuleClient(m *Manifest) (*HTTPModuleClient, error) {
	return NewHTTPModuleClientWithConfig(m, nil, "")
}

func NewHTTPModuleClientWithConfig(m *Manifest, config map[string]any, token string) (*HTTPModuleClient, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	rawURL := strings.TrimRight(m.Transport.URL, "/")
	if rawURL == "" {
		return nil, fmt.Errorf("module %q transport URL is empty", m.ID)
	}
	endpoint, _ := url.Parse(rawURL)
	hasSecrets := token != ""
	for key, value := range config {
		field := m.ConfigSchema[key]
		if value != nil && (field.Secret || field.Type == "password") {
			hasSecrets = true
		}
	}
	host := endpoint.Hostname()
	loopback := strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
	if hasSecrets && endpoint.Scheme != "https" && !loopback {
		return nil, fmt.Errorf("含凭证的模块连接需要 HTTPS，或使用本机回环地址")
	}
	encoded, err := json.Marshal(config)
	if err != nil || len(encoded) > 24*1024 {
		return nil, fmt.Errorf("模块配置过大或不可编码")
	}
	return &HTTPModuleClient{
		manifest:     m,
		baseURL:      rawURL,
		configHeader: base64.StdEncoding.EncodeToString(encoded),
		token:        token,
		semaphore:    make(chan struct{}, 8),
		httpClient: &http.Client{
			Timeout:       10 * time.Second,
			Transport:     http.DefaultTransport.(*http.Transport).Clone(),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *HTTPModuleClient) ModuleID() string {
	return c.manifest.ID
}

type httpResponseError struct{ status int }

func (e *httpResponseError) Error() string {
	return fmt.Sprintf("模块请求失败（HTTP %d）", e.status)
}

func (c *HTTPModuleClient) doJSON(ctx context.Context, method, path string, reqBody any, respBody any) error {
	c.healthMu.Lock()
	blocked := !c.openUntil.IsZero()
	c.healthMu.Unlock()
	if blocked {
		return fmt.Errorf("来源暂时不可用，正在等待连接检查")
	}
	err := c.requestJSON(ctx, method, path, reqBody, respBody)
	var responseErr *httpResponseError
	// Missing content and invalid search input are ordinary business outcomes.
	// Caps failures, authentication, rate limits and server/network errors affect
	// availability; optional notification delivery is tracked separately.
	if path == "/api/v1/caps" || !errors.As(err, &responseErr) || responseErr.status < 400 || responseErr.status >= 500 || responseErr.status == 401 || responseErr.status == 403 || responseErr.status == 429 {
		c.recordHealth(ctx, err)
	}
	return err
}

func (c *HTTPModuleClient) requestJSON(ctx context.Context, method, path string, reqBody any, respBody any) error {
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-JMlab-Module-ID", c.manifest.ID)
	if c.configHeader != "" {
		req.Header.Set("X-JMlab-Module-Config", c.configHeader)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.hostBridge != nil {
		for key, value := range c.hostBridge.Headers(ctx, c) {
			req.Header.Set(key, value)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("module HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpResponseError{status: resp.StatusCode}
	}

	if respBody != nil {
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
		if err != nil {
			return fmt.Errorf("读取模块响应失败")
		}
		if len(data) > 8*1024*1024 {
			return fmt.Errorf("模块响应超过 8 MiB 上限")
		}
		if err := json.Unmarshal(data, respBody); err != nil {
			return fmt.Errorf("failed to decode module response: %w", err)
		}
	}
	return nil
}

func (c *HTTPModuleClient) Close() { c.httpClient.CloseIdleConnections() }

// ---------------- MetadataProvider 实现 ----------------

func (c *HTTPModuleClient) Caps(ctx context.Context) (MetadataCaps, error) {
	var caps MetadataCaps
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/caps", nil, &caps)
	return caps, err
}

func (c *HTTPModuleClient) Search(ctx context.Context, query string, itemType ItemType, page, pageSize int) ([]MediaSummary, int, error) {
	req := map[string]any{
		"query":    query,
		"itemType": itemType,
		"page":     page,
		"pageSize": pageSize,
	}
	var res struct {
		Items []MediaSummary `json:"items"`
		Total int            `json:"total"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/search", req, &res); err != nil {
		return nil, 0, err
	}
	return res.Items, res.Total, nil
}

func (c *HTTPModuleClient) GetDetail(ctx context.Context, sourceID string, providerIDs map[string]string) (MediaDetail, error) {
	req := map[string]any{
		"sourceId":    sourceID,
		"providerIds": providerIDs,
	}
	var detail MediaDetail
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/detail", req, &detail); err != nil {
		return MediaDetail{}, err
	}
	return detail, nil
}

func (c *HTTPModuleClient) GetRankings(ctx context.Context, rankingType, period string, page int) (RankingSnapshot, error) {
	req := map[string]any{
		"rankingType": rankingType,
		"period":      period,
		"page":        page,
	}
	var snapshot RankingSnapshot
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/rankings", req, &snapshot); err != nil {
		return RankingSnapshot{}, err
	}
	return snapshot, nil
}

// ---------------- StreamProvider 实现 ----------------

func (c *HTTPModuleClient) DiscoverSources(ctx context.Context, itemID, title string, itemType ItemType, providerIDs map[string]string, extra map[string]any) ([]StreamCandidate, error) {
	req := map[string]any{
		"itemId":      itemID,
		"title":       title,
		"itemType":    itemType,
		"providerIds": providerIDs,
		"extra":       extra,
	}
	var res struct {
		Candidates []StreamCandidate `json:"candidates"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/stream/discover", req, &res); err != nil {
		return nil, err
	}
	return res.Candidates, nil
}

func (c *HTTPModuleClient) ResolveStream(ctx context.Context, candidateID, encryptedPayload string, clientHeaders map[string]string) (ResolvedStream, error) {
	req := map[string]any{
		"candidateId":      candidateID,
		"encryptedPayload": encryptedPayload,
		"clientHeaders":    clientHeaders,
	}
	var resolved ResolvedStream
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/stream/resolve", req, &resolved); err != nil {
		return ResolvedStream{}, err
	}
	return resolved, nil
}

func (c *HTTPModuleClient) ProbeSource(ctx context.Context, candidateID, encryptedPayload string) (HealthResult, error) {
	req := map[string]any{
		"candidateId":      candidateID,
		"encryptedPayload": encryptedPayload,
	}
	var health HealthResult
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/stream/probe", req, &health); err != nil {
		return HealthResult{Healthy: false, Error: err.Error()}, err
	}
	return health, nil
}
