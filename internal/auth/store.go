package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/graysui/JMlab/internal/repository"
)

var (
	ErrNotFound           = errors.New("资源不存在")
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrDisabled           = errors.New("用户已禁用")
)

type User struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Status      string   `json:"status"`
	IsAdmin     bool     `json:"isAdmin"`
	Roles       []string `json:"roles"`
	CreatedAt   string   `json:"createdAt,omitempty"`
}

type Session struct {
	ID         string
	Token      string
	User       User
	ExpiresAt  time.Time
	ClientName string
	DeviceID   string
}

type Preferences struct {
	Theme            map[string]any `json:"theme"`
	ModuleFlags      map[string]any `json:"moduleFlags"`
	Locale           string         `json:"locale"`
	AdultConfirmedAt *string        `json:"adultConfirmedAt,omitempty"`
}

type LibraryPermission struct {
	LibraryID string
	CanView   bool
	CanPlay   bool
}

type Store struct{ db *repository.Database }

func NewStore(db *repository.Database) *Store { return &Store{db: db} }

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM users")).Scan(&count)
	return count, err
}

func (s *Store) CreateUser(ctx context.Context, username, displayName, passwordHash, role string) (User, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	user := User{ID: newID("usr"), Username: strings.TrimSpace(username), DisplayName: strings.TrimSpace(displayName), Status: "active", Roles: []string{role}, CreatedAt: now}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("开始用户事务失败: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO users (id, username, display_name, password_hash, status, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, 'active', ?, ?, ?)"), user.ID, user.Username, user.DisplayName, passwordHash, boolInt(role == "Admin"), now, now); err != nil {
		return User{}, err
	}
	var roleID string
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM roles WHERE name = ?"), role).Scan(&roleID); err != nil {
		return User{}, fmt.Errorf("角色不存在: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)"), user.ID, roleID, now); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_preferences (user_id, created_at, updated_at) VALUES (?, ?, ?)"), user.ID, now, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("提交用户事务失败: %w", err)
	}
	return user, nil
}

func (s *Store) FindUserByUsername(ctx context.Context, username string) (User, string, error) {
	var user User
	var hash string
	var isAdmin int
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, username, display_name, password_hash, status, is_admin, created_at FROM users WHERE LOWER(username) = LOWER(?)"), strings.TrimSpace(username)).Scan(&user.ID, &user.Username, &user.DisplayName, &hash, &user.Status, &isAdmin, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return User{}, "", fmt.Errorf("读取用户失败: %w", err)
	}
	user.IsAdmin = isAdmin == 1
	roles, err := s.rolesForUser(ctx, user.ID)
	if err != nil {
		return User{}, "", err
	}
	if roles == nil {
		roles = []string{}
	}
	if user.IsAdmin {
		hasAdmin := false
		for _, r := range roles {
			if r == "Admin" {
				hasAdmin = true
				break
			}
		}
		if !hasAdmin {
			roles = append(roles, "Admin")
		}
	}
	user.Roles = roles
	return user, hash, nil
}

func (s *Store) rolesForUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id = r.id WHERE ur.user_id = ? ORDER BY r.name"), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s *Store) GetUser(ctx context.Context, userID string) (User, error) {
	var user User
	var isAdmin int
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id, username, display_name, status, is_admin, created_at FROM users WHERE id = ?"), userID).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &isAdmin, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("读取用户失败: %w", err)
	}
	user.IsAdmin = isAdmin == 1
	roles, err := s.rolesForUser(ctx, user.ID)
	if err != nil {
		return User{}, err
	}
	if roles == nil {
		roles = []string{}
	}
	if user.IsAdmin {
		hasAdmin := false
		for _, r := range roles {
			if r == "Admin" {
				hasAdmin = true
				break
			}
		}
		if !hasAdmin {
			roles = append(roles, "Admin")
		}
	}
	user.Roles = roles
	return user, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.DB().QueryContext(ctx, s.db.Rebind("SELECT id, username, display_name, status, is_admin, created_at FROM users ORDER BY created_at, username"))
	if err != nil {
		return nil, err
	}
	var users []User
	for rows.Next() {
		var user User
		var isAdmin int
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &isAdmin, &user.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		user.IsAdmin = isAdmin == 1
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range users {
		roles, err := s.rolesForUser(ctx, users[index].ID)
		if err != nil {
			return nil, err
		}
		if roles == nil {
			roles = []string{}
		}
		if users[index].IsAdmin {
			hasAdmin := false
			for _, r := range roles {
				if r == "Admin" {
					hasAdmin = true
					break
				}
			}
			if !hasAdmin {
				roles = append(roles, "Admin")
			}
		}
		users[index].Roles = roles
	}
	return users, nil
}

