-- +goose Up
-- +goose NO TRANSACTION

-- Normalize agent names to lowercase in events (single-pass).
UPDATE events SET agent_name = LOWER(TRIM(agent_name))
WHERE agent_name != LOWER(TRIM(agent_name));

-- Normalize agent_state: keep the row with the highest last_seen_event_id on collision.
-- Wrapped in explicit transaction for crash safety: a crash between DELETE and INSERT
-- would otherwise lose all agent_state rows permanently.
BEGIN;

CREATE TEMPORARY TABLE _agent_state_winners AS
SELECT
    LOWER(TRIM(agent_name)) AS agent_name,
    MAX(last_seen_event_id) AS last_seen_event_id,
    (SELECT focus_task_id FROM agent_state AS inner_as
     WHERE LOWER(TRIM(inner_as.agent_name)) = LOWER(TRIM(outer_as.agent_name))
     ORDER BY last_seen_event_id DESC LIMIT 1) AS focus_task_id,
    (SELECT focus_project_id FROM agent_state AS inner_as
     WHERE LOWER(TRIM(inner_as.agent_name)) = LOWER(TRIM(outer_as.agent_name))
     ORDER BY last_seen_event_id DESC LIMIT 1) AS focus_project_id,
    MAX(version) AS version,
    MAX(last_active_at) AS last_active_at
FROM agent_state AS outer_as
GROUP BY LOWER(TRIM(agent_name));

DELETE FROM agent_state;
INSERT INTO agent_state (agent_name, last_seen_event_id, focus_task_id, focus_project_id, version, last_active_at)
SELECT agent_name, last_seen_event_id, focus_task_id, focus_project_id, version, last_active_at
FROM _agent_state_winners;

DROP TABLE _agent_state_winners;

COMMIT;

-- Normalize idempotency: on collision keep completed request.
-- Wrapped in explicit transaction for crash safety: a crash between DELETE and INSERT
-- would otherwise lose all idempotency rows permanently.
BEGIN;

CREATE TEMPORARY TABLE _idempotency_winners AS
SELECT
    LOWER(TRIM(agent_name)) AS agent_name,
    request_id,
    command,
    (SELECT result_json FROM idempotency AS inner_i
     WHERE LOWER(TRIM(inner_i.agent_name)) = LOWER(TRIM(outer_i.agent_name))
       AND inner_i.request_id = outer_i.request_id
     ORDER BY LENGTH(result_json) DESC LIMIT 1) AS result_json,
    MIN(created_at) AS created_at
FROM idempotency AS outer_i
GROUP BY LOWER(TRIM(outer_i.agent_name)), request_id;

DELETE FROM idempotency;
INSERT INTO idempotency (agent_name, request_id, command, result_json, created_at)
SELECT agent_name, request_id, command, result_json, created_at
FROM _idempotency_winners;

DROP TABLE _idempotency_winners;

COMMIT;

-- Normalize tasks.claimed_by to lowercase.
UPDATE tasks SET claimed_by = LOWER(TRIM(claimed_by))
WHERE claimed_by IS NOT NULL AND claimed_by != LOWER(TRIM(claimed_by));

-- Normalize memory.scope_id for agent-scoped entries.
-- On collision (same key after lowercasing), keep the most recently created row.
BEGIN;

CREATE TEMPORARY TABLE _memory_agent_winners AS
SELECT
    key,
    LOWER(TRIM(scope_id)) AS scope_id,
    (SELECT m2.value FROM memory AS m2
     WHERE m2.scope = 'agent'
       AND LOWER(TRIM(m2.scope_id)) = LOWER(TRIM(outer_m.scope_id))
       AND m2.key = outer_m.key
     ORDER BY m2.created_at DESC LIMIT 1) AS value,
    (SELECT m2.value_type FROM memory AS m2
     WHERE m2.scope = 'agent'
       AND LOWER(TRIM(m2.scope_id)) = LOWER(TRIM(outer_m.scope_id))
       AND m2.key = outer_m.key
     ORDER BY m2.created_at DESC LIMIT 1) AS value_type,
    'agent' AS scope,
    (SELECT m2.expires_at FROM memory AS m2
     WHERE m2.scope = 'agent'
       AND LOWER(TRIM(m2.scope_id)) = LOWER(TRIM(outer_m.scope_id))
       AND m2.key = outer_m.key
     ORDER BY m2.created_at DESC LIMIT 1) AS expires_at,
    MAX(created_at) AS created_at
FROM memory AS outer_m
WHERE scope = 'agent'
GROUP BY LOWER(TRIM(scope_id)), key;

DELETE FROM memory WHERE scope = 'agent';
INSERT INTO memory (key, scope_id, value, value_type, scope, expires_at, created_at)
SELECT key, scope_id, value, value_type, scope, expires_at, created_at
FROM _memory_agent_winners;

DROP TABLE _memory_agent_winners;

COMMIT;

-- +goose Down

-- Cannot reverse: original casing is lost
SELECT 1;
