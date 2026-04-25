-- +goose Up
-- +goose StatementBegin

ALTER TABLE memory ADD COLUMN source_event_id INTEGER;
ALTER TABLE memory ADD COLUMN source_task_id  TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE memory DROP COLUMN source_task_id;
ALTER TABLE memory DROP COLUMN source_event_id;

-- +goose StatementEnd
