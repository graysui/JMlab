package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "golang.org/x/image/webp"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/metadata"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

const maxImageBytes = 10 << 20
const maxDerivativeDimension = 2048

type Service struct {
	modules      *module.Registry
	db           *repository.Database
	authService  *auth.Service
	adapter      metadata.SourceAdapter
	cacheDir     string
	client       *http.Client
	directClient *http.Client
	logger       *slog.Logger
}

type Image struct {
	ID, ItemID, ImageType, SourceURL, SourceLabel, MimeType, Status, SHA256 string
	Width, Height, SortOrder, SourcePriority                                int
	UpdatedAt, ErrorMessage, LastAttemptAt                                  string
	FetchDurationMS                                                         int
	IsFavorite                                                              bool
}

type imageView struct {
	ID              string `json:"id"`
	ItemID          string `json:"itemId"`
	ImageType       string `json:"imageType"`
	URL             string `json:"url"`
	SourceLabel     string `json:"sourceLabel,omitempty"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	MimeType        string `json:"mimeType"`
	Status          string `json:"status"`
	SortOrder       int    `json:"sortOrder"`
	SourcePriority  int    `json:"sourcePriority"`
	SHA256          string `json:"sha256,omitempty"`
	UpdatedAt       string `json:"updatedAt"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	LastAttemptAt   string `json:"lastAttemptAt,omitempty"`
	FetchDurationMS int    `json:"fetchDurationMs"`
	IsFavorite      bool   `json:"isFavorite"`
}

type imageAttempt struct {
	ItemID, ImageID, ImageType, SourceURL, SourceLabel, Status, ErrorMessage string
	SourcePriority, DurationMS                                               int
	AttemptedAt                                                              string
}

func NewService(db *repository.Database, authService *auth.Service, cfg config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	cacheDir := filepath.Join(cfg.DataDir, "cache", "images")
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			return validateRemoteURL(request.URL)
		},
	}
	directClient := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			return validateRemoteURL(request.URL)
		},
	}
	return &Service{db: db, authService: authService, cacheDir: cacheDir, client: client, directClient: directClient, logger: logger}
}

func (s *Service) SetSourceAdapter(adapter metadata.SourceAdapter) {
	s.adapter = adapter
}

func (s *Service) EnsureItemImages(ctx context.Context) error {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind(`
		UPDATE item_images SET source_url = replace(source_url, 'https://tp.spfcas.com/rhe951l4q', 'https://c0.jdbstatic.com') WHERE source_url LIKE '%tp.spfcas.com%'
	`))
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind(`
		UPDATE item_images SET source_url = replace(source_url, 'http://c0.jdbstatic.com', 'https://c0.jdbstatic.com') WHERE source_url LIKE 'http://c0.jdbstatic.com%'
	`))
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind(`
		UPDATE item_images SET status = 'pending', error_message = NULL WHERE status = 'error'
	`))
	if s.adapter == nil && s.modules == nil {
		return nil
	}
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind(`
		SELECT vi.id, COALESCE(vi.source_id, ''), COALESCE(vi.source_url, '')
		FROM media_items vi
		WHERE vi.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM item_images ii WHERE ii.item_id = vi.id)
		LIMIT 300
	`))
	if err != nil {
		return err
	}
	defer rows.Close()
	type itemRef struct{ id, sourceID, sourceURL string }
	var toRepair []itemRef
	for rows.Next() {
		var ref itemRef
		if scanErr := rows.Scan(&ref.id, &ref.sourceID, &ref.sourceURL); scanErr == nil {
			toRepair = append(toRepair, ref)
		}
	}
	for _, ref := range toRepair {
		if ref.sourceID == "" && ref.sourceURL == "" {
			continue
		}
		sourceItem, fetchErr := s.fetchItemMetadata(ctx, ref.id, ref.sourceID, ref.sourceURL)
		if fetchErr != nil || len(sourceItem.Images) == 0 {
			continue
		}
		for idx, img := range sourceItem.Images {
			_ = s.SeedSource(ctx, ref.id, img.Type, img.URL, img.Label, img.Priority, idx)
		}
	}
	return nil
}

// SeedSource records a remote image source discovered while importing metadata.
// The image remains pending until the normal image reload path downloads it,
// so a large ranking generation does not block on image bytes.
func (s *Service) SeedSource(ctx context.Context, itemID, imageType, sourceURL, sourceLabel string, sourcePriority, sortOrder int) error {
	sourceURL = normalizeImageURL(sourceURL)
	if !validImageType(imageType) {
		return fmt.Errorf("图片类型不支持")
	}
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("图片地址必须是 HTTPS URL")
	}
	if !s.exists(ctx, itemID) {
		return fmt.Errorf("作品不存在")
	}
	cacheSum := sha256.Sum256([]byte(itemID + ":" + imageType + ":" + parsed.String()))
	cacheKey := hex.EncodeToString(cacheSum[:])
	imageID := "img_" + cacheKey[:24]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO item_images (id, item_id, image_type, source_url, cache_key, source_label, status, sort_order, source_priority, mime_type, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, 'application/octet-stream', ?, ?) ON CONFLICT(cache_key) DO UPDATE SET item_id = excluded.item_id, image_type = excluded.image_type, source_url = excluded.source_url, source_label = excluded.source_label, source_priority = excluded.source_priority, sort_order = excluded.sort_order, updated_at = excluded.updated_at, status = CASE WHEN item_images.status = 'ready' THEN 'ready' ELSE 'pending' END"), imageID, itemID, imageType, parsed.String(), cacheKey, strings.TrimSpace(sourceLabel), sortOrder, sourcePriority, now, now)
	if err != nil {
		return fmt.Errorf("图片来源写入失败: %w", err)
	}
	return nil
}

