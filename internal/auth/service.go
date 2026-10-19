package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/repository"
)

const (
	sessionCookieName = "jmlab_session"
	sessionTTL        = 30 * 24 * time.Hour
)

type principalContextKey struct{}

type Principal struct {
	SessionID  string
	RawToken   string
	FromCookie bool
	User       User
}

type Service struct {
	store    *Store
	cfg      config.Config
	logger   *slog.Logger
	rateMu   sync.Mutex
	failures map[string]loginFailure
}

// GetPreferences exposes the current user's display preferences to other
// application services without leaking the auth store implementation.
func (s *Service) GetPreferences(ctx context.Context, userID string) (Preferences, error) {
	return s.store.GetPreferences(ctx, userID)
}

type loginFailure struct {
	count int
	last  time.Time
}

func NewService(db *repository.Database, cfg config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: NewStore(db), cfg: cfg, logger: logger, failures: make(map[string]loginFailure)}
}

func (s *Service) EnsureBootstrap(ctx context.Context) error {
	count, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count != 0 || s.cfg.BootstrapAdminUsername == "" {
		return nil
	}
	hash, err := HashPassword(s.cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	user, err := s.store.CreateUser(ctx, s.cfg.BootstrapAdminUsername, s.cfg.BootstrapAdminUsername, hash, "Admin")
	if err != nil {
		return fmt.Errorf("创建初始管理员失败: %w", err)
	}
	s.logger.Info("已创建初始管理员", "userId", user.ID, "username", user.Username)
	return nil
}

// AuthenticateCredentials creates a bearer session for Jellyfin-compatible
// clients. The regular Web login handler adds rate limiting and cookie/CSRF
// response details; this method keeps the credential/session contract reusable
// by protocol adapters without exposing the password store.
func (s *Service) AuthenticateCredentials(ctx context.Context, username, password, clientName, deviceID string) (Session, string, error) {
	user, passwordHash, err := s.store.FindUserByUsername(ctx, username)
	if err != nil || !VerifyPassword(passwordHash, password) {
		return Session{}, "", ErrInvalidCredentials
	}
	if user.Status != "active" {
		return Session{}, "", ErrDisabled
	}
	session, token, err := s.store.CreateSession(ctx, user, clientName, deviceID, sessionTTL)
	if err != nil {
		return Session{}, "", err
	}
	s.store.TouchLogin(ctx, user.ID)
	return session, token, nil
}

func (s *Service) RegisterRoutes(router chi.Router) {
	router.Post("/auth/login", s.login)
	router.With(s.RequireAuth).Post("/auth/logout", s.logout)
	router.With(s.RequireAuth).Get("/auth/me", s.me)
	router.With(s.RequireAuth).Get("/me/preferences", s.getPreferences)
	router.With(s.RequireAuth, s.requireCSRF).Patch("/me/preferences", s.patchPreferences)
	router.With(s.RequireAuth, s.RequireRole("Admin")).Get("/admin/users", s.listUsers)
	router.With(s.RequireAuth, s.RequireRole("Admin"), s.requireCSRF).Post("/admin/users", s.createUser)
	router.With(s.RequireAuth, s.RequireRole("Admin"), s.requireCSRF).Patch("/admin/users/{userID}", s.updateUser)
	router.With(s.RequireAuth, s.RequireRole("Admin"), s.requireCSRF).Delete("/admin/users/{userID}", s.deleteUser)
	router.With(s.RequireAuth, s.RequireRole("Admin"), s.requireCSRF).Post("/admin/users/{userID}/reset-password", s.resetPassword)
	router.With(s.RequireAuth, s.RequireRole("Admin"), s.requireCSRF).Post("/admin/users/{userID}/revoke-sessions", s.revokeSessions)
	router.With(s.RequireAuth, s.RequireRole("Admin"), s.requireCSRF).Put("/admin/users/{userID}/libraries", s.setLibraries)
}

func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, fromCookie := tokenFromRequest(r)
		if token == "" {
			writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
			return
		}
		session, err := s.store.GetSession(r.Context(), token)
		if err != nil {
			status := http.StatusUnauthorized
			code := "INVALID_SESSION"
			if errors.Is(err, ErrDisabled) {
				code = "USER_DISABLED"
			}
			writeError(w, r, status, code, "登录已失效，请重新登录")
			return
		}
		principal := Principal{SessionID: session.ID, RawToken: token, FromCookie: fromCookie, User: session.User}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		ctx = context.WithValue(ctx, "jmlab.user_id", session.User.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) TryAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, fromCookie := tokenFromRequest(r)
		if token != "" {
			if session, err := s.store.GetSession(r.Context(), token); err == nil {
				principal := Principal{SessionID: session.ID, RawToken: token, FromCookie: fromCookie, User: session.User}
				ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
				ctx = context.WithValue(ctx, "jmlab.user_id", session.User.ID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				writeError(w, r, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
				return
			}
			for _, role := range principal.User.Roles {
				if _, exists := allowed[role]; exists || role == "Admin" {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", "没有执行此操作的权限")
		})
	}
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func (s *Service) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || !principal.FromCookie || validCSRF(s.cfg.MasterKey, principal.RawToken, r.Header.Get("X-CSRF-Token")) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, r, http.StatusForbidden, "CSRF_REQUIRED", "缺少或无效的 CSRF 令牌")
	})
}

