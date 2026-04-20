-- +goose Up
-- +goose StatementBegin
ALTER TABLE memory ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_memory_pinned ON memory(pinned) WHERE pinned = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_memory_pinned;
ALTER TABLE memory DROP COLUMN pinned;
-- +goose StatementEnd
