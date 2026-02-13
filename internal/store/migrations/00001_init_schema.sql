-- +goose Up
-- +goose StatementBegin

-- Events table: append-only event log, core continuity primitive
CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    task_id TEXT,
    message TEXT NOT NULL,
    metadata TEXT, -- JSON
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_events_id ON events(id);
CREATE INDEX idx_events_agent_name ON events(agent_name);
CREATE INDEX idx_events_task_id ON events(task_id);

-- Tasks table: task definitions and status
CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tasks_status ON tasks(status);

-- Agent state: cursor position and focus tracking
CREATE TABLE agent_state (
    agent_name TEXT PRIMARY KEY,
    last_seen_event_id INTEGER NOT NULL DEFAULT 0,
    focus_task_id TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    last_active_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Memory: key-value storage with scoping
CREATE TABLE memory (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL,
    value TEXT,
    value_type TEXT NOT NULL,
    scope TEXT NOT NULL,
    scope_id TEXT,
    expires_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(scope, scope_id, key)
);

CREATE INDEX idx_memory_scope_key ON memory(scope, key);

-- Artifacts: file outputs and content
CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    event_id INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    content_type TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (task_id) REFERENCES tasks(id),
    FOREIGN KEY (event_id) REFERENCES events(id)
);

-- Projects: project metadata
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    metadata TEXT, -- JSON
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS memory;
DROP TABLE IF EXISTS agent_state;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS events;

-- +goose StatementEnd
