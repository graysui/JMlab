-- +goose Up
CREATE TABLE module_restore_queue (
    module_id TEXT PRIMARY KEY,
    record_encrypted TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE module_restore_queue;
