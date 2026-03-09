package actions

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// TaskDeleteIdempotent deletes a task and appends a task_deleted event, idempotent on request_id.
func TaskDeleteIdempotent(db *sql.DB, agentName, requestID, taskID string) (int64, error) {
	return runDeleteIdempotent(db, deleteParams{
		AgentName:    agentName,
		RequestID:    requestID,
		ResourceID:   taskID,
		ResourceName: "task",
		Command:      "task.delete",
		EventKind:    models.EventKindTaskDeleted,
		EventTaskID:  taskID,
		EventMessage: fmt.Sprintf("Task deleted: %s", taskID),
		DeleteFn: func(tx *sql.Tx) error {
			return store.DeleteTaskTx(tx, agentName, taskID)
		},
	})
}