func (s *Service) RegisterRoutes(router chi.Router) {
	router.With(s.authService.RequireAuth).Get("/items/{itemID}/images", s.list)
	router.With(s.authService.RequireAuth, s.authService.RequireRole("Admin", "Editor"), s.authService.RequireCSRF).Post("/items/{itemID}/images/reload", s.reload)
	router.With(s.authService.RequireAuth).Get("/items/{itemID}/images/{imageID}/content", s.content)
	router.With(s.authService.RequireAuth).Head("/items/{itemID}/images/{imageID}/content", s.content)
	router.With(s.authService.RequireAuth).Get("/items/{itemID}/images/{imageType}", s.serve)
	router.With(s.authService.RequireAuth).Head("/items/{itemID}/images/{imageType}", s.serve)
}

func unescapeParam(r *http.Request, key string) string {
	val := chi.URLParam(r, key)
	if unescaped, err := url.PathUnescape(val); err == nil {
		return unescaped
	}
	return val
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	itemID := unescapeParam(r, "itemID")
	if !s.visible(r.Context(), itemID, principal.User) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
		return
	}
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT i.id, i.item_id, i.image_type, i.source_url, COALESCE(i.source_label, ''), COALESCE(i.mime_type, 'application/octet-stream'), COALESCE(i.width, 0), COALESCE(i.height, 0), COALESCE(i.sha256, ''), i.status, i.sort_order, i.source_priority, i.updated_at, COALESCE(i.error_message, ''), COALESCE(i.last_attempt_at, ''), COALESCE(i.fetch_duration_ms, 0), COALESCE(uis.is_favorite, 0) FROM item_images i LEFT JOIN user_image_states uis ON uis.image_id = i.id AND uis.user_id = ? WHERE i.item_id = ? ORDER BY i.image_type, i.sort_order, i.source_priority, i.id"), principal.User.ID, itemID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "IMAGES_READ_FAILED", "读取图片失败")
		return
	}
	defer rows.Close()
	images := make([]imageView, 0)
	for rows.Next() {
		var image Image
		var favorite int
		if err := rows.Scan(&image.ID, &image.ItemID, &image.ImageType, &image.SourceURL, &image.SourceLabel, &image.MimeType, &image.Width, &image.Height, &image.SHA256, &image.Status, &image.SortOrder, &image.SourcePriority, &image.UpdatedAt, &image.ErrorMessage, &image.LastAttemptAt, &image.FetchDurationMS, &favorite); err != nil {
			writeError(w, r, http.StatusInternalServerError, "IMAGES_READ_FAILED", "读取图片失败")
			return
		}
		image.IsFavorite = favorite == 1
		images = append(images, s.view(image))
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "IMAGES_READ_FAILED", "读取图片失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": images})
}

func (s *Service) reload(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	itemID := unescapeParam(r, "itemID")
	var input struct {
		Images []struct {
			Type     string `json:"type"`
			URL      string `json:"url"`
			Label    string `json:"sourceLabel"`
			Priority int    `json:"sourcePriority"`
		} `json:"images"`
	}
	if err := decodeJSON(r, &input); err != nil || len(input.Images) == 0 {
		writeError(w, r, http.StatusBadRequest, "INVALID_IMAGES", "images 必须是非空数组")
		return
	}
	if !s.exists(r.Context(), itemID) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
		return
	}
	results := make([]Image, 0, len(input.Images))
	failed := 0
	for index, requested := range input.Images {
		priority := requested.Priority
		if priority == 0 {
			priority = index
		}
		image, err := s.download(r.Context(), itemID, requested.Type, requested.URL, requested.Label, priority, index)
		if err != nil {
			failed++
			s.logger.Warn("图片刷新失败", "itemId", itemID, "imageType", requested.Type, "sourcePriority", priority, "error", safeError(err))
			continue
		}
		results = append(results, image)
	}
	s.audit(r.Context(), principal.User.ID, itemID, middleware.GetReqID(r.Context()), len(results), failed)
	status := http.StatusOK
	if len(results) == 0 {
		status = http.StatusBadGateway
	} else if failed > 0 {
		status = http.StatusMultiStatus
	}
	views := make([]imageView, 0, len(results))
	for _, image := range results {
		views = append(views, s.view(image))
	}
	writeJSON(w, status, map[string]any{"items": views, "failed": failed})
}

