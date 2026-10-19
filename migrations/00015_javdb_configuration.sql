-- +goose Up

CREATE TABLE javdb_configuration (
    id TEXT PRIMARY KEY CHECK (id = 'global'),
    base_url TEXT NOT NULL,
    encrypted_cookie TEXT NOT NULL DEFAULT '',
    updated_by_user_id TEXT,
    updated_at TEXT NOT NULL
);

-- +goose Down

DROP TABLE javdb_configuration;
