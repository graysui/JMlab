-- +goose Up
-- No table references stream_sources. Rebuild it inside the migration transaction
-- to make legacy provider settings optional without changing source identities.
CREATE TABLE stream_sources_next (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    module_id TEXT NOT NULL DEFAULT 'reference-stream-builtin' CHECK (module_id <> ''),
    provider_id TEXT REFERENCES online_providers(id) ON DELETE RESTRICT,
    priority INTEGER NOT NULL DEFAULT 50,
    attributes_locked INTEGER NOT NULL DEFAULT 0 CHECK (attributes_locked IN (0, 1)),
    label TEXT NOT NULL,
    protocol TEXT NOT NULL CHECK (protocol IN ('hls', 'http', 'redirect')),
    quality TEXT,
    language TEXT,
    candidate_url_encrypted TEXT,
    resolved_url_encrypted TEXT,
    resolved_headers_encrypted TEXT,
    duration_seconds INTEGER,
    size_bytes INTEGER,
    is_preview INTEGER NOT NULL DEFAULT 0 CHECK (is_preview IN (0, 1)),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'unavailable', 'expired')),
    last_resolved_at TEXT,
    expires_at TEXT,
    last_error TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_probe_status TEXT,
    last_probe_at TEXT,
    last_probe_duration_ms INTEGER NOT NULL DEFAULT 0,
    last_probe_error TEXT,
    probe_count INTEGER NOT NULL DEFAULT 0,
    probe_success_count INTEGER NOT NULL DEFAULT 0,
    probe_duration_ms_total INTEGER NOT NULL DEFAULT 0,
    CHECK (provider_id IS NULL OR module_id = 'reference-stream-builtin')
);
INSERT INTO stream_sources_next (
    id, item_id, module_id, provider_id, priority, label, protocol, quality, language,
    candidate_url_encrypted, resolved_url_encrypted, resolved_headers_encrypted,
    duration_seconds, size_bytes, is_preview, status, last_resolved_at, expires_at,
    last_error, sort_order, created_at, updated_at, enabled, last_probe_status,
    last_probe_at, last_probe_duration_ms, last_probe_error, probe_count,
    probe_success_count, probe_duration_ms_total
)
SELECT ss.id, ss.item_id,
    CASE WHEN ss.provider_id LIKE 'module:%' THEN SUBSTR(ss.provider_id, 8)
         ELSE COALESCE(NULLIF(ss.module_id, ''), 'reference-stream-builtin') END,
    CASE WHEN ss.provider_id LIKE 'module:%' THEN NULL ELSE ss.provider_id END,
    COALESCE(op.priority, 50), ss.label, ss.protocol, ss.quality, ss.language,
    ss.candidate_url_encrypted, ss.resolved_url_encrypted, ss.resolved_headers_encrypted,
    ss.duration_seconds, ss.size_bytes, ss.is_preview, ss.status, ss.last_resolved_at,
    ss.expires_at, ss.last_error, ss.sort_order, ss.created_at, ss.updated_at,
    CASE WHEN ss.provider_id LIKE 'module:%' AND op.enabled = 0 THEN 0 ELSE ss.enabled END,
    ss.last_probe_status, ss.last_probe_at, ss.last_probe_duration_ms,
    ss.last_probe_error, ss.probe_count, ss.probe_success_count, ss.probe_duration_ms_total
FROM stream_sources ss LEFT JOIN online_providers op ON op.id = ss.provider_id;
DROP TABLE stream_sources;
ALTER TABLE stream_sources_next RENAME TO stream_sources;
CREATE INDEX idx_playback_sources_item ON stream_sources(item_id, status, sort_order);
CREATE INDEX idx_playback_sources_status_item ON stream_sources(status, item_id);
CREATE INDEX idx_playback_sources_admin ON stream_sources(item_id, enabled, status, sort_order);
CREATE INDEX idx_stream_sources_module ON stream_sources(module_id, item_id);
-- module: IDs were generated placeholders, never independent provider settings.
DELETE FROM online_providers WHERE id LIKE 'module:%';

-- +goose Down
-- Old hosts require synthetic provider rows and cannot represent this identity.
-- Use a backup from before the upgrade rather than silently losing candidates.
CREATE TABLE module_stream_downgrade_guard (count INTEGER CHECK (count = 0));
INSERT INTO module_stream_downgrade_guard VALUES (1);
