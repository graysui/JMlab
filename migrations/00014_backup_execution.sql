-- +goose Up

CREATE TABLE backup_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    requested_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    include_user_state INTEGER NOT NULL DEFAULT 0 CHECK (include_user_state IN (0, 1)),
    include_encrypted_config INTEGER NOT NULL DEFAULT 0 CHECK (include_encrypted_config IN (0, 1)),
    artifact_name TEXT,
    artifact_sha256 TEXT,
    artifact_size INTEGER,
    created_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE TABLE backup_run_targets (
    run_id TEXT NOT NULL REFERENCES backup_runs(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL,
    target_type TEXT NOT NULL,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    artifact_path TEXT,
    bytes_written INTEGER NOT NULL DEFAULT 0,
    checksum TEXT,
    error_message TEXT,
    started_at TEXT,
    completed_at TEXT,
    PRIMARY KEY (run_id, target_id)
);

CREATE INDEX idx_backup_runs_task ON backup_runs(task_id);
CREATE INDEX idx_backup_run_targets_status ON backup_run_targets(run_id, status);

-- +goose Down

DROP INDEX IF EXISTS idx_backup_run_targets_status;
DROP INDEX IF EXISTS idx_backup_runs_task;
DROP TABLE IF EXISTS backup_run_targets;
DROP TABLE IF EXISTS backup_runs;
