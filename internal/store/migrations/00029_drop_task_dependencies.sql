-- +goose Up
-- task_dependencies was never wired at the store layer: zero INSERT/SELECT in
-- code, the only reference is an FK-cascade comment. Dependency-blocking is
-- modeled by the free-form tasks.blocked_reason="dependency" convention
-- (models.BlockedReasonDependency) + resume Rule 1.5, not this relational table.
DROP INDEX IF EXISTS idx_task_deps_task_id;
DROP INDEX IF EXISTS idx_task_deps_depends_on;
DROP TABLE IF EXISTS task_dependencies;

-- +goose Down
CREATE TABLE IF NOT EXISTS task_dependencies (
    task_id TEXT NOT NULL,
    depends_on_task_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, depends_on_task_id),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
    FOREIGN KEY (depends_on_task_id) REFERENCES tasks(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_task_deps_depends_on ON task_dependencies(depends_on_task_id);
CREATE INDEX IF NOT EXISTS idx_task_deps_task_id ON task_dependencies(task_id);
