-- +goose Up

CREATE INDEX idx_item_rankings_current_rank ON item_rankings(ranking_type, is_current, rank, item_id);
CREATE INDEX idx_virtual_items_sync_time ON virtual_items(last_synced_at, updated_at, id);
CREATE INDEX idx_user_item_states_recent_play ON user_item_states(user_id, last_played_at, item_id);

-- +goose Down

DROP INDEX IF EXISTS idx_user_item_states_recent_play;
DROP INDEX IF EXISTS idx_virtual_items_sync_time;
DROP INDEX IF EXISTS idx_item_rankings_current_rank;
