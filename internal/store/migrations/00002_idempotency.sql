-- +goose Up
-- +goose StatementBegin

-- Idempotency: used to make mutating operations safe under retries.
-- Rows are written transactionally with the operation they guard so we don't
-- leave behind "in progress" locks on crashes.
CREATE TABLE idempotency (
    agent_name TEXT NOT NULL,
    request_id TEXT NOT NULL,
    command TEXT NOT NULL,
    result_json TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_name, request_id)
);

CREATE INDEX idx_idempotency_agent ON idempotency(agent_name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS idempotency;

-- +goose StatementEnd

