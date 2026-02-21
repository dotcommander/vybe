-- +goose Up
-- +goose StatementBegin

ALTER TABLE retrospective_jobs ADD COLUMN since_event_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE retrospective_jobs ADD COLUMN until_event_id INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN for existing tables in-place in a way
-- compatible with this migration strategy. Keep columns on rollback.
SELECT 1;

-- +goose StatementEnd
