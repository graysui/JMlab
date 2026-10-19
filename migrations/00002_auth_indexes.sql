-- +goose Up

CREATE UNIQUE INDEX idx_users_username_ci ON users (LOWER(username));
CREATE INDEX idx_users_status ON users(status, created_at);
CREATE INDEX idx_user_roles_role ON user_roles(role_id, user_id);
CREATE INDEX idx_user_preferences_adult ON user_preferences(adult_confirmed_at);
CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_user_id, created_at);

-- +goose Down

DROP INDEX IF EXISTS idx_audit_logs_actor;
DROP INDEX IF EXISTS idx_user_preferences_adult;
DROP INDEX IF EXISTS idx_user_roles_role;
DROP INDEX IF EXISTS idx_users_status;
DROP INDEX IF EXISTS idx_users_username_ci;
