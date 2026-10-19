-- +goose Up

CREATE UNIQUE INDEX idx_virtual_items_source_id ON virtual_items(source, source_id) WHERE source_id IS NOT NULL;
CREATE INDEX idx_virtual_items_code_title ON virtual_items(code, title, updated_at);
CREATE INDEX idx_virtual_items_status ON virtual_items(metadata_status, deleted_at, updated_at);
CREATE INDEX idx_people_name_ci ON people(LOWER(name));
CREATE INDEX idx_studios_name_ci ON studios(LOWER(name));
CREATE INDEX idx_series_name_ci ON series(LOWER(name));
CREATE INDEX idx_tags_name_ci ON tags(LOWER(name));
CREATE INDEX idx_library_items_item ON library_items(item_id, library_id);
CREATE INDEX idx_library_permissions_user ON user_library_permissions(user_id, can_view, library_id);

-- +goose Down

DROP INDEX IF EXISTS idx_library_permissions_user;
DROP INDEX IF EXISTS idx_library_items_item;
DROP INDEX IF EXISTS idx_tags_name_ci;
DROP INDEX IF EXISTS idx_series_name_ci;
DROP INDEX IF EXISTS idx_studios_name_ci;
DROP INDEX IF EXISTS idx_people_name_ci;
DROP INDEX IF EXISTS idx_virtual_items_status;
DROP INDEX IF EXISTS idx_virtual_items_code_title;
DROP INDEX IF EXISTS idx_virtual_items_source_id;
