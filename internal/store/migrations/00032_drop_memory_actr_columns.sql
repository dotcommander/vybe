-- +goose Up
-- +goose StatementBegin
ALTER TABLE memory DROP COLUMN access_count;
ALTER TABLE memory DROP COLUMN last_accessed_at;
ALTER TABLE memory DROP COLUMN half_life_days;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE memory ADD COLUMN access_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE memory ADD COLUMN last_accessed_at DATETIME;
ALTER TABLE memory ADD COLUMN half_life_days REAL;
-- +goose StatementEnd
