-- +goose Up
DROP INDEX IF EXISTS idx_tasks_claimed_by;
DROP INDEX IF EXISTS idx_tasks_claim_expires_at;
ALTER TABLE tasks DROP COLUMN claimed_by;
ALTER TABLE tasks DROP COLUMN claimed_at;
ALTER TABLE tasks DROP COLUMN claim_expires_at;
ALTER TABLE tasks DROP COLUMN last_heartbeat_at;
ALTER TABLE tasks DROP COLUMN attempt;

-- +goose Down
ALTER TABLE tasks ADD COLUMN claimed_by TEXT;
ALTER TABLE tasks ADD COLUMN claimed_at TIMESTAMP;
ALTER TABLE tasks ADD COLUMN claim_expires_at TIMESTAMP;
ALTER TABLE tasks ADD COLUMN last_heartbeat_at TIMESTAMP;
ALTER TABLE tasks ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
