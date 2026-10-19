package userdata

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

type Service struct {
	events      *module.EventBus
	db          *repository.Database
	authService *auth.Service
}
type ItemState struct {
	UserID             string  `json:"userId"`
	ItemID             string  `json:"itemId"`
	IsFavorite         bool    `json:"isFavorite"`
	IsWatched          bool    `json:"isWatched"`
	IsWantToWatch      bool    `json:"isWantToWatch"`
	PositionTicks      int64   `json:"positionTicks"`
	RuntimeTicks       int64   `json:"runtimeTicks"`
	LastPlayedAt       *string `json:"lastPlayedAt,omitempty"`
	UserRating         *int    `json:"userRating,omitempty"`
	Note               string  `json:"note,omitempty"`
	Version            int     `json:"version"`
	UpdatedAt          string  `json:"updatedAt"`
	PlaybackSessionID  string  `json:"playbackSessionId,omitempty"`
	PlaybackDeviceID   string  `json:"playbackDeviceId,omitempty"`
	PlaybackDeviceName string  `json:"playbackDeviceName,omitempty"`
	PlaybackState      string  `json:"playbackState"`
	PlaybackPercent    float64 `json:"playbackPercent"`
	PlaybackEventAt    *string `json:"playbackEventAt,omitempty"`
}
type Playlist struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Overview    string `json:"overview,omitempty"`
	IsPublic    bool   `json:"isPublic"`
	OwnerUserID string `json:"ownerUserId"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}
type PlaylistItem struct {
	ItemID        string `json:"itemId"`
	Position      int    `json:"position"`
	AddedByUserID string `json:"addedByUserId"`
	CreatedAt     string `json:"createdAt"`
	Title         string `json:"title"`
	ItemType      string `json:"itemType"`
	Year          *int   `json:"year,omitempty"`
}

func NewService(db *repository.Database, authService *auth.Service) *Service {
	return &Service{db: db, authService: authService}
}

func (s *Service) RegisterRoutes(router chi.Router) {
	router.With(s.authService.RequireAuth).Get("/me/items/{itemID}/state", s.getState)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Put("/me/items/{itemID}/state", s.putState)
	router.With(s.authService.RequireAuth).Get("/me/people/favorites", s.favoritePeople)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Post("/me/people/{personID}/favorite", s.favoritePerson)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Delete("/me/people/{personID}/favorite", s.unfavoritePerson)
	router.With(s.authService.RequireAuth).Get("/me/images/favorites", s.favoriteImages)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Post("/me/images/{imageID}/favorite", s.favoriteImage)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Delete("/me/images/{imageID}/favorite", s.unfavoriteImage)
	router.With(s.authService.RequireAuth).Get("/playlists", s.listPlaylists)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Post("/playlists", s.createPlaylist)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Patch("/playlists/{playlistID}", s.updatePlaylist)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Delete("/playlists/{playlistID}", s.deletePlaylist)
	router.With(s.authService.RequireAuth).Get("/playlists/{playlistID}/items", s.listPlaylistItems)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Post("/playlists/{playlistID}/items", s.addPlaylistItem)
	router.With(s.authService.RequireAuth, s.authService.RequireCSRF).Delete("/playlists/{playlistID}/items/{itemID}", s.removePlaylistItem)
}

func (s *Service) GetItemState(ctx context.Context, userID, itemID string) (ItemState, error) {
	state, err := scanItemState(ctx, s.db.DB(), s.db.Rebind(stateQuery), userID, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemState{UserID: userID, ItemID: itemID, Version: 0, PlaybackState: "stopped"}, nil
	}
	if err != nil {
		return ItemState{}, err
	}
	return state, nil
}

type stateScanner interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const stateQuery = "SELECT user_id, item_id, is_favorite, is_watched, is_want_to_watch, playback_position_ticks, playback_runtime_ticks, last_played_at, user_rating, note, version, updated_at, COALESCE(playback_session_id, ''), COALESCE(playback_device_id, ''), COALESCE(playback_device_name, ''), COALESCE(playback_state, 'stopped'), COALESCE(playback_percent, 0), playback_event_at FROM user_item_states WHERE user_id = ? AND item_id = ?"

func scanItemState(ctx context.Context, db stateScanner, query, userID, itemID string) (ItemState, error) {
	var state ItemState
	var favorite, watched, want int
	var last, note sql.NullString
	var rating sql.NullInt64
	var playbackEventAt sql.NullString
	err := db.QueryRowContext(ctx, query, userID, itemID).Scan(&state.UserID, &state.ItemID, &favorite, &watched, &want, &state.PositionTicks, &state.RuntimeTicks, &last, &rating, &note, &state.Version, &state.UpdatedAt, &state.PlaybackSessionID, &state.PlaybackDeviceID, &state.PlaybackDeviceName, &state.PlaybackState, &state.PlaybackPercent, &playbackEventAt)
	if err != nil {
		return ItemState{}, err
	}
	state.IsFavorite, state.IsWatched, state.IsWantToWatch = favorite == 1, watched == 1, want == 1
	if last.Valid {
		state.LastPlayedAt = &last.String
	}
	if rating.Valid {
		value := int(rating.Int64)
		state.UserRating = &value
	}
	state.Note = note.String
	if playbackEventAt.Valid {
		state.PlaybackEventAt = &playbackEventAt.String
	}
	return state, nil
}

func (s *Service) UpsertItemState(ctx context.Context, userID, sessionID, itemID string, input StateInput) (ItemState, error) {
	if input.Rating != nil && (*input.Rating < 0 || *input.Rating > 10) {
		return ItemState{}, fmt.Errorf("评分必须在 0 到 10 之间")
	}
	if input.Note != nil && len([]rune(*input.Note)) > 2000 {
		return ItemState{}, fmt.Errorf("备注不能超过 2000 个字符")
	}
	current, err := s.GetItemState(ctx, userID, itemID)
	if err != nil {
		return ItemState{}, err
	}
	if input.Version != nil && *input.Version != current.Version {
		return ItemState{}, fmt.Errorf("状态版本冲突")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	nextVersion := current.Version + 1
	lastPlayed := current.LastPlayedAt
	if input.PositionTicks != nil {
		value := now
		lastPlayed = &value
	}
	rating := any(nil)
	if input.Rating != nil {
		rating = *input.Rating
	}
	if input.Rating == nil && !input.ratingPresent && current.UserRating != nil {
		rating = *current.UserRating
	}
	note := current.Note
	if input.Note != nil {
		note = *input.Note
	}
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO user_item_states (user_id, item_id, is_favorite, is_watched, is_want_to_watch, playback_position_ticks, playback_runtime_ticks, last_played_at, user_rating, note, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(user_id, item_id) DO UPDATE SET is_favorite = excluded.is_favorite, is_watched = excluded.is_watched, is_want_to_watch = excluded.is_want_to_watch, playback_position_ticks = excluded.playback_position_ticks, playback_runtime_ticks = excluded.playback_runtime_ticks, last_played_at = excluded.last_played_at, user_rating = excluded.user_rating, note = excluded.note, version = excluded.version, updated_at = excluded.updated_at"), userID, itemID, boolInt(valueOr(input.Favorite, current.IsFavorite)), boolInt(valueOr(input.Watched, current.IsWatched)), boolInt(valueOr(input.WantToWatch, current.IsWantToWatch)), valueOrInt64(input.PositionTicks, current.PositionTicks), valueOrInt64(input.RuntimeTicks, current.RuntimeTicks), nullable(lastPlayed), rating, note, nextVersion, now, now)
	if err != nil {
		return ItemState{}, err
	}
	eventType := "progress"
	if input.Favorite != nil {
		eventType = "favorite"
	}
	if input.Watched != nil {
		eventType = "watched"
	}
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO user_item_state_events (id, user_id, item_id, session_id, event_type, position_ticks, runtime_ticks, version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), newID("evt"), userID, itemID, nullableString(sessionID), eventType, valueOrInt64(input.PositionTicks, current.PositionTicks), valueOrInt64(input.RuntimeTicks, current.RuntimeTicks), nextVersion, now)
	s.emitState(ctx, "user.state.updated", userID, itemID, nextVersion)
	return s.GetItemState(ctx, userID, itemID)
}

type StateInput struct {
	ratingPresent bool
	Favorite      *bool   `json:"favorite"`
	Watched       *bool   `json:"watched"`
	WantToWatch   *bool   `json:"wantToWatch"`
	PositionTicks *int64  `json:"positionTicks"`
	RuntimeTicks  *int64  `json:"runtimeTicks"`
	Rating        *int    `json:"rating"`
	Note          *string `json:"note"`
	Version       *int    `json:"version"`
}

type PlaybackInput struct {
	EventType         string
	SessionID         string
	PlaybackSessionID string
	DeviceID          string
	DeviceName        string
	Paused            bool
	PositionTicks     int64
	RuntimeTicks      int64
	Version           *int
	EventAt           time.Time
}

type PlaybackResult struct {
	State    ItemState
	Accepted bool
	Stale    bool
}

// UpsertPlayback applies a playback event only when it is newer than the
// stored event and, when supplied, based on the caller's state version.
func (s *Service) UpsertPlayback(ctx context.Context, userID, itemID string, input PlaybackInput) (PlaybackResult, error) {
	eventType := strings.ToLower(strings.TrimSpace(input.EventType))
	if eventType != "playback_start" && eventType != "progress" && eventType != "playback_stop" {
		return PlaybackResult{}, fmt.Errorf("播放事件类型无效")
	}
	if input.PositionTicks < 0 || input.RuntimeTicks < 0 {
		return PlaybackResult{}, fmt.Errorf("播放进度不能为负数")
	}
	if len([]rune(input.SessionID)) > 160 || len([]rune(input.PlaybackSessionID)) > 160 || len([]rune(input.DeviceID)) > 160 || len([]rune(input.DeviceName)) > 160 {
		return PlaybackResult{}, fmt.Errorf("播放会话或设备信息过长")
	}
	eventAt := input.EventAt.UTC()
	if eventAt.IsZero() {
		eventAt = time.Now().UTC()
	}
	if eventAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		return PlaybackResult{}, fmt.Errorf("播放事件时间不能晚于当前时间")
	}

	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return PlaybackResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanItemState(ctx, tx, s.db.Rebind(stateQuery), userID, itemID)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return PlaybackResult{}, err
	}
	if missing {
		current = ItemState{UserID: userID, ItemID: itemID, PlaybackState: "stopped"}
	}
	if input.Version != nil && *input.Version != current.Version {
		return PlaybackResult{}, fmt.Errorf("状态版本冲突")
	}
	if current.PlaybackEventAt != nil {
		if previous, parseErr := time.Parse(time.RFC3339Nano, *current.PlaybackEventAt); parseErr == nil && !eventAt.After(previous) {
			return PlaybackResult{State: current, Accepted: false, Stale: true}, nil
		}
	}
	runtimeTicks := input.RuntimeTicks
	if runtimeTicks == 0 {
		runtimeTicks = current.RuntimeTicks
	}
	positionTicks := input.PositionTicks
	if runtimeTicks > 0 && positionTicks > runtimeTicks {
		positionTicks = runtimeTicks
	}
	percent := float64(0)
	if runtimeTicks > 0 {
		percent = float64(positionTicks) * 100 / float64(runtimeTicks)
		if percent > 100 {
			percent = 100
		}
	}
	watched := current.IsWatched || (runtimeTicks > 0 && percent >= 90)
	playbackState := "playing"
	if eventType == "playback_stop" {
		playbackState = "stopped"
	} else if input.Paused {
		playbackState = "paused"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	eventAtText := eventAt.Format(time.RFC3339Nano)
	nextVersion := current.Version + 1
	result, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_item_states (user_id, item_id, is_favorite, is_watched, is_want_to_watch, playback_position_ticks, playback_runtime_ticks, last_played_at, user_rating, note, version, created_at, updated_at, playback_session_id, playback_device_id, playback_device_name, playback_state, playback_percent, playback_event_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(user_id, item_id) DO UPDATE SET is_watched = excluded.is_watched, playback_position_ticks = excluded.playback_position_ticks, playback_runtime_ticks = excluded.playback_runtime_ticks, last_played_at = excluded.last_played_at, version = excluded.version, updated_at = excluded.updated_at, playback_session_id = excluded.playback_session_id, playback_device_id = excluded.playback_device_id, playback_device_name = excluded.playback_device_name, playback_state = excluded.playback_state, playback_percent = excluded.playback_percent, playback_event_at = excluded.playback_event_at WHERE user_item_states.version = ? AND (user_item_states.playback_event_at IS NULL OR user_item_states.playback_event_at < excluded.playback_event_at)"), userID, itemID, boolInt(current.IsFavorite), boolInt(watched), boolInt(current.IsWantToWatch), positionTicks, runtimeTicks, eventAtText, nullableInt(current.UserRating), current.Note, nextVersion, now, now, nullableString(input.PlaybackSessionID), nullableString(input.DeviceID), nullableString(input.DeviceName), playbackState, percent, eventAtText, current.Version)
	if err != nil {
		return PlaybackResult{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		latest, readErr := scanItemState(ctx, tx, s.db.Rebind(stateQuery), userID, itemID)
		if readErr == nil && latest.PlaybackEventAt != nil {
			if previous, parseErr := time.Parse(time.RFC3339Nano, *latest.PlaybackEventAt); parseErr == nil && !eventAt.After(previous) {
				return PlaybackResult{State: latest, Accepted: false, Stale: true}, nil
			}
		}
		return PlaybackResult{}, fmt.Errorf("状态版本冲突")
	}
	if _, err = tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_item_state_events (id, user_id, item_id, session_id, event_type, position_ticks, runtime_ticks, version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), newID("evt"), userID, itemID, nullableString(input.SessionID), eventType, positionTicks, runtimeTicks, nextVersion, eventAtText); err != nil {
		return PlaybackResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlaybackResult{}, err
	}
	topic := "user.state.updated"
	if eventType == "playback_start" {
		topic = "playback.started"
	} else if eventType == "playback_stop" {
		topic = "playback.stopped"
	}
	s.emitState(ctx, topic, userID, itemID, nextVersion)
	state, err := s.GetItemState(ctx, userID, itemID)
	if err != nil {
		return PlaybackResult{}, err
	}
	return PlaybackResult{State: state, Accepted: true}, nil
}

func unescapeParam(r *http.Request, key string) string {
	val := chi.URLParam(r, key)
	if unescaped, err := url.PathUnescape(val); err == nil {
		return unescaped
	}
	return val
}

func (s *Service) getState(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.visibleItem(r.Context(), principal.User, unescapeParam(r, "itemID")) {
		writeError(w, r, 404, "ITEM_NOT_FOUND", "作品不存在或当前账号不可见")
		return
	}
	state, err := s.GetItemState(r.Context(), principal.User.ID, unescapeParam(r, "itemID"))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "STATE_READ_FAILED", "读取用户状态失败")
		return
	}
	writeJSON(w, http.StatusOK, state)
}
func (s *Service) putState(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	if !s.visibleItem(r.Context(), principal.User, unescapeParam(r, "itemID")) {
		writeError(w, r, 404, "ITEM_NOT_FOUND", "作品不存在或当前账号不可见")
		return
	}
	var input StateInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_STATE", "用户状态格式不正确")
		return
	}
	state, err := s.UpsertItemState(r.Context(), principal.User.ID, principal.SessionID, unescapeParam(r, "itemID"), input)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "冲突") {
			status = http.StatusConflict
		}
		writeError(w, r, status, "STATE_WRITE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Service) favoritePeople(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	visibility, accessArgs := auth.MediaVisibilitySQL("vi", principal.User.ID, auth.Elevated(principal.User), false)
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT p.id, p.name FROM people p JOIN user_person_states ups ON ups.person_id=p.id AND ups.user_id=? AND ups.is_favorite=1 WHERE EXISTS (SELECT 1 FROM item_people ip JOIN media_items vi ON vi.id=ip.item_id WHERE ip.person_id=p.id AND ("+visibility+")) ORDER BY p.name"), append([]any{principal.User.ID}, accessArgs...)...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "FAVORITES_READ_FAILED", "读取收藏演员失败")
		return
	}
	defer rows.Close()
	values := make([]map[string]string, 0)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			writeError(w, r, http.StatusInternalServerError, "FAVORITES_READ_FAILED", "读取收藏人物失败")
			return
		}
		values = append(values, map[string]string{"id": id, "name": name})
	}
	if rows.Err() != nil {
		writeError(w, r, http.StatusInternalServerError, "FAVORITES_READ_FAILED", "读取收藏人物失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}
func (s *Service) favoritePerson(w http.ResponseWriter, r *http.Request) {
	s.setPersonFavorite(w, r, true)
}
func (s *Service) unfavoritePerson(w http.ResponseWriter, r *http.Request) {
	s.setPersonFavorite(w, r, false)
}
func (s *Service) setPersonFavorite(w http.ResponseWriter, r *http.Request, favorite bool) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	id := chi.URLParam(r, "personID")
	if favorite {
		visibility, accessArgs := auth.MediaVisibilitySQL("vi", principal.User.ID, auth.Elevated(principal.User), false)
		var count int
		err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM people p WHERE p.id=? AND EXISTS (SELECT 1 FROM item_people ip JOIN media_items vi ON vi.id=ip.item_id WHERE ip.person_id=p.id AND ("+visibility+"))"), append([]any{id}, accessArgs...)...).Scan(&count)
		if err != nil || count == 0 {
			writeError(w, r, http.StatusNotFound, "PERSON_NOT_FOUND", "人物不存在或当前账号不可见")
			return
		}
	} else {
		if _, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM user_person_states WHERE user_id=? AND person_id=?"), principal.User.ID, id); err != nil {
			writeError(w, r, http.StatusInternalServerError, "FAVORITE_WRITE_FAILED", "取消人物收藏失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"favorite": false})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO user_person_states (user_id, person_id, is_favorite, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(user_id, person_id) DO UPDATE SET is_favorite = excluded.is_favorite, updated_at = excluded.updated_at"), principal.User.ID, chi.URLParam(r, "personID"), boolInt(favorite), now, now)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "FAVORITE_WRITE_FAILED", "更新演员收藏失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"favorite": favorite})
}
func (s *Service) favoriteImages(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	visibility, accessArgs := auth.MediaVisibilitySQL("vi", principal.User.ID, auth.Elevated(principal.User), false)
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT i.id, i.item_id, i.image_type, COALESCE(i.source_label, ''), COALESCE(i.mime_type, 'application/octet-stream'), COALESCE(i.width, 0), COALESCE(i.height, 0), i.updated_at FROM item_images i JOIN user_image_states uis ON uis.image_id=i.id AND uis.user_id=? AND uis.is_favorite=1 JOIN media_items vi ON vi.id=i.item_id WHERE i.status='ready' AND ("+visibility+") ORDER BY i.updated_at DESC"), append([]any{principal.User.ID}, accessArgs...)...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "FAVORITES_READ_FAILED", "读取收藏图片失败")
		return
	}
	defer rows.Close()
	values := make([]map[string]any, 0)
	for rows.Next() {
		var id, itemID, imageType, sourceLabel, mimeType, updatedAt string
		var width, height int
		if err := rows.Scan(&id, &itemID, &imageType, &sourceLabel, &mimeType, &width, &height, &updatedAt); err != nil {
			writeError(w, r, http.StatusInternalServerError, "FAVORITES_READ_FAILED", "读取收藏图片失败")
			return
		}
		values = append(values, map[string]any{"id": id, "itemId": itemID, "imageType": imageType, "url": "/api/v1/items/" + url.PathEscape(itemID) + "/images/" + url.PathEscape(id) + "/content", "sourceLabel": sourceLabel, "mimeType": mimeType, "width": width, "height": height, "isFavorite": true, "updatedAt": updatedAt})
	}
	if rows.Err() != nil {
		writeError(w, r, http.StatusInternalServerError, "FAVORITES_READ_FAILED", "读取收藏图片失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}

func (s *Service) favoriteImage(w http.ResponseWriter, r *http.Request) {
	s.setImageFavorite(w, r, true)
}

func (s *Service) unfavoriteImage(w http.ResponseWriter, r *http.Request) {
	s.setImageFavorite(w, r, false)
}

func (s *Service) setImageFavorite(w http.ResponseWriter, r *http.Request, favorite bool) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	imageID := chi.URLParam(r, "imageID")
	var itemID string
	err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT item_id FROM item_images WHERE id = ? AND status = 'ready'"), imageID).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在或当前账号不可见")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "FAVORITE_WRITE_FAILED", "读取图片权限失败")
		return
	}
	if !s.visibleItem(r.Context(), principal.User, itemID) {
		writeError(w, r, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在或当前账号不可见")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO user_image_states (user_id, image_id, is_favorite, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(user_id, image_id) DO UPDATE SET is_favorite = excluded.is_favorite, updated_at = excluded.updated_at"), principal.User.ID, imageID, boolInt(favorite), now, now)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "FAVORITE_WRITE_FAILED", "更新图片收藏失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imageId": imageID, "itemId": itemID, "favorite": favorite})
}

func (s *Service) visibleItem(ctx context.Context, user auth.User, itemID string) bool {
	return auth.CanAccessMedia(ctx, s.db, user, itemID, false)
}

func (s *Service) listPlaylists(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT id, name, COALESCE(overview, ''), is_public, owner_user_id, created_at, updated_at FROM playlists WHERE owner_user_id = ? OR is_public = 1 ORDER BY updated_at DESC"), principal.User.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "PLAYLISTS_READ_FAILED", "读取播放列表失败")
		return
	}
	defer rows.Close()
	playlists := make([]Playlist, 0)
	for rows.Next() {
		var playlist Playlist
		var public int
		if rows.Scan(&playlist.ID, &playlist.Name, &playlist.Overview, &public, &playlist.OwnerUserID, &playlist.CreatedAt, &playlist.UpdatedAt) == nil {
			playlist.IsPublic = public == 1
			playlists = append(playlists, playlist)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": playlists})
}
func (s *Service) createPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	var input struct {
		Name     string `json:"name"`
		Overview string `json:"overview"`
		IsPublic bool   `json:"isPublic"`
	}
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.Name) == "" || len([]rune(input.Name)) > 100 {
		writeError(w, r, http.StatusBadRequest, "INVALID_PLAYLIST", "清单名称需要填写，且不能超过 100 个字")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	playlist := Playlist{ID: newID("pl"), Name: strings.TrimSpace(input.Name), Overview: strings.TrimSpace(input.Overview), IsPublic: input.IsPublic, OwnerUserID: principal.User.ID, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO playlists (id, owner_user_id, name, overview, is_public, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)"), playlist.ID, playlist.OwnerUserID, playlist.Name, playlist.Overview, boolInt(playlist.IsPublic), now, now)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "PLAYLIST_CREATE_FAILED", "创建播放列表失败")
		return
	}
	writeJSON(w, http.StatusCreated, playlist)
}
func (s *Service) updatePlaylist(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	playlistID := chi.URLParam(r, "playlistID")
	var input struct {
		Name     *string `json:"name"`
		Overview *string `json:"overview"`
		IsPublic *bool   `json:"isPublic"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_PLAYLIST", "播放列表格式不正确")
		return
	}
	updates := make([]string, 0)
	args := make([]any, 0)
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" || len([]rune(*input.Name)) > 100 {
			writeError(w, r, 400, "INVALID_PLAYLIST", "清单名称需要填写，且不能超过 100 个字")
			return
		}
		updates = append(updates, "name = ?")
		args = append(args, strings.TrimSpace(*input.Name))
	}
	if input.Overview != nil {
		updates = append(updates, "overview = ?")
		args = append(args, strings.TrimSpace(*input.Overview))
	}
	if input.IsPublic != nil {
		updates = append(updates, "is_public = ?")
		args = append(args, boolInt(*input.IsPublic))
	}
	if len(updates) == 0 {
		writeError(w, r, http.StatusBadRequest, "NO_CHANGES", "没有要更新的字段")
		return
	}
	updates = append(updates, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano), playlistID, principal.User.ID)
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("UPDATE playlists SET "+strings.Join(updates, ", ")+" WHERE id = ? AND owner_user_id = ?"), args...)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "PLAYLIST_UPDATE_FAILED", "更新播放列表失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "播放列表不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
func (s *Service) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM playlists WHERE id = ? AND owner_user_id = ?"), chi.URLParam(r, "playlistID"), principal.User.ID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "PLAYLIST_DELETE_FAILED", "删除播放列表失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "播放列表不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
func (s *Service) canEditPlaylist(ctx context.Context, userID, playlistID string) bool {
	var count int
	return s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM playlists WHERE id = ? AND owner_user_id = ?"), playlistID, userID).Scan(&count) == nil && count == 1
}
func (s *Service) listPlaylistItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	playlistID := chi.URLParam(r, "playlistID")
	var count int
	if s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*) FROM playlists WHERE id = ? AND (owner_user_id = ? OR is_public = 1)"), playlistID, principal.User.ID).Scan(&count) != nil || count != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "播放列表不存在")
		return
	}
	visibility, args := auth.MediaVisibilitySQL("vi", principal.User.ID, auth.Elevated(principal.User), false)
	args = append([]any{playlistID}, args...)
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT pi.item_id, pi.position, pi.added_by_user_id, pi.created_at, vi.title, vi.item_type, vi.year FROM playlist_items pi JOIN media_items vi ON vi.id = pi.item_id WHERE pi.playlist_id = ? AND "+visibility+" ORDER BY pi.position, pi.created_at, pi.item_id"), args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "PLAYLIST_ITEMS_READ_FAILED", "读取播放列表内容失败")
		return
	}
	defer rows.Close()
	items := make([]PlaylistItem, 0)
	for rows.Next() {
		var item PlaylistItem
		if rows.Scan(&item.ItemID, &item.Position, &item.AddedByUserID, &item.CreatedAt, &item.Title, &item.ItemType, &item.Year) == nil {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Service) addPlaylistItem(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	playlistID := chi.URLParam(r, "playlistID")
	if !s.canEditPlaylist(r.Context(), principal.User.ID, playlistID) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "播放列表不存在")
		return
	}
	var input struct {
		ItemID   string `json:"itemId"`
		Position *int   `json:"position"`
	}
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.ItemID) == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_PLAYLIST_ITEM", "itemId 不能为空")
		return
	}
	if !s.visibleItem(r.Context(), principal.User, input.ItemID) {
		writeError(w, r, 404, "ITEM_NOT_FOUND", "作品不存在或当前账号不可见")
		return
	}
	if input.Position != nil && *input.Position < 0 {
		writeError(w, r, 400, "INVALID_POSITION", "清单位置不能小于零")
		return
	}
	position := 0
	if input.Position != nil {
		position = *input.Position
	} else {
		_ = s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT COALESCE(MAX(position), -1) + 1 FROM playlist_items WHERE playlist_id = ?"), playlistID).Scan(&position)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("INSERT INTO playlist_items (playlist_id, item_id, position, added_by_user_id, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(playlist_id, item_id) DO UPDATE SET position = excluded.position"), playlistID, input.ItemID, position, principal.User.ID, now)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "PLAYLIST_ITEM_WRITE_FAILED", "添加播放列表内容失败")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"playlistId": playlistID, "itemId": input.ItemID, "position": position})
}
func (s *Service) removePlaylistItem(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	playlistID, itemID := chi.URLParam(r, "playlistID"), unescapeParam(r, "itemID")
	if !s.canEditPlaylist(r.Context(), principal.User.ID, playlistID) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "播放列表不存在")
		return
	}
	result, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM playlist_items WHERE playlist_id = ? AND item_id = ?"), playlistID, itemID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "PLAYLIST_ITEM_DELETE_FAILED", "移除播放列表内容失败")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "播放列表内容不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func valueOr(value *bool, fallback bool) bool {
	if value != nil {
		return *value
	}
	return fallback
}
func valueOrInt64(value *int64, fallback int64) int64 {
	if value != nil {
		return *value
	}
	return fallback
}
func nullable(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return *value
}
func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
func newID(prefix string) string { return prefix + "_" + rand.Text() }
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
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