func (s *Service) download(ctx context.Context, itemID, imageType, sourceURL, sourceLabel string, sourcePriority, sortOrder int) (Image, error) {
	started := time.Now()
	sourceURL = normalizeImageURL(sourceURL)
	cleanURL := strings.TrimSpace(sourceURL)
	if !validImageType(imageType) {
		err := fmt.Errorf("图片类型不支持")
		s.recordAttempt(ctx, imageAttempt{ItemID: itemID, ImageType: imageType, SourceURL: cleanURL, SourceLabel: sourceLabel, SourcePriority: sourcePriority, Status: "error", ErrorMessage: safeError(err), DurationMS: int(time.Since(started).Milliseconds()), AttemptedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		return Image{}, err
	}
	cleanURL = normalizeImageURL(cleanURL)
	parsed, err := url.Parse(cleanURL)
	if err != nil {
		s.recordAttempt(ctx, imageAttempt{ItemID: itemID, ImageType: imageType, SourceURL: cleanURL, SourceLabel: sourceLabel, SourcePriority: sourcePriority, Status: "error", ErrorMessage: "图片 URL 无效", DurationMS: int(time.Since(started).Milliseconds()), AttemptedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		return Image{}, fmt.Errorf("图片 URL 无效")
	}
	if err := validateRemoteURL(parsed); err != nil {
		s.recordAttempt(ctx, imageAttempt{ItemID: itemID, ImageType: imageType, SourceURL: cleanURL, SourceLabel: sourceLabel, SourcePriority: sourcePriority, Status: "error", ErrorMessage: safeError(err), DurationMS: int(time.Since(started).Milliseconds()), AttemptedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		return Image{}, err
	}
	if err := os.MkdirAll(s.cacheDir, 0o750); err != nil {
		s.recordAttempt(ctx, imageAttempt{ItemID: itemID, ImageType: imageType, SourceURL: cleanURL, SourceLabel: sourceLabel, SourcePriority: sourcePriority, Status: "error", ErrorMessage: "图片缓存目录不可用", DurationMS: int(time.Since(started).Milliseconds()), AttemptedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		return Image{}, err
	}

	// Prepare candidate URLs
	var candidateURLs []string
	if strings.Contains(cleanURL, "jdbstatic.com") || strings.Contains(cleanURL, "spfcas.com") {
		cleanJdbURL := cleanURL
		if strings.Contains(cleanJdbURL, "tp.spfcas.com/rhe951l4q") {
			cleanJdbURL = strings.ReplaceAll(cleanJdbURL, "https://tp.spfcas.com/rhe951l4q", "https://c0.jdbstatic.com")
			cleanJdbURL = strings.ReplaceAll(cleanJdbURL, "http://tp.spfcas.com/rhe951l4q", "https://c0.jdbstatic.com")
		}
		candidateURLs = []string{
			"https://wsrv.nl/?url=" + url.QueryEscape(cleanJdbURL),
			"https://images.weserv.nl/?url=" + url.QueryEscape(cleanJdbURL),
			cleanJdbURL,
		}
	} else {
		candidateURLs = []string{
			cleanURL,
			"https://wsrv.nl/?url=" + url.QueryEscape(cleanURL),
		}
	}

	var data []byte
	var contentType string
	var lastErr error

	for _, cand := range candidateURLs {
		candParsed, candErr := url.Parse(cand)
		if candErr != nil || validateRemoteURL(candParsed) != nil {
			continue
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, cand, nil)
		if reqErr != nil {
			continue
		}
		req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")

		resp, doErr := s.client.Do(req)
		if doErr != nil || (resp != nil && resp.StatusCode >= 400) {
			if resp != nil {
				resp.Body.Close()
			}
			if directReq, directErr := http.NewRequestWithContext(ctx, http.MethodGet, cand, nil); directErr == nil {
				directReq.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
				directReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
				if directResp, directDoErr := s.directClient.Do(directReq); directDoErr == nil {
					resp = directResp
					doErr = nil
				}
			}
		}
		if doErr != nil || resp == nil {
			lastErr = doErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			resp.Body.Close()
			continue
		}
		limited := io.LimitReader(resp.Body, maxImageBytes+1)
		bodyBytes, readErr := io.ReadAll(limited)
		resp.Body.Close()
		if readErr != nil || len(bodyBytes) == 0 || len(bodyBytes) > maxImageBytes {
			lastErr = readErr
			continue
		}
		if mime, ok := detectImageMime(bodyBytes); ok {
			data = bodyBytes
			contentType = mime
			break
		}
	}

	if len(data) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("上游响应不是有效图片")
		}
		s.recordAttempt(ctx, imageAttempt{ItemID: itemID, ImageType: imageType, SourceURL: cleanURL, SourceLabel: sourceLabel, SourcePriority: sourcePriority, Status: "error", ErrorMessage: safeError(lastErr), DurationMS: int(time.Since(started).Milliseconds()), AttemptedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		return Image{}, lastErr
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		config.Width = 800
		config.Height = 1200
	}
	hash := sha256.Sum256([]byte(itemID + ":" + imageType + ":" + parsed.String()))
	cacheKey := hex.EncodeToString(hash[:])
	target := filepath.Join(s.cacheDir, cacheKey)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return Image{}, err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return Image{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := "img_" + cacheKey[:24]
	contentHash := hex.EncodeToString(sha256Bytes(data))
	durationMS := int(time.Since(started).Milliseconds())
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO item_images (id, item_id, image_type, source_url, cache_key, width, height, sha256, mime_type, source_label, source_priority, sort_order, status, error_message, last_attempt_at, fetch_duration_ms, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'ready', NULL, ?, ?, ?, ?) ON CONFLICT(cache_key) DO UPDATE SET item_id = excluded.item_id, image_type = excluded.image_type, source_url = excluded.source_url, width = excluded.width, height = excluded.height, sha256 = excluded.sha256, mime_type = excluded.mime_type, source_label = excluded.source_label, source_priority = excluded.source_priority, sort_order = excluded.sort_order, status = 'ready', error_message = NULL, last_attempt_at = excluded.last_attempt_at, fetch_duration_ms = excluded.fetch_duration_ms, updated_at = excluded.updated_at"), id, itemID, imageType, parsed.String(), cacheKey, config.Width, config.Height, contentHash, contentType, strings.TrimSpace(sourceLabel), sourcePriority, sortOrder, now, durationMS, now, now)
	if err != nil {
		s.recordAttempt(ctx, imageAttempt{ItemID: itemID, ImageID: id, ImageType: imageType, SourceURL: cleanURL, SourceLabel: sourceLabel, SourcePriority: sourcePriority, Status: "error", ErrorMessage: "图片元数据写入失败", DurationMS: durationMS, AttemptedAt: now})
		return Image{}, err
	}
	s.recordAttempt(ctx, imageAttempt{ItemID: itemID, ImageID: id, ImageType: imageType, SourceURL: cleanURL, SourceLabel: sourceLabel, SourcePriority: sourcePriority, Status: "ready", DurationMS: durationMS, AttemptedAt: now})
	return Image{ID: id, ItemID: itemID, ImageType: imageType, SourceURL: parsed.String(), SourceLabel: strings.TrimSpace(sourceLabel), MimeType: contentType, Width: config.Width, Height: config.Height, SHA256: contentHash, Status: "ready", SortOrder: sortOrder, SourcePriority: sourcePriority, FetchDurationMS: durationMS, LastAttemptAt: now, UpdatedAt: now}, nil
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	itemID, imageType := unescapeParam(r, "itemID"), chi.URLParam(r, "imageType")
	if ok && principal.User.ID != "" {
		if !s.visible(r.Context(), itemID, principal.User) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
			return
		}
	} else if !s.exists(r.Context(), itemID) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
		return
	}
	s.serveSelected(w, r, itemID, imageType, "")
}

func (s *Service) content(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	itemID, imageID := unescapeParam(r, "itemID"), chi.URLParam(r, "imageID")
	if ok && principal.User.ID != "" {
		if !s.visible(r.Context(), itemID, principal.User) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
			return
		}
	} else if !s.exists(r.Context(), itemID) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "作品不存在")
		return
	}
	s.serveSelected(w, r, itemID, "", imageID)
}

func (s *Service) serveSelected(w http.ResponseWriter, r *http.Request, itemID, imageType, imageID string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	var id, cacheKey, mimeType, contentHash, status, sourceURL, sourceLabel, actualType string
	var width, height, sourcePriority, sortOrder int

	query := "SELECT id, cache_key, COALESCE(mime_type, 'application/octet-stream'), COALESCE(sha256, ''), COALESCE(width, 0), COALESCE(height, 0), status, source_url, COALESCE(source_label, ''), source_priority, sort_order, image_type FROM item_images WHERE item_id = ?"
	args := []any{itemID}
	if imageID != "" {
		query += " AND id = ?"
		args = append(args, imageID)
	} else {
		query += " AND image_type = ? ORDER BY CASE WHEN status = 'ready' THEN 0 ELSE 1 END, sort_order, source_priority, id LIMIT 1"
		args = append(args, imageType)
	}
	err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind(query), args...).Scan(&id, &cacheKey, &mimeType, &contentHash, &width, &height, &status, &sourceURL, &sourceLabel, &sourcePriority, &sortOrder, &actualType)

	// If backdrop requested but not found, try poster; if poster requested but not found, try backdrop
	if errors.Is(err, sql.ErrNoRows) && imageID == "" {
		fallbackType := ""
		if imageType == "backdrop" {
			fallbackType = "poster"
		} else if imageType == "poster" {
			fallbackType = "backdrop"
		}
		if fallbackType != "" {
			fallbackQuery := "SELECT id, cache_key, COALESCE(mime_type, 'application/octet-stream'), COALESCE(sha256, ''), COALESCE(width, 0), COALESCE(height, 0), status, source_url, COALESCE(source_label, ''), source_priority, sort_order, image_type FROM item_images WHERE item_id = ? AND image_type = ? ORDER BY CASE WHEN status = 'ready' THEN 0 ELSE 1 END, sort_order, source_priority, id LIMIT 1"
			err = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind(fallbackQuery), itemID, fallbackType).Scan(&id, &cacheKey, &mimeType, &contentHash, &width, &height, &status, &sourceURL, &sourceLabel, &sourcePriority, &sortOrder, &actualType)
		}
	}

	// If still not found and adapter is available, attempt to seed images from upstream
	if errors.Is(err, sql.ErrNoRows) && (s.adapter != nil || s.modules != nil) {
		var sourceID, sourceURLVal string
		if scanErr := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COALESCE(source_id, ''), COALESCE(source_url, '') FROM media_items WHERE id = ? AND deleted_at IS NULL"), itemID).Scan(&sourceID, &sourceURLVal); scanErr == nil && (sourceID != "" || sourceURLVal != "") {
			if sourceItem, fetchErr := s.fetchItemMetadata(r.Context(), itemID, sourceID, sourceURLVal); fetchErr == nil && len(sourceItem.Images) > 0 {
				for idx, img := range sourceItem.Images {
					_ = s.SeedSource(r.Context(), itemID, img.Type, img.URL, img.Label, img.Priority, idx)
				}
				err = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind(query), args...).Scan(&id, &cacheKey, &mimeType, &contentHash, &width, &height, &status, &sourceURL, &sourceLabel, &sourcePriority, &sortOrder, &actualType)
				if errors.Is(err, sql.ErrNoRows) && imageType == "backdrop" {
					fallbackQuery := "SELECT id, cache_key, COALESCE(mime_type, 'application/octet-stream'), COALESCE(sha256, ''), COALESCE(width, 0), COALESCE(height, 0), status, source_url, COALESCE(source_label, ''), source_priority, sort_order, image_type FROM item_images WHERE item_id = ? AND image_type = 'poster' ORDER BY CASE WHEN status = 'ready' THEN 0 ELSE 1 END, sort_order, source_priority, id LIMIT 1"
					err = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind(fallbackQuery), itemID).Scan(&id, &cacheKey, &mimeType, &contentHash, &width, &height, &status, &sourceURL, &sourceLabel, &sourcePriority, &sortOrder, &actualType)
				}
			}
		}
	}

	if errors.Is(err, sql.ErrNoRows) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.fallbackPoster(r.Context(), itemID))
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "IMAGE_READ_FAILED", "读取图片失败")
		return
	}

	path := filepath.Join(s.cacheDir, cacheKey)
	data, readErr := os.ReadFile(path)
	if readErr != nil || status != "ready" {
		downloaded, dlErr := s.download(r.Context(), itemID, actualType, sourceURL, sourceLabel, sourcePriority, sortOrder)
		if dlErr == nil {
			hash := sha256.Sum256([]byte(itemID + ":" + actualType + ":" + strings.TrimSpace(downloaded.SourceURL)))
			cacheKey = hex.EncodeToString(hash[:])
			path = filepath.Join(s.cacheDir, cacheKey)
			data, readErr = os.ReadFile(path)
			mimeType = downloaded.MimeType
			contentHash = downloaded.SHA256
			width = downloaded.Width
			height = downloaded.Height
		} else {
			if strings.HasPrefix(sourceURL, "https://") {
				http.Redirect(w, r, sourceURL, http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(s.fallbackPoster(r.Context(), itemID))
			return
		}
	}
	if readErr != nil || len(data) == 0 {
		if strings.HasPrefix(sourceURL, "https://") {
			http.Redirect(w, r, sourceURL, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.fallbackPoster(r.Context(), itemID))
		return
	}

	requestedWidth, requestedHeight, fit, err := parseDerivativeQuery(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_IMAGE_SIZE", err.Error())
		return
	}
	variant := fmt.Sprintf("%s:%d:%d:%s", contentHash, requestedWidth, requestedHeight, fit)
	etag := `"` + hex.EncodeToString(sha256Bytes([]byte(variant)))[:32] + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-store")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if requestedWidth == 0 && requestedHeight == 0 {
		w.Header().Set("Content-Type", mimeType)
		http.ServeContent(w, r, cacheKey, time.Time{}, bytes.NewReader(data))
		return
	}
	variantData, variantType, err := derivative(data, mimeType, width, height, requestedWidth, requestedHeight, fit)
	if err != nil {
		w.Header().Set("Content-Type", mimeType)
		http.ServeContent(w, r, cacheKey, time.Time{}, bytes.NewReader(data))
		return
	}
	w.Header().Set("Content-Type", variantType)
	w.Header().Set("Content-Length", strconv.Itoa(len(variantData)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(variantData)
}

func (s *Service) exists(ctx context.Context, itemID string) bool {
	var count int
	return s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM media_items WHERE id = ? AND deleted_at IS NULL"), itemID).Scan(&count) == nil && count == 1
}

func (s *Service) view(image Image) imageView {
	return imageView{
		ID: image.ID, ItemID: image.ItemID, ImageType: image.ImageType,
		URL:         "/api/v1/items/" + url.PathEscape(image.ItemID) + "/images/" + url.PathEscape(image.ID) + "/content",
		SourceLabel: image.SourceLabel, Width: image.Width, Height: image.Height,
		MimeType: image.MimeType, Status: image.Status, SortOrder: image.SortOrder,
		SourcePriority: image.SourcePriority, SHA256: image.SHA256, UpdatedAt: image.UpdatedAt,
		ErrorMessage: image.ErrorMessage, LastAttemptAt: image.LastAttemptAt,
		FetchDurationMS: image.FetchDurationMS, IsFavorite: image.IsFavorite,
	}
}

func (s *Service) recordAttempt(ctx context.Context, attempt imageAttempt) {
	if strings.TrimSpace(attempt.SourceURL) == "" || strings.TrimSpace(attempt.ItemID) == "" {
		return
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO image_source_attempts (id, item_id, image_id, image_type, source_url, source_label, source_priority, status, error_message, duration_ms, attempted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), "img_attempt_"+fmt.Sprint(time.Now().UnixNano()), attempt.ItemID, nullableString(attempt.ImageID), attempt.ImageType, attempt.SourceURL, strings.TrimSpace(attempt.SourceLabel), attempt.SourcePriority, attempt.Status, nullableString(attempt.ErrorMessage), attempt.DurationMS, attempt.AttemptedAt)
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func parseDerivativeQuery(r *http.Request) (int, int, string, error) {
	width, err := parseDerivativeDimension(r.URL.Query().Get("width"), r.URL.Query().Get("w"))
	if err != nil {
		return 0, 0, "", err
	}
	height, err := parseDerivativeDimension(r.URL.Query().Get("height"), r.URL.Query().Get("h"))
	if err != nil {
		return 0, 0, "", err
	}
	fit := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("fit")))
	if fit == "" {
		fit = "cover"
	}
	if fit != "cover" && fit != "contain" {
		return 0, 0, "", fmt.Errorf("fit 只能是 cover 或 contain")
	}
	if width == 0 && height == 0 {
		return 0, 0, fit, nil
	}
	return width, height, fit, nil
}

func parseDerivativeDimension(primary, fallback string) (int, error) {
	value := strings.TrimSpace(primary)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if value == "" {
		return 0, nil
	}
	dimension, err := strconv.Atoi(value)
	if err != nil || dimension < 1 || dimension > maxDerivativeDimension {
		return 0, fmt.Errorf("图片尺寸必须在 1 到 %d 之间", maxDerivativeDimension)
	}
	return dimension, nil
}

func derivative(data []byte, mimeType string, sourceWidth, sourceHeight, requestedWidth, requestedHeight int, fit string) ([]byte, string, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	bounds := source.Bounds()
	if sourceWidth <= 0 {
		sourceWidth = bounds.Dx()
	}
	if sourceHeight <= 0 {
		sourceHeight = bounds.Dy()
	}
	if requestedWidth == 0 {
		requestedWidth = max(1, int(float64(requestedHeight)*float64(sourceWidth)/float64(sourceHeight)))
	}
	if requestedHeight == 0 {
		requestedHeight = max(1, int(float64(requestedWidth)*float64(sourceHeight)/float64(sourceWidth)))
	}
	crop := source
	if fit == "cover" {
		targetRatio := float64(requestedWidth) / float64(requestedHeight)
		sourceRatio := float64(sourceWidth) / float64(sourceHeight)
		if sourceRatio > targetRatio {
			cropWidth := max(1, int(float64(sourceHeight)*targetRatio))
			x := bounds.Min.X + (bounds.Dx()-cropWidth)/2
			crop = cropImage(source, image.Rect(x, bounds.Min.Y, x+cropWidth, bounds.Max.Y))
		} else if sourceRatio < targetRatio {
			cropHeight := max(1, int(float64(sourceWidth)/targetRatio))
			y := bounds.Min.Y + (bounds.Dy()-cropHeight)/2
			crop = cropImage(source, image.Rect(bounds.Min.X, y, bounds.Max.X, y+cropHeight))
		}
	} else {
		scale := minFloat(float64(requestedWidth)/float64(sourceWidth), float64(requestedHeight)/float64(sourceHeight))
		requestedWidth = max(1, int(float64(sourceWidth)*scale))
		requestedHeight = max(1, int(float64(sourceHeight)*scale))
	}
	result := image.NewRGBA(image.Rect(0, 0, requestedWidth, requestedHeight))
	draw.Draw(result, result.Bounds(), &image.Uniform{C: color.Transparent}, image.Point{}, draw.Src)
	sourceBounds := crop.Bounds()
	for y := 0; y < requestedHeight; y++ {
		for x := 0; x < requestedWidth; x++ {
			sourceX := sourceBounds.Min.X + x*sourceBounds.Dx()/requestedWidth
			sourceY := sourceBounds.Min.Y + y*sourceBounds.Dy()/requestedHeight
			result.Set(x, y, crop.At(sourceX, sourceY))
		}
	}
	var output bytes.Buffer
	outputType := mimeType
	if strings.Contains(strings.ToLower(mimeType), "png") {
		err = png.Encode(&output, result)
		outputType = "image/png"
	} else {
		err = jpeg.Encode(&output, result, &jpeg.Options{Quality: 82})
		outputType = "image/jpeg"
	}
	if err != nil {
		return nil, "", err
	}
	return output.Bytes(), outputType, nil
}

func max(first, second int) int {
	if first > second {
		return first
	}
	return second
}

func minFloat(first, second float64) float64 {
	if first < second {
		return first
	}
	return second
}

func cropImage(source image.Image, rect image.Rectangle) image.Image {
	if subImage, ok := source.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return subImage.SubImage(rect)
	}
	result := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(result, result.Bounds(), source, rect.Min, draw.Src)
	return result
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if parsed, parseErr := url.Parse(message); parseErr == nil && parsed.Scheme != "" && parsed.Host != "" {
		return "图片上游请求失败"
	}
	return strings.TrimSpace(message)
}

func (s *Service) GetImageData(ctx context.Context, itemID, imageType string) ([]byte, string, error) {
	var cacheKey, mimeType, status, sourceURL, sourceLabel, actualType string
	var sourcePriority, sortOrder int

	query := "SELECT cache_key, COALESCE(mime_type, 'application/octet-stream'), status, source_url, COALESCE(source_label, ''), source_priority, sort_order, image_type FROM item_images WHERE item_id = ? AND image_type = ? ORDER BY CASE WHEN status = 'ready' THEN 0 ELSE 1 END, sort_order, source_priority, id LIMIT 1"
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), itemID, imageType).Scan(&cacheKey, &mimeType, &status, &sourceURL, &sourceLabel, &sourcePriority, &sortOrder, &actualType)

	if errors.Is(err, sql.ErrNoRows) {
		fallbackType := ""
		if imageType == "backdrop" {
			fallbackType = "poster"
		} else if imageType == "poster" {
			fallbackType = "backdrop"
		}
		if fallbackType != "" {
			err = s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), itemID, fallbackType).Scan(&cacheKey, &mimeType, &status, &sourceURL, &sourceLabel, &sourcePriority, &sortOrder, &actualType)
		}
	}

	if errors.Is(err, sql.ErrNoRows) && (s.adapter != nil || s.modules != nil) {
		var sourceID, sourceURLVal string
		if scanErr := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COALESCE(source_id, ''), COALESCE(source_url, '') FROM media_items WHERE id = ? AND deleted_at IS NULL"), itemID).Scan(&sourceID, &sourceURLVal); scanErr == nil && (sourceID != "" || sourceURLVal != "") {
			if sourceItem, fetchErr := s.fetchItemMetadata(ctx, itemID, sourceID, sourceURLVal); fetchErr == nil && len(sourceItem.Images) > 0 {
				for idx, img := range sourceItem.Images {
					_ = s.SeedSource(ctx, itemID, img.Type, img.URL, img.Label, img.Priority, idx)
				}
				err = s.db.DB().QueryRowContext(ctx, s.db.Rebind(query), itemID, imageType).Scan(&cacheKey, &mimeType, &status, &sourceURL, &sourceLabel, &sourcePriority, &sortOrder, &actualType)
			}
		}
	}

	if err != nil {
		return s.fallbackPoster(ctx, itemID), "image/svg+xml", nil
	}

	path := filepath.Join(s.cacheDir, cacheKey)
	data, readErr := os.ReadFile(path)
	if readErr != nil || status != "ready" {
		downloaded, dlErr := s.download(ctx, itemID, actualType, sourceURL, sourceLabel, sourcePriority, sortOrder)
		if dlErr != nil {
			return s.fallbackPoster(ctx, itemID), "image/svg+xml", nil
		}
		hash := sha256.Sum256([]byte(itemID + ":" + actualType + ":" + strings.TrimSpace(downloaded.SourceURL)))
		cacheKey = hex.EncodeToString(hash[:])
		path = filepath.Join(s.cacheDir, cacheKey)
		data, readErr = os.ReadFile(path)
		if readErr != nil {
			return s.fallbackPoster(ctx, itemID), "image/svg+xml", nil
		}
		mimeType = downloaded.MimeType
	}
	return data, mimeType, nil
}

