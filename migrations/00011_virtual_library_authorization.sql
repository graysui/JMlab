-- +goose Up

CREATE TABLE role_library_permissions (
    role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    library_id TEXT NOT NULL REFERENCES virtual_libraries(id) ON DELETE CASCADE,
    can_view INTEGER NOT NULL DEFAULT 1 CHECK (can_view IN (0, 1)),
    can_play INTEGER NOT NULL DEFAULT 1 CHECK (can_play IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (role_id, library_id)
);

CREATE INDEX idx_library_permissions_library_user ON user_library_permissions(library_id, user_id, can_view, can_play);
CREATE INDEX idx_role_library_permissions_library ON role_library_permissions(library_id, role_id, can_view, can_play);
CREATE INDEX idx_role_library_permissions_role ON role_library_permissions(role_id, library_id, can_view, can_play);
CREATE INDEX idx_virtual_libraries_enabled_public ON virtual_libraries(enabled, is_public, is_adult, name);
CREATE INDEX idx_virtual_libraries_poster ON virtual_libraries(poster_image_id);

-- +goose Down

DROP INDEX IF EXISTS idx_virtual_libraries_poster;
DROP INDEX IF EXISTS idx_virtual_libraries_enabled_public;
DROP INDEX IF EXISTS idx_role_library_permissions_role;
DROP INDEX IF EXISTS idx_role_library_permissions_library;
DROP INDEX IF EXISTS idx_library_permissions_library_user;
DROP TABLE IF EXISTS role_library_permissions;
