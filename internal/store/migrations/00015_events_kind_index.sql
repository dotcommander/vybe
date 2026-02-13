-- +goose Up
CREATE INDEX IF NOT EXISTS idx_events_kind_archived ON events(kind, archived_at, id);

-- +goose Down
DROP INDEX IF EXISTS idx_events_kind_archived;
