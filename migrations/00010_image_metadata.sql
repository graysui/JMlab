-- +goose Up

ALTER TABLE item_images ADD COLUMN mime_type TEXT NOT NULL DEFAULT 'application/octet-stream';
ALTER TABLE item_images ADD COLUMN source_label TEXT;
ALTER TABLE item_images ADD COLUMN source_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE item_images ADD COLUMN error_message TEXT;
ALTER TABLE item_images ADD COLUMN last_attempt_at TEXT;
ALTER TABLE item_images ADD COLUMN fetch_duration_ms INTEGER NOT NULL DEFAULT 0;

CREATE TABLE image_source_attempts (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    image_id TEXT REFERENCES item_images(id) ON DELETE SET NULL,
    image_type TEXT NOT NULL CHECK (image_type IN ('poster', 'backdrop', 'gallery', 'preview', 'avatar')),
    source_url TEXT NOT NULL,
    source_label TEXT,
    source_priority INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('ready', 'error')),
    error_message TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    attempted_at TEXT NOT NULL
);

CREATE INDEX idx_image_attempts_item ON image_source_attempts(item_id, image_type, attempted_at);
CREATE INDEX idx_image_attempts_source ON image_source_attempts(item_id, source_url, attempted_at);

-- +goose Down

DROP INDEX IF EXISTS idx_image_attempts_source;
DROP INDEX IF EXISTS idx_image_attempts_item;
DROP TABLE IF EXISTS image_source_attempts;
