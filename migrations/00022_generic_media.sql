-- +goose Up

-- Renaming preserves every existing item/source ID and retargets foreign keys.
ALTER TABLE virtual_items RENAME TO media_items;
ALTER TABLE playback_sources RENAME TO stream_sources;

ALTER TABLE media_items ADD COLUMN item_type TEXT NOT NULL DEFAULT 'movie' CHECK (item_type IN ('movie', 'series', 'season', 'episode', 'video'));
ALTER TABLE media_items ADD COLUMN parent_id TEXT REFERENCES media_items(id) ON DELETE RESTRICT;
ALTER TABLE media_items ADD COLUMN index_number INTEGER CHECK (index_number IS NULL OR index_number >= 0);
ALTER TABLE media_items ADD COLUMN provider_ids_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE media_items ADD COLUMN extra_attributes_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE media_items ADD COLUMN metadata_module_id TEXT NOT NULL DEFAULT 'reference-provider-javdb';
ALTER TABLE stream_sources ADD COLUMN module_id TEXT;

UPDATE media_items SET provider_ids_json = '{}' WHERE provider_ids_json IS NULL;
UPDATE stream_sources SET module_id = CASE WHEN provider_id LIKE 'module:%' THEN SUBSTR(provider_id, 8) ELSE 'reference-stream-builtin' END;
CREATE INDEX idx_media_items_parent ON media_items(parent_id, index_number, id);
CREATE INDEX idx_media_items_type ON media_items(item_type, deleted_at);
CREATE INDEX idx_media_items_module_source ON media_items(metadata_module_id, source_id);
CREATE INDEX idx_stream_sources_module ON stream_sources(module_id, item_id);

-- +goose Down
-- The legacy application cannot represent hierarchy. Explicitly reject a
-- downgrade while non-movie data exists instead of silently discarding it.
CREATE TABLE media_downgrade_guard (count INTEGER CHECK (count = 0));
INSERT INTO media_downgrade_guard SELECT COUNT(*) FROM media_items WHERE item_type <> 'movie';
DROP TABLE media_downgrade_guard;
DROP INDEX idx_stream_sources_module;
DROP INDEX idx_media_items_module_source;
DROP INDEX idx_media_items_type;
DROP INDEX idx_media_items_parent;
ALTER TABLE stream_sources DROP COLUMN module_id;
ALTER TABLE media_items DROP COLUMN metadata_module_id;
ALTER TABLE media_items DROP COLUMN extra_attributes_json;
ALTER TABLE media_items DROP COLUMN provider_ids_json;
ALTER TABLE media_items DROP COLUMN index_number;
ALTER TABLE media_items DROP COLUMN parent_id;
ALTER TABLE media_items DROP COLUMN item_type;
ALTER TABLE stream_sources RENAME TO playback_sources;
ALTER TABLE media_items RENAME TO virtual_items;
