-- +goose Up
CREATE TABLE module_ranking_schedules (
    module_id TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
    interval_hours INTEGER NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 720),
    update_time TEXT NOT NULL DEFAULT '',
    targets_json TEXT NOT NULL DEFAULT '[]',
    last_run_at TEXT,
    updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE module_ranking_schedules;
