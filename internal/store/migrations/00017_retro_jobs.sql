-- +goose Up
-- +goose StatementBegin

CREATE TABLE retrospective_jobs (
    id TEXT PRIMARY KEY,
    agent_name TEXT NOT NULL,
    project_id TEXT,
    session_id TEXT,
    status TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    next_run_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_by TEXT,
    claim_expires_at TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE INDEX idx_retro_jobs_status_next_run_at ON retrospective_jobs(status, next_run_at);
CREATE INDEX idx_retro_jobs_claim_expires_at ON retrospective_jobs(claim_expires_at);
CREATE INDEX idx_retro_jobs_agent_created_at ON retrospective_jobs(agent_name, created_at);

CREATE UNIQUE INDEX idx_retro_jobs_agent_session_dedupe
ON retrospective_jobs(agent_name, session_id)
WHERE session_id IS NOT NULL AND session_id <> '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_retro_jobs_agent_session_dedupe;
DROP INDEX IF EXISTS idx_retro_jobs_agent_created_at;
DROP INDEX IF EXISTS idx_retro_jobs_claim_expires_at;
DROP INDEX IF EXISTS idx_retro_jobs_status_next_run_at;
DROP TABLE IF EXISTS retrospective_jobs;

-- +goose StatementEnd
