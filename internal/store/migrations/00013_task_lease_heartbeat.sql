-- +goose Up
ALTER TABLE tasks ADD COLUMN last_heartbeat_at TIMESTAMP;
ALTER TABLE tasks ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_tasks_claim_expires_at ON tasks(claim_expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_claim_expires_at;
-- SQLite migrations are forward-only for added columns.
