-- +goose Up
-- +goose StatementBegin
-- Event archiving/summarization removed; events are now unbounded append-only
-- (consistent with vybe's append-only-truth design). No replacement prune.
DROP INDEX IF EXISTS idx_events_archived_at;
DROP INDEX IF EXISTS idx_events_kind_archived;
ALTER TABLE events DROP COLUMN archived_at;
CREATE INDEX IF NOT EXISTS idx_events_kind_id ON events(kind, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE events ADD COLUMN archived_at TIMESTAMP;
CREATE INDEX idx_events_archived_at ON events(archived_at);
CREATE INDEX idx_events_kind_archived ON events(kind, archived_at, id);
DROP INDEX IF EXISTS idx_events_kind_id;
-- +goose StatementEnd
