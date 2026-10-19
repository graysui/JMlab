package module

import (
	"context"
	"errors"
	"sync"
	"time"
)

const healthInterval = 15 * time.Second

// HealthSnapshot contains only safe diagnostics, never upstream response text.
type HealthSnapshot struct {
	State               string    `json:"state"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	LastCheckedAt       time.Time `json:"lastCheckedAt,omitzero"`
	LastSuccessAt       time.Time `json:"lastSuccessAt,omitzero"`
	NextCheckAt         time.Time `json:"nextCheckAt,omitzero"`
}

func (c *HTTPModuleClient) Health() HealthSnapshot {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	state := c.health
	if state.State == "" {
		state.State = "unknown"
	}
	return state
}

func (c *HTTPModuleClient) recordHealth(ctx context.Context, err error) {
	// User cancellation and watcher shutdown do not indicate an offline source.
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	// Serialize state and observer notifications so concurrent replies cannot
	// publish an older healthy snapshot after a newer open circuit snapshot.
	c.resultMu.Lock()
	defer c.resultMu.Unlock()
	c.healthMu.Lock()
	now := time.Now().UTC()
	c.health.LastCheckedAt = now
	c.health.NextCheckAt = now.Add(healthInterval)
	if err == nil {
		c.health.State = "healthy"
		c.health.ConsecutiveFailures = 0
		c.health.LastSuccessAt = now
		c.openUntil, c.backoff = time.Time{}, 0
	} else {
		c.health.ConsecutiveFailures++
		c.health.State = "degraded"
		if c.health.ConsecutiveFailures >= 3 {
			// Already-running business calls may fail after the circuit opens.
			// They must not repeatedly extend the same cooldown window.
			if c.openUntil.IsZero() || !now.Before(c.openUntil) {
				c.backoff *= 2
				if c.backoff < 30*time.Second {
					c.backoff = 30 * time.Second
				}
				if c.backoff > 5*time.Minute {
					c.backoff = 5 * time.Minute
				}
				c.openUntil = now.Add(c.backoff)
			}
			c.health.State, c.health.NextCheckAt = "open", c.openUntil
		}
	}
	snapshot := c.health
	c.healthMu.Unlock()
	if c.onHealth != nil {
		c.onHealth(snapshot)
	}
}

func (c *HTTPModuleClient) probeHealth(ctx context.Context) {
	var caps MetadataCaps
	err := c.requestJSON(ctx, "GET", "/api/v1/caps", nil, &caps)
	if err == nil && (c.manifest.Type == TypeStream || c.manifest.Type == TypeComposite) && !caps.SupportsStreams {
		err = errors.New("播放源能力不可用")
	}
	c.recordHealth(ctx, err)
}

func httpClient(entry *ModuleEntry) *HTTPModuleClient {
	if entry == nil {
		return nil
	}
	if client, ok := entry.MetadataInstance.(*HTTPModuleClient); ok {
		return client
	}
	client, _ := entry.StreamInstance.(*HTTPModuleClient)
	return client
}

func (m *Manager) observeHealth(id string, client *HTTPModuleClient, health HealthSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.registry.Get(id)
	if !ok || entry.Status == StatusDisabled || httpClient(entry) != client {
		return
	}
	oldStatus := entry.Status
	status := oldStatus
	message := ""
	switch health.State {
	case "open":
		status, message = StatusError, "来源连续连接失败，已暂停调用，系统会自动重试"
	case "degraded":
		message = "来源连接不稳定，系统正在检查"
	case "healthy":
		status = StatusActive
	}
	m.registry.setHealth(id, health, status, message, oldStatus != status && status == StatusActive)
	if oldStatus != status {
		m.syncEvents(id)
		m.emitStatus(id, string(status))
	}
}

// CheckHealth probes independent sources concurrently, never holding m.mu over
// network IO. It is safe to invoke while replacing/disabling modules.
func (m *Manager) CheckHealth(ctx context.Context) {
	if !m.probeMu.TryLock() {
		return
	}
	defer m.probeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, entry := range m.registry.List() {
		client := httpClient(entry)
		if client == nil || entry.Status == StatusDisabled || time.Now().Before(client.Health().NextCheckAt) {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			current, ok := m.registry.Get(entry.Manifest.ID)
			if !ok || current.Status == StatusDisabled || httpClient(current) != client {
				return
			}
			probeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			client.probeHealth(probeCtx)
		}()
	}
	wg.Wait()
}
