-- +goose Up
-- PostgreSQL INTEGER is 32-bit. Jellyfin ticks and media/backup byte counts
-- exceed that range during normal use. Add/copy/drop/rename is portable to
-- SQLite and preserves every row, key, foreign key and unrelated column.

ALTER TABLE user_item_states ADD COLUMN playback_position_ticks_64 BIGINT NOT NULL DEFAULT 0 CHECK (playback_position_ticks_64 >= 0);
ALTER TABLE user_item_states ADD COLUMN playback_runtime_ticks_64 BIGINT NOT NULL DEFAULT 0 CHECK (playback_runtime_ticks_64 >= 0);
UPDATE user_item_states SET playback_position_ticks_64=playback_position_ticks, playback_runtime_ticks_64=playback_runtime_ticks;
ALTER TABLE user_item_states DROP COLUMN playback_position_ticks;
ALTER TABLE user_item_states DROP COLUMN playback_runtime_ticks;
ALTER TABLE user_item_states RENAME COLUMN playback_position_ticks_64 TO playback_position_ticks;
ALTER TABLE user_item_states RENAME COLUMN playback_runtime_ticks_64 TO playback_runtime_ticks;

ALTER TABLE user_item_state_events ADD COLUMN position_ticks_64 BIGINT NOT NULL DEFAULT 0 CHECK (position_ticks_64 >= 0);
ALTER TABLE user_item_state_events ADD COLUMN runtime_ticks_64 BIGINT NOT NULL DEFAULT 0 CHECK (runtime_ticks_64 >= 0);
UPDATE user_item_state_events SET position_ticks_64=position_ticks, runtime_ticks_64=runtime_ticks;
ALTER TABLE user_item_state_events DROP COLUMN position_ticks;
ALTER TABLE user_item_state_events DROP COLUMN runtime_ticks;
ALTER TABLE user_item_state_events RENAME COLUMN position_ticks_64 TO position_ticks;
ALTER TABLE user_item_state_events RENAME COLUMN runtime_ticks_64 TO runtime_ticks;

ALTER TABLE stream_sources ADD COLUMN size_bytes_64 BIGINT;
ALTER TABLE stream_sources ADD COLUMN probe_duration_ms_total_64 BIGINT NOT NULL DEFAULT 0;
UPDATE stream_sources SET size_bytes_64=size_bytes, probe_duration_ms_total_64=probe_duration_ms_total;
ALTER TABLE stream_sources DROP COLUMN size_bytes;
ALTER TABLE stream_sources DROP COLUMN probe_duration_ms_total;
ALTER TABLE stream_sources RENAME COLUMN size_bytes_64 TO size_bytes;
ALTER TABLE stream_sources RENAME COLUMN probe_duration_ms_total_64 TO probe_duration_ms_total;

ALTER TABLE online_providers ADD COLUMN probe_duration_ms_total_64 BIGINT NOT NULL DEFAULT 0;
UPDATE online_providers SET probe_duration_ms_total_64=probe_duration_ms_total;
ALTER TABLE online_providers DROP COLUMN probe_duration_ms_total;
ALTER TABLE online_providers RENAME COLUMN probe_duration_ms_total_64 TO probe_duration_ms_total;

ALTER TABLE backup_runs ADD COLUMN artifact_size_64 BIGINT;
UPDATE backup_runs SET artifact_size_64=artifact_size;
ALTER TABLE backup_runs DROP COLUMN artifact_size;
ALTER TABLE backup_runs RENAME COLUMN artifact_size_64 TO artifact_size;

ALTER TABLE backup_run_targets ADD COLUMN bytes_written_64 BIGINT NOT NULL DEFAULT 0;
UPDATE backup_run_targets SET bytes_written_64=bytes_written;
ALTER TABLE backup_run_targets DROP COLUMN bytes_written;
ALTER TABLE backup_run_targets RENAME COLUMN bytes_written_64 TO bytes_written;

-- +goose Down
-- Narrowing can lose existing playback/backup values. Restore a pre-upgrade
-- backup when returning to an older database schema.
CREATE TABLE large_values_downgrade_guard (count INTEGER CHECK (count=0));
INSERT INTO large_values_downgrade_guard VALUES (1);
