-- +goose Up
-- +goose StatementBegin
ALTER TABLE memory ADD COLUMN half_life_days REAL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE memory DROP COLUMN half_life_days;
-- +goose StatementEnd
