-- +goose Up

CREATE INDEX idx_collections_owner_public_updated ON collections(owner_user_id, is_public, updated_at);
CREATE INDEX idx_collections_source ON collections(source, source_id);
CREATE INDEX idx_collection_items_collection_position ON collection_items(collection_id, position, item_id);

-- +goose Down

DROP INDEX IF EXISTS idx_collection_items_collection_position;
DROP INDEX IF EXISTS idx_collections_source;
DROP INDEX IF EXISTS idx_collections_owner_public_updated;
