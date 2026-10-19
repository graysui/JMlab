-- +goose Up

CREATE INDEX idx_virtual_items_code_search ON virtual_items(code, deleted_at, updated_at, id);
CREATE INDEX idx_virtual_items_title_search ON virtual_items(title, deleted_at, updated_at, id);
CREATE INDEX idx_virtual_items_original_title_search ON virtual_items(original_title, deleted_at, updated_at, id);
CREATE INDEX idx_people_name_search ON people(name, is_excluded, id);
CREATE INDEX idx_studios_name_search ON studios(name, is_excluded, id);
CREATE INDEX idx_series_name_search ON series(name, is_excluded, id);
CREATE INDEX idx_tags_name_search ON tags(name, is_excluded, id);

-- +goose Down

DROP INDEX IF EXISTS idx_tags_name_search;
DROP INDEX IF EXISTS idx_series_name_search;
DROP INDEX IF EXISTS idx_studios_name_search;
DROP INDEX IF EXISTS idx_people_name_search;
DROP INDEX IF EXISTS idx_virtual_items_original_title_search;
DROP INDEX IF EXISTS idx_virtual_items_title_search;
DROP INDEX IF EXISTS idx_virtual_items_code_search;
