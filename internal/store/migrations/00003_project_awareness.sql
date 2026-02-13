-- +goose Up
-- +goose StatementBegin

-- Project-aware agent state: link agents and tasks to projects.
ALTER TABLE agent_state ADD COLUMN focus_project_id TEXT;
ALTER TABLE tasks ADD COLUMN project_id TEXT;
CREATE INDEX idx_tasks_project_id ON tasks(project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_tasks_project_id;
-- SQLite does not support DROP COLUMN; down migration is best-effort.

-- +goose StatementEnd