// RequireCSRF is exported for feature modules that register cookie-authenticated write routes.
func (s *Service) RequireCSRF(next http.Handler) http.Handler { return s.requireCSRF(next) }

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginAllowed(clientIP(r)) {
		writeError(w, r, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "登录尝试过于频繁，请稍后再试")
		return
	}
	var input struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		ClientName string `json:"clientName"`
		DeviceID   string `json:"deviceId"`
	}
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.Username) == "" || input.Password == "" {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "用户名和密码不能为空")
		return
	}
	session, token, err := s.AuthenticateCredentials(r.Context(), input.Username, input.Password, input.ClientName, input.DeviceID)
	if errors.Is(err, ErrInvalidCredentials) {
		s.recordLoginFailure(clientIP(r))
		writeError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "用户名或密码错误")
		return
	}
	if errors.Is(err, ErrDisabled) {
		writeError(w, r, http.StatusUnauthorized, "USER_DISABLED", "用户已禁用")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "SESSION_CREATE_FAILED", "创建会话失败")
		return
	}
	s.resetLoginFailures(clientIP(r))
	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"accessToken": token, "tokenType": "Bearer", "expiresAt": session.ExpiresAt.Format(time.RFC3339), "csrfToken": csrfToken(s.cfg.MasterKey, token), "user": session.User, "session": map[string]any{"id": session.ID, "clientName": session.ClientName, "deviceId": session.DeviceID, "expiresAt": session.ExpiresAt.Format(time.RFC3339)}})
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if err := s.store.RevokeSession(r.Context(), principal.SessionID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "LOGOUT_FAILED", "退出登录失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookie(), SameSite: http.SameSiteLaxMode})
	s.store.Audit(r.Context(), principal.User.ID, "logout", "session", principal.SessionID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": principal.User, "sessionId": principal.SessionID, "csrfToken": csrfToken(s.cfg.MasterKey, principal.RawToken)})
}

func (s *Service) getPreferences(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	preferences, err := s.store.GetPreferences(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "PREFERENCES_READ_FAILED", "读取偏好失败")
		return
	}
	writeJSON(w, http.StatusOK, preferences)
}

