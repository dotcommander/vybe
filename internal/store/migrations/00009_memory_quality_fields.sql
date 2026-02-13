-- +goose Up
-- +goose StatementBegin

ALTER TABLE memory ADD COLUMN canonical_key TEXT;
ALTER TABLE memory ADD COLUMN confidence REAL NOT NULL DEFAULT 0.5;
ALTER TABLE memory ADD COLUMN last_seen_at TIMESTAMP;
ALTER TABLE memory ADD COLUMN source_event_id INTEGER;
ALTER TABLE memory ADD COLUMN superseded_by TEXT;

UPDATE memory
SET canonical_key = LOWER(TRIM(key))
WHERE canonical_key IS NULL OR canonical_key = '';

UPDATE memory
SET last_seen_at = created_at
WHERE last_seen_at IS NULL;

CREATE INDEX idx_memory_scope_canonical_expires
    ON memory(scope, scope_id, canonical_key, expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_memory_scope_canonical_expires;

ALTER TABLE memory DROP COLUMN superseded_by;
ALTER TABLE memory DROP COLUMN source_event_id;
ALTER TABLE memory DROP COLUMN last_seen_at;
ALTER TABLE memory DROP COLUMN confidence;
ALTER TABLE memory DROP COLUMN canonical_key;

-- +goose StatementEnd
