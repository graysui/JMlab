-- +goose Up

-- Recreate item_rankings to support arbitrary ranking_type and period (e.g. annual years for top250)
CREATE TABLE item_rankings_new (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    ranking_type TEXT NOT NULL,
    period TEXT NOT NULL DEFAULT 'daily',
    rank INTEGER NOT NULL CHECK (rank > 0),
    observed_at TEXT NOT NULL,
    is_current INTEGER NOT NULL DEFAULT 1 CHECK (is_current IN (0, 1)),
    created_at TEXT NOT NULL,
    UNIQUE (item_id, ranking_type, period, observed_at)
);

INSERT INTO item_rankings_new (item_id, ranking_type, period, rank, observed_at, is_current, created_at)
SELECT item_id, ranking_type, period, rank, observed_at, is_current, created_at FROM item_rankings;

DROP TABLE item_rankings;
ALTER TABLE item_rankings_new RENAME TO item_rankings;
CREATE INDEX idx_item_rankings_current_rank ON item_rankings(ranking_type, period, is_current, rank, item_id);

-- Update existing records: hot -> playback, top250 daily -> all
UPDATE item_rankings SET ranking_type = 'playback' WHERE ranking_type = 'hot';
UPDATE item_rankings SET period = 'all' WHERE ranking_type = 'top250' AND period = 'daily';

-- Recreate ranking_generation_locks without restrictive CHECK constraints
DROP TABLE IF EXISTS ranking_generation_locks;
CREATE TABLE ranking_generation_locks (
    ranking_type TEXT NOT NULL,
    period TEXT NOT NULL,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (ranking_type, period),
    UNIQUE (task_id)
);

-- +goose Down

DROP TABLE IF EXISTS ranking_generation_locks;
CREATE TABLE ranking_generation_locks (
    ranking_type TEXT NOT NULL CHECK (ranking_type IN ('hot', 'top250')),
    period TEXT NOT NULL CHECK (period IN ('daily', 'weekly', 'monthly')),
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (ranking_type, period),
    UNIQUE (task_id)
);

CREATE TABLE item_rankings_old (
    item_id TEXT NOT NULL REFERENCES virtual_items(id) ON DELETE CASCADE,
    ranking_type TEXT NOT NULL,
    period TEXT NOT NULL DEFAULT 'daily' CHECK (period IN ('daily', 'weekly', 'monthly')),
    rank INTEGER NOT NULL CHECK (rank > 0),
    observed_at TEXT NOT NULL,
    is_current INTEGER NOT NULL DEFAULT 1 CHECK (is_current IN (0, 1)),
    created_at TEXT NOT NULL,
    UNIQUE (item_id, ranking_type, observed_at)
);

INSERT INTO item_rankings_old (item_id, ranking_type, period, rank, observed_at, is_current, created_at)
SELECT item_id, ranking_type, CASE WHEN period IN ('daily', 'weekly', 'monthly') THEN period ELSE 'daily' END, rank, observed_at, is_current, created_at
FROM item_rankings;

DROP TABLE item_rankings;
ALTER TABLE item_rankings_old RENAME TO item_rankings;
CREATE INDEX idx_item_rankings_current_rank ON item_rankings(ranking_type, period, is_current, rank, item_id);
