-- +goose Up
ALTER TABLE tasks ADD COLUMN blocked_reason TEXT;

-- +goose Down
-- SQLite doesn't support DROP COLUMN directly; this is a no-op for down.
