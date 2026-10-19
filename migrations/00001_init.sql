-- +goose Up

CREATE TABLE virtual_items (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT 'javdb',
    source_id TEXT,
    code TEXT NOT NULL UNIQUE,
    raw_code TEXT NOT NULL,
    title TEXT NOT NULL,
    translated_title TEXT,
    original_title TEXT,
    overview TEXT,
    release_date TEXT,
    year INTEGER CHECK (year IS NULL OR (year BETWEEN 1900 AND 2100)),
    runtime_seconds INTEGER CHECK (runtime_seconds IS NULL OR runtime_seconds > 0),
    source_rating REAL CHECK (source_rating IS NULL OR (source_rating BETWEEN 0 AND 10)),
    rating_count INTEGER NOT NULL DEFAULT 0 CHECK (rating_count >= 0),
    source_url TEXT NOT NULL,
    source_updated_at TEXT,
    last_synced_at TEXT,
    metadata_status TEXT NOT NULL DEFAULT 'partial' CHECK (metadata_status IN ('complete', 'partial', 'error')),
    is_adult INTEGER NOT NULL DEFAULT 1 CHECK (is_adult IN (0, 1)),
    deleted_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_virtual_items_year ON virtual_items(year);
CREATE INDEX idx_virtual_items_release_date ON virtual_items(release_date);
CREATE INDEX idx_virtual_items_rating ON virtual_items(source_rating);

CREATE TABLE metadata_field_states (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    field_name TEXT NOT NULL,
    value_json TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('javdb', 'manual', 'import')),
    source_updated_at TEXT,
    locked INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
    updated_by_user_id TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (item_id, field_name)
);

