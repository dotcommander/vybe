-- +goose Up
-- Back the ListMemoryBySource provenance reader (memory list --by-source-*).
-- source_event_id / source_task_id were added in 00028 without indexes; the
-- reader is now a live CLI path, so without these it full-scans the memory table.
-- Partial (WHERE NOT NULL) indexes keep them small — most rows have no provenance.
CREATE INDEX IF NOT EXISTS idx_memory_source_event_id ON memory(source_event_id) WHERE source_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memory_source_task_id ON memory(source_task_id) WHERE source_task_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_memory_source_task_id;
DROP INDEX IF EXISTS idx_memory_source_event_id;