func (s *Service) visible(ctx context.Context, itemID string, user auth.User) bool {
	return auth.CanAccessMedia(ctx, s.db, user, itemID, false)
}
func (s *Service) audit(ctx context.Context, actor, itemID, requestID string, completed, failed int) {
	metadata, _ := json.Marshal(map[string]int{"completed": completed, "failed": failed})
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, 'reload_images', 'virtual_item', ?, ?, ?, ?, ?)"), "aud_"+fmt.Sprint(time.Now().UnixNano()), actor, itemID, requestID, map[bool]string{true: "partial", false: "success"}[failed > 0], string(metadata), time.Now().UTC().Format(time.RFC3339Nano))
}
func validImageType(imageType string) bool {
	switch imageType {
	case "poster", "backdrop", "gallery", "preview", "avatar":
		return true
	default:
		return false
	}
}
func validateRemoteURL(parsed *url.URL) error {
	if parsed == nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("图片地址必须是 HTTPS URL")
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return fmt.Errorf("拒绝访问私有网络图片地址")
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return fmt.Errorf("拒绝访问本地图片地址")
	}
	return nil
}
func sha256Bytes(data []byte) []byte { sum := sha256.Sum256(data); return sum[:] }
func isElevated(user auth.User) bool {
	for _, role := range user.Roles {
		if role == "Admin" || role == "Editor" {
			return true
		}
	}
	return false
}
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": middleware.GetReqID(r.Context())}})
}

func normalizeImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.Index(raw, "tp.spfcas.com/rhe951l4q"); idx != -1 {
		raw = "https://c0.jdbstatic.com" + raw[idx+len("tp.spfcas.com/rhe951l4q"):]
	}
	if idx := strings.Index(raw, "spfcas.com"); idx != -1 {
		schemeEnd := strings.Index(raw, "://")
		if schemeEnd != -1 {
			pathStart := strings.Index(raw[schemeEnd+3:], "/")
			if pathStart != -1 {
				raw = "https://c0.jdbstatic.com" + raw[schemeEnd+3+pathStart:]
			}
		}
	}
	if strings.HasPrefix(raw, "http://c0.jdbstatic.com") {
		raw = "https://c0.jdbstatic.com" + strings.TrimPrefix(raw, "http://c0.jdbstatic.com")
	}
	if strings.HasPrefix(raw, "http://jdbstatic.com") {
		raw = "https://c0.jdbstatic.com" + strings.TrimPrefix(raw, "http://jdbstatic.com")
	}
	if strings.HasPrefix(raw, "https://jdbstatic.com") {
		raw = "https://c0.jdbstatic.com" + strings.TrimPrefix(raw, "https://jdbstatic.com")
	}
	return raw
}

func detectImageMime(data []byte) (string, bool) {
	if len(data) < 4 {
		return "", false
	}
	contentType := http.DetectContentType(data)
	if strings.HasPrefix(contentType, "image/") {
		return contentType, true
	}
	if bytes.HasPrefix(data, []byte("\xff\xd8\xff")) {
		return "image/jpeg", true
	}
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png", true
	}
	if bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")) {
		return "image/gif", true
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp", true
	}
	return "", false
}

