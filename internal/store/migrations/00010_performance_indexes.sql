-- +goose Up
CREATE INDEX IF NOT EXISTS idx_task_deps_task_id ON task_dependencies(task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_focus_selection ON tasks(status, project_id, priority DESC, created_at ASC);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_focus_selection;
DROP INDEX IF EXISTS idx_task_deps_task_id;
