-- +goose Up
ALTER TABLE online_providers ADD COLUMN last_probe_status TEXT;
ALTER TABLE online_providers ADD COLUMN last_probe_at TEXT;
ALTER TABLE online_providers ADD COLUMN last_probe_duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE online_providers ADD COLUMN last_probe_error TEXT;
ALTER TABLE online_providers ADD COLUMN probe_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE online_providers ADD COLUMN probe_success_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE online_providers ADD COLUMN probe_duration_ms_total INTEGER NOT NULL DEFAULT 0;

ALTER TABLE playback_sources ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1));
ALTER TABLE playback_sources ADD COLUMN last_probe_status TEXT;
ALTER TABLE playback_sources ADD COLUMN last_probe_at TEXT;
ALTER TABLE playback_sources ADD COLUMN last_probe_duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playback_sources ADD COLUMN last_probe_error TEXT;
ALTER TABLE playback_sources ADD COLUMN probe_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playback_sources ADD COLUMN probe_success_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playback_sources ADD COLUMN probe_duration_ms_total INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_playback_sources_admin ON playback_sources(item_id, enabled, status, sort_order);

-- +goose Down
DROP INDEX IF EXISTS idx_playback_sources_admin;
