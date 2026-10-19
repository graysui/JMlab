-- +goose Up

CREATE INDEX idx_item_people_item_role ON item_people(item_id, role, person_id);
CREATE INDEX idx_item_people_person_role_item ON item_people(person_id, role, item_id);
CREATE INDEX idx_item_studios_studio_item ON item_studios(studio_id, item_id);
CREATE INDEX idx_item_series_series_item ON item_series(series_id, item_id);
CREATE INDEX idx_item_tags_tag_item ON item_tags(tag_id, item_id);
CREATE INDEX idx_collection_items_item_collection ON collection_items(item_id, collection_id);
CREATE INDEX idx_user_item_states_favorite ON user_item_states(user_id, is_favorite, item_id);
CREATE INDEX idx_user_item_states_watched ON user_item_states(user_id, is_watched, item_id);
CREATE INDEX idx_playback_sources_status_item ON playback_sources(status, item_id);
CREATE INDEX idx_virtual_items_year_updated ON virtual_items(year, updated_at, id);
CREATE INDEX idx_virtual_items_release_updated ON virtual_items(release_date, updated_at, id);

-- +goose Down

DROP INDEX IF EXISTS idx_virtual_items_release_updated;
DROP INDEX IF EXISTS idx_virtual_items_year_updated;
DROP INDEX IF EXISTS idx_playback_sources_status_item;
DROP INDEX IF EXISTS idx_user_item_states_watched;
DROP INDEX IF EXISTS idx_user_item_states_favorite;
DROP INDEX IF EXISTS idx_collection_items_item_collection;
DROP INDEX IF EXISTS idx_item_tags_tag_item;
DROP INDEX IF EXISTS idx_item_series_series_item;
DROP INDEX IF EXISTS idx_item_studios_studio_item;
DROP INDEX IF EXISTS idx_item_people_person_role_item;
DROP INDEX IF EXISTS idx_item_people_item_role;
