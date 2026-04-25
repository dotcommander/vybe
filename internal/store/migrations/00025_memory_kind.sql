-- +goose Up
-- +goose StatementBegin
ALTER TABLE memory ADD COLUMN kind TEXT NOT NULL DEFAULT 'fact'
  CHECK (kind IN ('fact', 'directive'));

CREATE INDEX IF NOT EXISTS idx_memory_kind ON memory(kind) WHERE kind = 'directive';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_memory_kind;
ALTER TABLE memory DROP COLUMN kind;
-- +goose StatementEnd