func (s *Service) fallbackPoster(ctx context.Context, itemID string) []byte {
	var code, title string
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT code,title FROM media_items WHERE id=?"), itemID).Scan(&code, &title); err != nil {
		return fallbackPosterSVG("暂无海报")
	}
	if strings.HasPrefix(code, "media:") || code == "" {
		code = title
	}
	return fallbackPosterSVG(code)
}

func fallbackPosterSVG(code string) []byte {
	if code == "" {
		code = "暂无海报"
	}
	label := []rune(code)
	if len(label) > 14 {
		code = string(label[:14]) + "…"
	}
	code = html.EscapeString(code)
	return []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="600" viewBox="0 0 400 600"><rect width="100%%" height="100%%" fill="#121826"/><rect x="20" y="20" width="360" height="560" rx="12" fill="#1e293b" stroke="#334155" stroke-width="2"/><text x="200" y="290" fill="#38bdf8" font-size="28" font-weight="bold" font-family="sans-serif" text-anchor="middle">%s</text><text x="200" y="340" fill="#94a3b8" font-size="16" font-family="sans-serif" text-anchor="middle">JMlab Media</text></svg>`, code))
}

func (s *Service) SetModuleRegistry(registry *module.Registry) { s.modules = registry }
func (s *Service) fetchItemMetadata(ctx context.Context, id, sourceID, sourceURL string) (metadata.SourceItem, error) {
	var moduleID, idsJSON string
	if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT metadata_module_id,provider_ids_json FROM media_items WHERE id=?"), id).Scan(&moduleID, &idsJSON); err != nil {
		return metadata.SourceItem{}, err
	}
	if s.modules == nil && s.adapter != nil {
		return s.adapter.Fetch(ctx, sourceID, sourceURL)
	}
	if s.modules == nil {
		return metadata.SourceItem{}, errors.New("元数据模块尚未配置")
	}
	entry, ok := s.modules.Get(moduleID)
	if !ok || entry.Status != module.StatusActive || entry.MetadataInstance == nil {
		return metadata.SourceItem{}, errors.New("元数据模块不可用")
	}
	var ids map[string]string
	_ = json.Unmarshal([]byte(idsJSON), &ids)
	detail, err := entry.MetadataInstance.GetDetail(ctx, sourceID, ids)
	if err != nil {
		return metadata.SourceItem{}, err
	}
	return metadata.DetailToSourceItem(detail), nil
}