func (s *Service) patchPreferences(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	current, err := s.store.GetPreferences(r.Context(), principal.User.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "PREFERENCES_READ_FAILED", "读取偏好失败")
		return
	}
	var input struct {
		Theme          map[string]any `json:"theme"`
		ModuleFlags    map[string]any `json:"moduleFlags"`
		Locale         string         `json:"locale"`
		AdultConfirmed *bool          `json:"adultConfirmed"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "偏好格式不正确")
		return
	}
	if input.Theme != nil {
		current.Theme = input.Theme
	}
	if input.ModuleFlags != nil {
		current.ModuleFlags = input.ModuleFlags
	}
	if input.Locale != "" {
		current.Locale = input.Locale
	}
	if input.AdultConfirmed != nil {
		current.AdultConfirmedAt = nil
		if *input.AdultConfirmed {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			current.AdultConfirmedAt = &now
		}
	}
	if err := s.store.SavePreferences(r.Context(), principal.User.ID, current); err != nil {
		writeError(w, r, http.StatusInternalServerError, "PREFERENCES_WRITE_FAILED", "保存偏好失败")
		return
	}
	s.store.Audit(r.Context(), principal.User.ID, "update_preferences", "user", principal.User.ID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusOK, current)
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "USERS_READ_FAILED", "读取用户失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users})
}

func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.Username) == "" || len([]byte(input.Password)) < 6 {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "用户名不能为空且密码至少 6 个字节")
		return
	}
	if input.Role == "" {
		input.Role = "Viewer"
	}
	if input.Role != "Admin" && input.Role != "Editor" && input.Role != "Viewer" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ROLE", "角色必须是 Admin、Editor 或 Viewer")
		return
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_PASSWORD", err.Error())
		return
	}
	user, err := s.store.CreateUser(r.Context(), input.Username, input.DisplayName, hash, input.Role)
	if err != nil {
		writeError(w, r, http.StatusConflict, "USER_CREATE_FAILED", "用户名已存在或无法创建用户")
		return
	}
	s.store.Audit(r.Context(), principal.User.ID, "create_user", "user", user.ID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusCreated, user)
}

func (s *Service) updateUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	existing, err := s.store.GetUser(r.Context(), userID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "用户不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "USER_READ_FAILED", "读取用户失败")
		return
	}
	var input struct {
		DisplayName *string `json:"displayName"`
		Status      *string `json:"status"`
		Role        *string `json:"role"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "用户更新格式不正确")
		return
	}
	displayName := existing.DisplayName
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
	}
	status := existing.Status
	if input.Status != nil {
		status = *input.Status
	}
	role := "Viewer"
	if len(existing.Roles) > 0 {
		role = existing.Roles[0]
	}
	if input.Role != nil {
		role = *input.Role
	}
	if role != "Admin" && role != "Editor" && role != "Viewer" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ROLE", "角色必须是 Admin、Editor 或 Viewer")
		return
	}
	if err := s.store.SetUserStatus(r.Context(), userID, status, displayName, role); err != nil {
		writeError(w, r, http.StatusBadRequest, "USER_UPDATE_FAILED", "更新用户失败")
		return
	}
	if status == "disabled" {
		_ = s.store.RevokeUserSessions(r.Context(), userID)
	}
	updated, _ := s.store.GetUser(r.Context(), userID)
	s.store.Audit(r.Context(), principal.User.ID, "update_user", "user", userID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusOK, updated)
}

func (s *Service) resetPassword(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil || len([]byte(input.Password)) < 6 {
		writeError(w, r, http.StatusBadRequest, "INVALID_PASSWORD", "密码至少需要 6 个字节")
		return
	}
	hash, _ := HashPassword(input.Password)
	if err := s.store.UpdatePassword(r.Context(), userID, hash); err != nil {
		writeError(w, r, http.StatusNotFound, "PASSWORD_RESET_FAILED", "用户不存在或密码重置失败")
		return
	}
	_ = s.store.RevokeUserSessions(r.Context(), userID)
	s.store.Audit(r.Context(), principal.User.ID, "reset_password", "user", userID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "password_reset"})
}

func (s *Service) revokeSessions(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	if err := s.store.RevokeUserSessions(r.Context(), userID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "SESSION_REVOKE_FAILED", "撤销会话失败")
		return
	}
	s.store.Audit(r.Context(), principal.User.ID, "revoke_sessions", "user", userID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "sessions_revoked"})
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	if userID == principal.User.ID {
		writeError(w, r, http.StatusBadRequest, "CANNOT_DELETE_SELF", "无法删除当前登录的账号")
		return
	}
	existing, err := s.store.GetUser(r.Context(), userID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "用户不存在")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "USER_READ_FAILED", "读取用户失败")
		return
	}
	if existing.Username == s.cfg.BootstrapAdminUsername {
		writeError(w, r, http.StatusBadRequest, "CANNOT_DELETE_BOOTSTRAP_ADMIN", "无法删除初始管理员账号")
		return
	}
	if err := s.store.DeleteUser(r.Context(), userID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "USER_DELETE_FAILED", "删除用户失败")
		return
	}
	s.store.Audit(r.Context(), principal.User.ID, "delete_user", "user", userID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "user_deleted"})
}

