package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/module"
	provider "github.com/graysui/JMlab/internal/stream"
)

const (
	discoveryBudget   = 6 * time.Second
	resolutionBudget  = 8 * time.Second
	moduleCallTimeout = 2 * time.Second
	moduleConcurrency = 8
)

type discoveryFlight struct {
	done     chan struct{}
	finished time.Time
	err      error
}

// Calls that ignore context still occupy a slot until they actually return.
// This keeps uncooperative in-process implementations from spawning unbounded work.
func moduleCall[T any](ctx context.Context, s *Service, fn func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	s.discoveryMu.Lock()
	if s.moduleCalls == nil {
		s.moduleCalls = make(chan struct{}, moduleConcurrency)
	}
	slots := s.moduleCalls
	s.discoveryMu.Unlock()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return zero, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-slots
		return zero, err
	}
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-slots }()
		defer func() {
			if recover() != nil {
				done <- result{err: errors.New("模块调用异常")}
			}
		}()
		value, err := fn()
		done <- result{value, err}
	}()
	select {
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return result.value, result.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func parallelModules(ctx context.Context, count int, fn func(int) error) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	jobs := make(chan int)
	for worker := 0; worker < min(4, count); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := fn(i); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < count; i++ {
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return errors.Join(append(failures, ctx.Err())...)
}

func supportsStreamItem(entry *module.ModuleEntry, kind module.ItemType) bool {
	if entry.Status != module.StatusActive || entry.StreamInstance == nil {
		return false
	}
	// Empty declarations preserve older in-process adapters; the module decides.
	types := entry.SupportedTypes()
	if len(types) == 0 && len(entry.Manifest.SupportedItemTypes) == 0 && (entry.Capabilities == nil || len(entry.Capabilities.SupportedTypes) == 0) {
		return true
	}
	for _, supported := range types {
		if kind == supported {
			return true
		}
	}
	return false
}

func (s *Service) discoverModuleSources(ctx context.Context, item catalog.Item, force bool) error {
	if s.modules == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryBudget)
	defer cancel()
	kind := module.ItemType(item.ItemType)
	if kind == "" {
		kind = module.ItemMovie
	}
	entries := []*module.ModuleEntry{}
	for _, entry := range s.modules.List() {
		if supportsStreamItem(entry, kind) && s.playbackScopeMatches(ctx, entry, item.ID) {
			entries = append(entries, entry)
		}
	}
	err := parallelModules(ctx, len(entries), func(i int) error {
		entry := entries[i]
		key := item.ID + ":" + entry.Manifest.ID + ":" + entry.Revision
		s.discoveryMu.Lock()
		if s.discoveries == nil {
			s.discoveries = map[string]*discoveryFlight{}
		}
		if previous := s.discoveries[key]; previous != nil {
			select {
			case <-previous.done:
				ttl := 30 * time.Second
				if previous.err != nil {
					ttl = 3 * time.Second
				}
				if !force && time.Since(previous.finished) < ttl && !errors.Is(previous.err, context.Canceled) && !errors.Is(previous.err, context.DeadlineExceeded) {
					s.discoveryMu.Unlock()
					return previous.err
				}
			default:
				s.discoveryMu.Unlock()
				select {
				case <-previous.done:
					return previous.err
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if len(s.discoveries) >= 4096 {
			for key, previous := range s.discoveries {
				select {
				case <-previous.done:
					delete(s.discoveries, key)
				default:
				}
			}
		}
		flight := &discoveryFlight{done: make(chan struct{})}
		s.discoveries[key] = flight
		s.discoveryMu.Unlock()
		err := s.discoverEntrySources(ctx, item, kind, entry)
		s.discoveryMu.Lock()
		flight.err, flight.finished = err, time.Now()
		close(flight.done)
		s.discoveryMu.Unlock()
		return err
	})
	if s.catalog != nil {
		err = errors.Join(err, s.catalog.SyncLibraryMemberships(ctx, "", item.ID))
	}
	return err
}

func (s *Service) discoverEntrySources(ctx context.Context, item catalog.Item, kind module.ItemType, entry *module.ModuleEntry) error {
	ids := map[string]string{}
	for key, value := range item.ProviderIDs {
		ids[key] = value
	}
	if item.Code != "" && !strings.HasPrefix(item.Code, "media:") {
		ids["code"] = item.Code
	}
	extra := map[string]any{"sourceUrl": item.SourceURL, "parentId": item.ParentID, "indexNumber": item.IndexNumber, "originalTitle": item.OriginalTitle, "year": item.Year}
	for key, value := range item.ExtraAttributes {
		extra[key] = value
	}
	callCtx, cancel := context.WithTimeout(ctx, moduleCallTimeout)
	candidates, err := moduleCall(callCtx, s, func() ([]module.StreamCandidate, error) {
		return entry.StreamInstance.DiscoverSources(callCtx, item.ID, item.Title, kind, ids, extra)
	})
	cancel()
	if err != nil {
		return errors.Join(errors.New("模块发现候选失败："+entry.Manifest.ID), contextError(err))
	}
	if len(candidates) > 100 {
		return errors.New("模块候选数量超过上限")
	}
	unique := make([]module.StreamCandidate, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.CandidateID != "" && !seen[candidate.CandidateID] {
			seen[candidate.CandidateID] = true
			unique = append(unique, candidate)
		}
	}
	return parallelModules(ctx, len(unique), func(i int) error {
		candidate := unique[i]
		callCtx, cancel := context.WithTimeout(ctx, moduleCallTimeout)
		defer cancel()
		resolved, err := moduleCall(callCtx, s, func() (module.ResolvedStream, error) {
			return entry.StreamInstance.ResolveStream(callCtx, candidate.CandidateID, candidate.EncryptedPayload, nil)
		})
		if err != nil {
			return errors.Join(errors.New("模块候选解析失败："+entry.Manifest.ID), contextError(err))
		}
		if resolved.Media == nil {
			resolved.Media = candidate.Media
		}
		if err := normalizeModuleMedia(&resolved); err != nil {
			return err
		}
		if len(resolved.Subtitles) > 64 {
			return errors.New("字幕数量过多")
		}
		converted := moduleResolution(resolved, candidate.IsPreview)
		if s.validateUpstreamURL(sourceRecord{ModuleID: entry.Manifest.ID}, converted.URL) != nil {
			return errors.New("模块播放地址未获授权")
		}
		ref := moduleCandidateRef{ModuleID: entry.Manifest.ID, CandidateID: candidate.CandidateID, Payload: candidate.EncryptedPayload, Revision: entry.Revision, Subtitles: resolved.Subtitles, Media: resolved.Media}
		payload, err := json.Marshal(ref)
		if err != nil {
			return errors.New("模块候选引用无效")
		}
		digest := sha256.Sum256([]byte(item.ID + "\x00" + entry.Manifest.ID + "\x00" + candidate.CandidateID))
		sourceID := "modsrc_" + hex.EncodeToString(digest[:16])
		definition := sourceDefinition{moduleID: entry.Manifest.ID, label: candidate.Label, isPreview: candidate.IsPreview}
		if definition.label == "" {
			definition.label = entry.Manifest.Name
		}
		return s.modules.WithActiveRevision(ctx, entry.Manifest.ID, entry.Revision, func() error {
			return s.upsertDiscoveredSource(ctx, item.ID, definition, provider.Candidate{ID: candidate.CandidateID, URL: string(payload), Quality: candidate.Quality, Language: candidate.Language, IsPreview: candidate.IsPreview}, converted, sourceID)
		})
	})
}

func contextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
