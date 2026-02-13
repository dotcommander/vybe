-- +goose Up
ALTER TABLE tasks ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_tasks_priority ON tasks(priority DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_priority;
-- SQLite doesn't support DROP COLUMN before 3.35.0, but migrations are forward-only
