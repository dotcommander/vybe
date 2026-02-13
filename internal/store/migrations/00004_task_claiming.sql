-- +goose Up
ALTER TABLE tasks ADD COLUMN claimed_by TEXT;
ALTER TABLE tasks ADD COLUMN claimed_at TIMESTAMP;
ALTER TABLE tasks ADD COLUMN claim_expires_at TIMESTAMP;
CREATE INDEX idx_tasks_claimed_by ON tasks(claimed_by);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_claimed_by;
-- SQLite doesn't support DROP COLUMN before 3.35.0, but migrations are forward-only
