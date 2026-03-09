package actions

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// ProjectDeleteIdempotent deletes a project and appends a project_deleted event, idempotent on request_id.
func ProjectDeleteIdempotent(db *sql.DB, agentName, requestID, projectID string) (int64, error) {
	return runDeleteIdempotent(db, deleteParams{
		AgentName:    agentName,
		RequestID:    requestID,
		ResourceID:   projectID,
		ResourceName: "project",
		Command:      "project.delete",
		EventKind:    models.EventKindProjectDeleted,
		EventTaskID:  "",
		EventMessage: fmt.Sprintf("Project deleted: %s", projectID),
		DeleteFn: func(tx *sql.Tx) error {
			return store.DeleteProjectTx(tx, projectID)
		},
	})
}
