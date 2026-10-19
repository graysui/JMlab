-- +goose Up

ALTER TABLE item_rankings ADD COLUMN period TEXT NOT NULL DEFAULT 'daily' CHECK (period IN ('daily', 'weekly', 'monthly'));
DROP INDEX IF EXISTS idx_item_rankings_current_rank;
CREATE INDEX idx_item_rankings_current_rank ON item_rankings(ranking_type, period, is_current, rank, item_id);

CREATE TABLE ranking_generation_locks (
    ranking_type TEXT NOT NULL CHECK (ranking_type IN ('hot', 'top250')),
    period TEXT NOT NULL CHECK (period IN ('daily', 'weekly', 'monthly')),
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (ranking_type, period),
    UNIQUE (task_id)
);

-- +goose Down

DROP TABLE IF EXISTS ranking_generation_locks;
DROP INDEX IF EXISTS idx_item_rankings_current_rank;
ALTER TABLE item_rankings DROP COLUMN period;
CREATE INDEX idx_item_rankings_current_rank ON item_rankings(ranking_type, is_current, rank, item_id);
