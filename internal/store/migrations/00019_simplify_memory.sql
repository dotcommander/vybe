-- +goose Up
-- +goose StatementBegin

-- Drop old indexes FIRST (before dropping columns they reference)
DROP INDEX IF EXISTS idx_memory_active_canonical;
DROP INDEX IF EXISTS idx_memory_canonical_unique;
DROP INDEX IF EXISTS idx_memory_scope_canonical_expires;

-- Add updated_at column with NULL default (CURRENT_TIMESTAMP is non-constant in SQLite)
ALTER TABLE memory ADD COLUMN updated_at DATETIME;

-- Backfill updated_at from last_seen_at or created_at
UPDATE memory SET updated_at = COALESCE(last_seen_at, created_at);

-- Drop over-engineered columns
ALTER TABLE memory DROP COLUMN canonical_key;
ALTER TABLE memory DROP COLUMN confidence;
ALTER TABLE memory DROP COLUMN source_event_id;
ALTER TABLE memory DROP COLUMN superseded_by;
ALTER TABLE memory DROP COLUMN last_seen_at;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE memory ADD COLUMN canonical_key TEXT;
ALTER TABLE memory ADD COLUMN confidence REAL NOT NULL DEFAULT 0.5;
ALTER TABLE memory ADD COLUMN source_event_id INTEGER;
ALTER TABLE memory ADD COLUMN superseded_by TEXT;
ALTER TABLE memory ADD COLUMN last_seen_at TIMESTAMP;

UPDATE memory SET last_seen_at = updated_at, canonical_key = LOWER(TRIM(key));

ALTER TABLE memory DROP COLUMN updated_at;

CREATE INDEX idx_memory_scope_canonical_expires ON memory(scope, scope_id, canonical_key, expires_at);

-- +goose StatementEnd