CREATE TABLE people (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    aliases_json TEXT NOT NULL DEFAULT '[]',
    gender TEXT,
    status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('active', 'retired', 'unknown')),
    birthday TEXT,
    height_cm INTEGER,
    cup_size TEXT,
    measurements_json TEXT,
    biography TEXT,
    avatar_image_id TEXT,
    source_url TEXT,
    is_excluded INTEGER NOT NULL DEFAULT 0 CHECK (is_excluded IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE studios (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    source_name TEXT,
    aliases_json TEXT NOT NULL DEFAULT '[]',
    is_excluded INTEGER NOT NULL DEFAULT 0 CHECK (is_excluded IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE series (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    source_name TEXT,
    aliases_json TEXT NOT NULL DEFAULT '[]',
    is_excluded INTEGER NOT NULL DEFAULT 0 CHECK (is_excluded IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE tags (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    source_name TEXT,
    aliases_json TEXT NOT NULL DEFAULT '[]',
    kind TEXT NOT NULL DEFAULT 'custom' CHECK (kind IN ('genre', 'theme', 'source', 'custom')),
    parent_id TEXT REFERENCES tags(id) ON DELETE RESTRICT,
    is_excluded INTEGER NOT NULL DEFAULT 0 CHECK (is_excluded IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE item_people (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES people(id) ON DELETE RESTRICT,
    role TEXT NOT NULL DEFAULT 'actor' CHECK (role IN ('actor', 'director', 'other')),
    sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_id, person_id, role)
);
CREATE TABLE item_studios (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    studio_id TEXT NOT NULL REFERENCES studios(id) ON DELETE RESTRICT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_id, studio_id)
);
CREATE TABLE item_series (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    series_id TEXT NOT NULL REFERENCES series(id) ON DELETE RESTRICT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (item_id, series_id)
);
CREATE TABLE item_tags (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE RESTRICT,
    PRIMARY KEY (item_id, tag_id)
);

CREATE TABLE item_images (
    id TEXT PRIMARY KEY,
    item_id TEXT REFERENCES virtual_items(id) ON DELETE CASCADE,
    image_type TEXT NOT NULL CHECK (image_type IN ('poster', 'backdrop', 'gallery', 'preview', 'avatar')),
    source_url TEXT NOT NULL,
    cache_key TEXT NOT NULL UNIQUE,
    width INTEGER,
    height INTEGER,
    sha256 TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'error')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE item_external_links (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    site_type TEXT NOT NULL CHECK (site_type IN ('javbus', 'javlibrary', 'fanza', 'javtxt', 'custom')),
    url TEXT NOT NULL,
    display_name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE item_rankings (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    ranking_type TEXT NOT NULL,
    rank INTEGER NOT NULL CHECK (rank > 0),
    observed_at TEXT NOT NULL,
    is_current INTEGER NOT NULL DEFAULT 1 CHECK (is_current IN (0, 1)),
    created_at TEXT NOT NULL,
    UNIQUE (item_id, ranking_type, observed_at)
);

CREATE TABLE online_site_accounts (
    id TEXT PRIMARY KEY,
    provider_type TEXT NOT NULL CHECK (provider_type IN ('javdb-video', 'trailer', 'external-online')),
    display_name TEXT NOT NULL,
    encrypted_config TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'error')),
    last_health_status TEXT,
    last_health_check_at TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE online_providers (
    id TEXT PRIMARY KEY,
    account_id TEXT REFERENCES online_site_accounts(id) ON DELETE SET NULL,
    provider_type TEXT NOT NULL CHECK (provider_type IN ('javdb-video', 'trailer', 'external-online')),
    display_name TEXT NOT NULL,
    icon_url TEXT,
    group_name TEXT,
    url_template TEXT NOT NULL,
    allowed_hosts_json TEXT NOT NULL DEFAULT '[]',
    priority INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    config_version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE playback_sources (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL REFERENCES online_providers(id) ON DELETE RESTRICT,
    label TEXT NOT NULL,
    protocol TEXT NOT NULL CHECK (protocol IN ('hls', 'http', 'redirect')),
    quality TEXT,
    language TEXT,
    candidate_url_encrypted TEXT,
    resolved_url_encrypted TEXT,
    resolved_headers_encrypted TEXT,
    duration_seconds INTEGER,
    size_bytes INTEGER,
    is_preview INTEGER NOT NULL DEFAULT 0 CHECK (is_preview IN (0, 1)),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'unavailable', 'expired')),
    last_resolved_at TEXT,
    expires_at TEXT,
    last_error TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    is_admin INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    last_login_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE roles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (name IN ('Admin', 'Editor', 'Viewer')),
    created_at TEXT NOT NULL
);
INSERT INTO roles (id, name, created_at) VALUES
    ('role-admin', 'Admin', '1970-01-01T00:00:00Z'),
    ('role-editor', 'Editor', '1970-01-01T00:00:00Z'),
    ('role-viewer', 'Viewer', '1970-01-01T00:00:00Z');
CREATE TABLE user_roles (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, role_id)
);
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_name TEXT,
    device_id TEXT,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    last_seen_at TEXT,
    created_at TEXT NOT NULL
);
CREATE TABLE user_preferences (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    theme_json TEXT NOT NULL DEFAULT '{}',
    module_flags_json TEXT NOT NULL DEFAULT '{}',
    locale TEXT NOT NULL DEFAULT 'zh-CN',
    adult_confirmed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE user_item_states (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    is_favorite INTEGER NOT NULL DEFAULT 0 CHECK (is_favorite IN (0, 1)),
    is_watched INTEGER NOT NULL DEFAULT 0 CHECK (is_watched IN (0, 1)),
    is_want_to_watch INTEGER NOT NULL DEFAULT 0 CHECK (is_want_to_watch IN (0, 1)),
    playback_position_ticks INTEGER NOT NULL DEFAULT 0 CHECK (playback_position_ticks >= 0),
    playback_runtime_ticks INTEGER NOT NULL DEFAULT 0 CHECK (playback_runtime_ticks >= 0),
    last_played_at TEXT,
    user_rating INTEGER CHECK (user_rating IS NULL OR (user_rating BETWEEN 0 AND 10)),
    note TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_id)
);
CREATE TABLE user_person_states (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    is_favorite INTEGER NOT NULL DEFAULT 0 CHECK (is_favorite IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, person_id)
);
CREATE TABLE user_image_states (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    image_id TEXT NOT NULL REFERENCES item_images(id) ON DELETE CASCADE,
    is_favorite INTEGER NOT NULL DEFAULT 0 CHECK (is_favorite IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, image_id)
);
CREATE TABLE user_entity_notes (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('person', 'studio', 'series', 'tag')),
    entity_id TEXT NOT NULL,
    note TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, entity_type, entity_id)
);

