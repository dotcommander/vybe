-- +goose Up
-- +goose StatementBegin

ALTER TABLE events ADD COLUMN archived_at TIMESTAMP;
CREATE INDEX idx_events_archived_at ON events(archived_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_events_archived_at;

-- +goose StatementEnd
