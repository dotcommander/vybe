-- +goose Up
DROP TABLE IF EXISTS retrospective_jobs;

-- +goose Down
-- Retrospective jobs table removed; queue system replaced with synchronous best-effort.
-- Re-creation would require restoring the full schema from 00017 + 00018.
