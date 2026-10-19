-- +goose Up
CREATE TABLE module_storage (
    module_id TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    value_encrypted TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(module_id, storage_key)
);

-- +goose Down
DROP TABLE module_storage;