func (s *Service) setLibraries(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	var input struct {
		Libraries []struct {
			ID      string `json:"id"`
			CanView bool   `json:"canView"`
			CanPlay bool   `json:"canPlay"`
		} `json:"libraries"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "媒体库授权格式不正确")
		return
	}
	permissions := make([]LibraryPermission, 0, len(input.Libraries))
	for _, library := range input.Libraries {
		permissions = append(permissions, LibraryPermission{LibraryID: library.ID, CanView: library.CanView, CanPlay: library.CanPlay})
	}
	if err := s.store.SetLibraryPermissions(r.Context(), userID, permissions); err != nil {
		writeError(w, r, http.StatusBadRequest, "LIBRARY_PERMISSION_FAILED", "保存媒体库授权失败")
		return
	}
	s.store.Audit(r.Context(), principal.User.ID, "set_library_permissions", "user", userID, middleware.GetReqID(r.Context()), "success", "{}")
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Service) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, Secure: s.secureCookie(), SameSite: http.SameSiteLaxMode})
}
func (s *Service) secureCookie() bool {
	return s.cfg.Environment == "production" || strings.HasPrefix(strings.ToLower(s.cfg.PublicURL), "https://")
}

func tokenFromRequest(r *http.Request) (string, bool) {
	if token := strings.TrimSpace(r.Header.Get("X-MediaBrowser-Token")); token != "" {
		return token, false
	}
	if token := strings.TrimSpace(r.Header.Get("X-Emby-Token")); token != "" {
		return token, false
	}
	if token := tokenFromAuthorization(r.Header.Get("X-Emby-Authorization")); token != "" {
		return token, false
	}
	if header := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:]), false
	}
	if token := tokenFromAuthorization(r.Header.Get("Authorization")); token != "" {
		return token, false
	}
	if token := strings.TrimSpace(r.URL.Query().Get("api_key")); token != "" {
		return token, false
	}
	if token := strings.TrimSpace(r.URL.Query().Get("api-key")); token != "" {
		return token, false
	}
	if token := strings.TrimSpace(r.URL.Query().Get("ApiKey")); token != "" {
		return token, false
	}
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		if !strings.HasPrefix(token, "v1:") && !strings.HasPrefix(token, "v2:") {
			return token, false
		}
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		return cookie.Value, true
	}
	return "", false
}

func tokenFromAuthorization(header string) string {
	header = strings.TrimPrefix(strings.TrimSpace(header), "MediaBrowser ")
	header = strings.TrimPrefix(header, "Emby ")
	for _, segment := range strings.Split(header, ",") {
		key, value, found := strings.Cut(segment, "=")
		trimmedKey := strings.TrimSpace(key)
		trimmedKey = strings.TrimPrefix(trimmedKey, "MediaBrowser ")
		trimmedKey = strings.TrimPrefix(trimmedKey, "Emby ")
		if !found || !strings.EqualFold(trimmedKey, "Token") {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), "\"")
	}
	return ""
}
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if index := strings.LastIndex(host, ":"); index > -1 {
		return host[:index]
	}
	return host
}
func (s *Service) loginAllowed(ip string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	failure := s.failures[ip]
	return failure.count < 5 || time.Since(failure.last) >= time.Minute
}
func (s *Service) recordLoginFailure(ip string) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	failure := s.failures[ip]
	if time.Since(failure.last) >= time.Minute {
		failure.count = 0
	}
	failure.count++
	failure.last = time.Now()
	s.failures[ip] = failure
}
func (s *Service) resetLoginFailures(ip string) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	delete(s.failures, ip)
}

func (s *Service) GetUser(ctx context.Context, userID string) (User, error) {
	return s.store.GetUser(ctx, userID)
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