CREATE TABLE virtual_libraries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    item_type TEXT NOT NULL DEFAULT 'movie' CHECK (item_type IN ('movie', 'collection', 'people', 'mixed')),
    filter_json TEXT NOT NULL DEFAULT '{}',
    sort_json TEXT NOT NULL DEFAULT '{}',
    poster_image_id TEXT REFERENCES item_images(id) ON DELETE SET NULL,
    is_public INTEGER NOT NULL DEFAULT 0 CHECK (is_public IN (0, 1)),
    is_adult INTEGER NOT NULL DEFAULT 1 CHECK (is_adult IN (0, 1)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE collections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'javdb')),
    source_id TEXT,
    poster_image_id TEXT REFERENCES item_images(id) ON DELETE SET NULL,
    owner_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    is_public INTEGER NOT NULL DEFAULT 0 CHECK (is_public IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE collection_items (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    PRIMARY KEY (collection_id, item_id)
);
CREATE TABLE library_items (
    library_id TEXT NOT NULL REFERENCES virtual_libraries(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    PRIMARY KEY (library_id, item_id)
);
CREATE TABLE user_library_permissions (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id TEXT NOT NULL REFERENCES virtual_libraries(id) ON DELETE CASCADE,
    can_view INTEGER NOT NULL DEFAULT 1 CHECK (can_view IN (0, 1)),
    can_play INTEGER NOT NULL DEFAULT 1 CHECK (can_play IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, library_id)
);
CREATE TABLE playlists (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    overview TEXT,
    is_public INTEGER NOT NULL DEFAULT 0 CHECK (is_public IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE playlist_items (
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL,
    added_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (playlist_id, item_id),
    UNIQUE (playlist_id, position)
);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    idempotency_key TEXT UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'partial', 'failed', 'cancelled')),
    payload_json TEXT NOT NULL DEFAULT '{}',
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    error_code TEXT,
    error_message TEXT,
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE task_attempts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    status TEXT NOT NULL,
    error_code TEXT,
    error_message TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    UNIQUE (task_id, attempt_number)
);
CREATE TABLE audit_logs (
    id TEXT PRIMARY KEY,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    request_id TEXT,
    result TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE TABLE backup_targets (
    id TEXT PRIMARY KEY,
    target_type TEXT NOT NULL CHECK (target_type IN ('local', 'webdav', 'git')),
    display_name TEXT NOT NULL,
    encrypted_config TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    retention_count INTEGER NOT NULL DEFAULT 7 CHECK (retention_count > 0),
    last_run_at TEXT,
    last_status TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_item_people_person ON item_people(person_id);
CREATE INDEX idx_item_studios_studio ON item_studios(studio_id);
CREATE INDEX idx_item_series_series ON item_series(series_id);
CREATE INDEX idx_item_tags_tag ON item_tags(tag_id);
CREATE INDEX idx_item_images_item ON item_images(item_id, image_type, sort_order);
CREATE INDEX idx_playback_sources_item ON playback_sources(item_id, status, sort_order);
CREATE INDEX idx_sessions_user ON sessions(user_id, revoked_at, expires_at);
CREATE INDEX idx_tasks_status ON tasks(status, created_at);
CREATE INDEX idx_audit_logs_resource ON audit_logs(resource_type, resource_id, created_at);

-- +goose Down

DROP TABLE IF EXISTS backup_targets;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS task_attempts;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS playlist_items;
DROP TABLE IF EXISTS playlists;
DROP TABLE IF EXISTS user_library_permissions;
DROP TABLE IF EXISTS library_items;
DROP TABLE IF EXISTS collection_items;
DROP TABLE IF EXISTS collections;
DROP TABLE IF EXISTS virtual_libraries;
DROP TABLE IF EXISTS user_entity_notes;
DROP TABLE IF EXISTS user_image_states;
DROP TABLE IF EXISTS user_person_states;
DROP TABLE IF EXISTS user_item_states;
DROP TABLE IF EXISTS user_preferences;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS playback_sources;
DROP TABLE IF EXISTS online_providers;
DROP TABLE IF EXISTS online_site_accounts;
DROP TABLE IF EXISTS item_rankings;
DROP TABLE IF EXISTS item_external_links;
DROP TABLE IF EXISTS item_images;
DROP TABLE IF EXISTS item_tags;
DROP TABLE IF EXISTS item_series;
DROP TABLE IF EXISTS item_studios;
DROP TABLE IF EXISTS item_people;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS series;
DROP TABLE IF EXISTS studios;
DROP TABLE IF EXISTS people;
DROP TABLE IF EXISTS metadata_field_states;
DROP TABLE IF EXISTS virtual_items;
