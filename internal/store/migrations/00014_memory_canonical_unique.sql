-- +goose Up
CREATE UNIQUE INDEX idx_memory_canonical_unique
ON memory(scope, scope_id, canonical_key)
WHERE superseded_by IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_memory_canonical_unique;
