-- +goose Up

CREATE TABLE ranking_schedule_config (
    id TEXT PRIMARY KEY DEFAULT 'default',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    update_time TEXT NOT NULL DEFAULT '04:00',
    interval_hours INTEGER NOT NULL DEFAULT 24,
    last_run_at TEXT,
    updated_at TEXT NOT NULL
);

INSERT INTO ranking_schedule_config (id, enabled, update_time, interval_hours, updated_at)
VALUES ('default', 1, '04:00', 24, '1970-01-01T00:00:00Z')
ON CONFLICT(id) DO NOTHING;

-- +goose Down

DROP TABLE IF EXISTS ranking_schedule_config;
