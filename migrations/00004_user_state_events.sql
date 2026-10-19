-- +goose Up

CREATE TABLE user_item_state_events (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('favorite', 'watched', 'want_to_watch', 'rating', 'note', 'progress', 'playback_start', 'playback_stop')),
    position_ticks INTEGER NOT NULL DEFAULT 0 CHECK (position_ticks >= 0),
    runtime_ticks INTEGER NOT NULL DEFAULT 0 CHECK (runtime_ticks >= 0),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_user_state_events_item ON user_item_state_events(user_id, item_id, created_at);
CREATE INDEX idx_playlists_owner_updated ON playlists(owner_user_id, updated_at);
CREATE INDEX idx_playlist_items_position ON playlist_items(playlist_id, position);

-- +goose Down

DROP INDEX IF EXISTS idx_playlist_items_position;
DROP INDEX IF EXISTS idx_playlists_owner_updated;
DROP INDEX IF EXISTS idx_user_state_events_item;
DROP TABLE IF EXISTS user_item_state_events;
