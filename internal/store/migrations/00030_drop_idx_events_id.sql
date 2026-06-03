-- +goose Up
-- idx_events_id was redundant: events.id is INTEGER PRIMARY KEY AUTOINCREMENT,
-- which is an alias for the rowid, so a secondary index over events(id) is pure
-- write amplification with zero query benefit. The rowid B-tree already serves
-- all id lookups and ordered scans.
DROP INDEX IF EXISTS idx_events_id;

-- +goose Down
CREATE INDEX IF NOT EXISTS idx_events_id ON events(id);
