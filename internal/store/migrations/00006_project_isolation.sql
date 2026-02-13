-- +goose Up
-- +goose StatementBegin

-- Isolate event streams by project for workspace-safe resume.
ALTER TABLE events ADD COLUMN project_id TEXT;
CREATE INDEX idx_events_project_cursor ON events(project_id, id);

-- Keep artifact ownership project-aware.
ALTER TABLE artifacts ADD COLUMN project_id TEXT;
CREATE INDEX idx_artifacts_project_id ON artifacts(project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_artifacts_project_id;
DROP INDEX IF EXISTS idx_events_project_cursor;
-- SQLite does not support DROP COLUMN; down migration is best-effort.

-- +goose StatementEnd
