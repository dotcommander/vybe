-- +goose Up
-- +goose StatementBegin
ALTER TABLE memory ADD COLUMN access_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE memory ADD COLUMN last_accessed_at DATETIME;
UPDATE memory SET last_accessed_at = updated_at;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE memory DROP COLUMN access_count;
ALTER TABLE memory DROP COLUMN last_accessed_at;
-- +goose StatementEnd
