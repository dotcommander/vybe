# The Idempotent Action Pattern

Developer specification for adding new functionality to vybe without breaking
data consistency or resume capabilities.

## Architecture rule

**All state changes must be idempotent and event-sourced.**

- **Never** write to the database without a `request_id`.
- **Never** mutate state without appending a corresponding event log.
- **Always** wrap the mutation and the event in a single transaction.

## Implementation pattern

When adding a new feature (e.g., "Assign Label"), follow this strict 3-layer
separation:

### Step A: Store Primitive (`internal/store/`)

Create a transaction-aware function. It only knows SQL. It does **not** manage
transaction commit/rollback.

```go
// AssignLabelTx performs the row update within an existing transaction.
// It returns an error if the label doesn't exist or optimistic locking fails.
func AssignLabelTx(tx *sql.Tx, taskID, label string) error {
    // 1. Validate inputs inside the TX (consistency check)
    // 2. Perform the UPDATE/INSERT
    _, err := tx.Exec(`INSERT INTO labels ...`, ...)
    return err
}
```

### Step B: Action Workflow (`internal/actions/`)

Business logic lives here. It orchestrates the store primitive and event log.

```go
func AssignLabelIdempotent(db *sql.DB, agentName, requestID, taskID, label string) error {
    cmdKey := "task.assign_label"

    _, err := store.RunIdempotent(db, agentName, requestID, cmdKey, func(tx *sql.Tx) (struct{}, error) {
        // A. Call the Store Primitive
        if err := store.AssignLabelTx(tx, taskID, label); err != nil {
            return struct{}{}, err
        }

        // B. Log the Event (CRITICAL: Must happen in same TX)
        msg := fmt.Sprintf("Assigned label: %s", label)
        if _, err := store.InsertEventTx(tx, "label_assigned", agentName, taskID, msg, ""); err != nil {
            return struct{}{}, fmt.Errorf("failed to log event: %w", err)
        }

        return struct{}{}, nil
    })

    return err
}
```

### Step C: Command Interface (`internal/commands/`)

The CLI layer only parses flags and calls the action.

```go
func newLabelAssignCmd() *cobra.Command {
    return &cobra.Command{
        Use: "assign",
        RunE: func(cmd *cobra.Command, args []string) error {
            agent, _ := requireActorName(cmd, "")
            reqID, _ := requireRequestID(cmd)

            return withDB(func(db *DB) error {
                return actions.AssignLabelIdempotent(db, agent, reqID, taskID, label)
            })
        },
    }
}
```

## Why this matters

- **Crash safety:** If the program crashes after the mutation but before the
  event insert, the database rolls back. State never drifts from the event log.
- **Retry safety:** If the agent retries with the same `request_id`,
  `RunIdempotent` detects it and returns the previous result without
  duplicating the mutation or event.
- **Resume capability:** Because `InsertEventTx` is mandatory, the resume logic
  can see this action occurred and build context for the next agent invocation.

## Code review checklist

Reject any PR that:

1. Calls `db.Exec` directly in a command handler.
2. Performs a mutation without a corresponding `store.InsertEventTx`.
3. Uses `db.Begin()` manually instead of `store.RunIdempotent` (for mutations).
   - **Exception:** `store/tx.go` internals and explicit idempotency tests.
4. Formats human-readable strings inside `internal/store/` SQL functions
   (move strings to `actions/`).
