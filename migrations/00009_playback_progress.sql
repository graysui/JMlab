-- +goose Up

ALTER TABLE user_item_states ADD COLUMN playback_session_id TEXT;
ALTER TABLE user_item_states ADD COLUMN playback_device_id TEXT;
ALTER TABLE user_item_states ADD COLUMN playback_device_name TEXT;
ALTER TABLE user_item_states ADD COLUMN playback_state TEXT NOT NULL DEFAULT 'stopped' CHECK (playback_state IN ('playing', 'paused', 'stopped'));
ALTER TABLE user_item_states ADD COLUMN playback_percent REAL NOT NULL DEFAULT 0 CHECK (playback_percent >= 0 AND playback_percent <= 100);
ALTER TABLE user_item_states ADD COLUMN playback_event_at TEXT;

CREATE INDEX idx_user_item_states_playback_recent ON user_item_states(user_id, playback_event_at);

-- +goose Down

DROP INDEX IF EXISTS idx_user_item_states_playback_recent;
