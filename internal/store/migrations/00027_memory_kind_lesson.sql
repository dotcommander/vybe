-- +goose Up
-- +goose StatementBegin
-- Expand the kind CHECK constraint to include 'lesson'.
-- SQLite requires a full table rebuild to change CHECK constraints.
-- Column list reflects state after migration 00019 dropped canonical/confidence/etc.
PRAGMA foreign_keys=OFF;

CREATE TABLE memory_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    key              TEXT    NOT NULL,
    value            TEXT,
    value_type       TEXT    NOT NULL,
    scope            TEXT    NOT NULL,
    scope_id         TEXT,
    expires_at       TIMESTAMP,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME,
    access_count     INTEGER NOT NULL DEFAULT 0,
    last_accessed_at DATETIME,
    pinned           INTEGER NOT NULL DEFAULT 0,
    kind             TEXT    NOT NULL DEFAULT 'fact'
                     CHECK (kind IN ('fact', 'directive', 'lesson')),
    half_life_days   REAL,
    UNIQUE(scope, scope_id, key)
);

INSERT INTO memory_new SELECT
    id, key, value, value_type, scope, scope_id, expires_at, created_at, updated_at,
    access_count, last_accessed_at, pinned, kind, half_life_days
FROM memory;

DROP TABLE memory;
ALTER TABLE memory_new RENAME TO memory;

-- Recreate indexes dropped by the table rebuild.
CREATE INDEX IF NOT EXISTS idx_memory_scope_key ON memory(scope, key);
CREATE INDEX IF NOT EXISTS idx_memory_pinned    ON memory(pinned) WHERE pinned = 1;
CREATE INDEX IF NOT EXISTS idx_memory_kind      ON memory(kind)   WHERE kind = 'directive';

PRAGMA foreign_keys=ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert kind CHECK to ('fact', 'directive') — demote 'lesson' rows to 'fact'.
PRAGMA foreign_keys=OFF;

UPDATE memory SET kind = 'fact' WHERE kind = 'lesson';

CREATE TABLE memory_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    key              TEXT    NOT NULL,
    value            TEXT,
    value_type       TEXT    NOT NULL,
    scope            TEXT    NOT NULL,
    scope_id         TEXT,
    expires_at       TIMESTAMP,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME,
    access_count     INTEGER NOT NULL DEFAULT 0,
    last_accessed_at DATETIME,
    pinned           INTEGER NOT NULL DEFAULT 0,
    kind             TEXT    NOT NULL DEFAULT 'fact'
                     CHECK (kind IN ('fact', 'directive')),
    half_life_days   REAL,
    UNIQUE(scope, scope_id, key)
);

INSERT INTO memory_new SELECT
    id, key, value, value_type, scope, scope_id, expires_at, created_at, updated_at,
    access_count, last_accessed_at, pinned, kind, half_life_days
FROM memory;

DROP TABLE memory;
ALTER TABLE memory_new RENAME TO memory;

CREATE INDEX IF NOT EXISTS idx_memory_scope_key ON memory(scope, key);
CREATE INDEX IF NOT EXISTS idx_memory_pinned    ON memory(pinned) WHERE pinned = 1;
CREATE INDEX IF NOT EXISTS idx_memory_kind      ON memory(kind)   WHERE kind = 'directive';

PRAGMA foreign_keys=ON;
-- +goose StatementEnd
