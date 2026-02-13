-- +goose Up
-- +goose StatementBegin

-- Canonical reconciliation and unique index are handled in Go post-migration
-- (reconcileCanonicalKeys + createCanonicalIndex in migrate.go).
-- SQL migration is intentionally empty to avoid index creation before
-- collision resolution.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_memory_active_canonical;

-- +goose StatementEnd