func (s *Store) SetUserStatus(ctx context.Context, userID, status, displayName, role string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("用户状态不合法")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, s.db.Rebind("UPDATE users SET status = ?, display_name = ?, is_admin = ?, updated_at = ? WHERE id = ?"), status, strings.TrimSpace(displayName), boolInt(role == "Admin"), now, userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	var roleID string
	if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT id FROM roles WHERE name = ?"), role).Scan(&roleID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM user_roles WHERE user_id = ?"), userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)"), userID, roleID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	result, err := s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?"), passwordHash, time.Now().UTC().Format(time.RFC3339Nano), userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// added_by_user_id is RESTRICT; all other account references have explicit
	// CASCADE/SET NULL rules. Do not run best-effort queries against legacy names:
	// PostgreSQL aborts the entire transaction after even one failed statement.
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM playlist_items WHERE added_by_user_id = ? OR playlist_id IN (SELECT id FROM playlists WHERE owner_user_id = ?)"), userID, userID); err != nil {
		return fmt.Errorf("清理用户播放列表失败: %w", err)
	}

	res, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM users WHERE id = ?"), userID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) TouchLogin(ctx context.Context, userID string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?"), time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), userID)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateSession(ctx context.Context, user User, clientName, deviceID string, ttl time.Duration) (Session, string, error) {
	raw, err := randomToken()
	if err != nil {
		return Session{}, "", err
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)
	session := Session{ID: newID("ses"), Token: raw, User: user, ExpiresAt: expires, ClientName: clientName, DeviceID: deviceID}
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO sessions (id, token_hash, user_id, client_name, device_id, expires_at, last_seen_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"), session.ID, hashToken(raw), user.ID, clientName, deviceID, expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return session, raw, err
}

func (s *Store) GetSession(ctx context.Context, token string) (Session, error) {
	var session Session
	var user User
	var isAdmin int
	var expires string
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT s.id, s.user_id, s.client_name, s.device_id, s.expires_at, u.username, u.display_name, u.status, u.is_admin, u.created_at FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.revoked_at IS NULL AND s.expires_at > ?"), hashToken(token), time.Now().UTC().Format(time.RFC3339Nano)).Scan(&session.ID, &user.ID, &session.ClientName, &session.DeviceID, &expires, &user.Username, &user.DisplayName, &user.Status, &isAdmin, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if subtle.ConstantTimeCompare([]byte(user.Status), []byte("active")) != 1 {
		return Session{}, ErrDisabled
	}
	session.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return Session{}, err
	}
	user.IsAdmin = isAdmin == 1
	user.Roles, err = s.rolesForUser(ctx, user.ID)
	if err != nil {
		return Session{}, err
	}
	session.User = user
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE sessions SET last_seen_at = ? WHERE id = ?"), time.Now().UTC().Format(time.RFC3339Nano), session.ID)
	return session, nil
}

func (s *Store) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL"), time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	return err
}
func (s *Store) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.DB().ExecContext(ctx, s.db.Rebind("UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL"), time.Now().UTC().Format(time.RFC3339Nano), userID)
	return err
}

func (s *Store) GetPreferences(ctx context.Context, userID string) (Preferences, error) {
	var theme, flags, locale string
	var adult sql.NullString
	err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT theme_json, module_flags_json, locale, adult_confirmed_at FROM user_preferences WHERE user_id = ?"), userID).Scan(&theme, &flags, &locale, &adult)
	if errors.Is(err, sql.ErrNoRows) {
		return Preferences{Theme: map[string]any{}, ModuleFlags: map[string]any{}, Locale: "zh-CN"}, nil
	}
	if err != nil {
		return Preferences{}, err
	}
	return decodePreferences(theme, flags, locale, adult.Valid, adult.String), nil
}

func (s *Store) SavePreferences(ctx context.Context, userID string, preferences Preferences) error {
	theme, flags, err := encodePreferences(preferences)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	adult := any(nil)
	if preferences.AdultConfirmedAt != nil {
		adult = *preferences.AdultConfirmedAt
	}
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO user_preferences (user_id, theme_json, module_flags_json, locale, adult_confirmed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET theme_json = excluded.theme_json, module_flags_json = excluded.module_flags_json, locale = excluded.locale, adult_confirmed_at = excluded.adult_confirmed_at, updated_at = excluded.updated_at"), userID, theme, flags, preferences.Locale, adult, now, now)
	return err
}

func (s *Store) Audit(ctx context.Context, actorUserID, action, resourceType, resourceID, requestID, result, metadata string) {
	_, _ = s.db.DB().ExecContext(ctx, s.db.Rebind("INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"), newID("aud"), actorUserID, action, resourceType, resourceID, requestID, result, metadata, time.Now().UTC().Format(time.RFC3339Nano))
}

func (s *Store) SetLibraryPermissions(ctx context.Context, userID string, permissions []LibraryPermission) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM user_library_permissions WHERE user_id = ?"), userID); err != nil {
		return err
	}
	for _, permission := range permissions {
		if strings.TrimSpace(permission.LibraryID) == "" {
			return fmt.Errorf("媒体库 ID 不能为空")
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO user_library_permissions (user_id, library_id, can_view, can_play, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)"), userID, permission.LibraryID, boolInt(permission.CanView), boolInt(permission.CanPlay), now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
