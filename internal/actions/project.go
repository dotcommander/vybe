package actions

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// ProjectCreate atomically inserts a project and logs a project_created event.
func ProjectCreate(db *sql.DB, agentName, name, metadata string) (*models.Project, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if name == "" {
		return nil, 0, fmt.Errorf("project name is required")
	}

	var (
		project *models.Project
		eventID int64
	)

	if err := store.Transact(db, func(tx *sql.Tx) error {
		var err error
		project, err = store.CreateProjectTx(tx, name, metadata)
		if err != nil {
			return err
		}

		lastEventID, err := store.InsertEventTx(tx, models.EventKindProjectCreated, agentName, "", fmt.Sprintf("Project created: %s", name), "")
		if err != nil {
			return fmt.Errorf("failed to append event: %w", err)
		}
		eventID = lastEventID
		return nil
	}); err != nil {
		return nil, 0, fmt.Errorf("failed to create project: %w", err)
	}

	return project, eventID, nil
}

// ProjectCreateIdempotent creates a project once per (agent_name, request_id).
// On retries with the same request id, it returns the originally created project + event id.
func ProjectCreateIdempotent(db *sql.DB, agentName, requestID, name, metadata string) (*models.Project, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, 0, fmt.Errorf("request id is required")
	}
	if name == "" {
		return nil, 0, fmt.Errorf("project name is required")
	}

	type idemResult struct {
		ProjectID string `json:"project_id"`
		EventID   int64  `json:"event_id"`
	}

	r, err := store.RunIdempotent(db, agentName, requestID, "project.create", func(tx *sql.Tx) (idemResult, error) {
		createdProject, err := store.CreateProjectTx(tx, name, metadata)
		if err != nil {
			return idemResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, models.EventKindProjectCreated, agentName, "", fmt.Sprintf("Project created: %s", name), "")
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{ProjectID: createdProject.ID, EventID: eventID}, nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create project: %w", err)
	}

	project, err := store.GetProject(db, r.ProjectID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch created project: %w", err)
	}

	return project, r.EventID, nil
}

// ProjectFocus sets the agent's focus project. Empty projectID clears focus.
func ProjectFocus(db *sql.DB, agentName, projectID string) (int64, error) {
	if agentName == "" {
		return 0, fmt.Errorf("agent name is required")
	}
	return store.SetAgentFocusProjectWithEvent(db, agentName, projectID)
}

// ProjectFocusIdempotent sets the agent's focus project once per (agent_name, request_id).
func ProjectFocusIdempotent(db *sql.DB, agentName, requestID, projectID string) (int64, error) {
	if agentName == "" {
		return 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return 0, fmt.Errorf("request id is required")
	}
	return store.SetAgentFocusProjectWithEventIdempotent(db, agentName, requestID, projectID)
}

// ProjectGet retrieves a project by ID.
func ProjectGet(db *sql.DB, projectID string) (*models.Project, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project ID is required")
	}

	project, err := store.GetProject(db, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	return project, nil
}

// ProjectList retrieves all projects.
func ProjectList(db *sql.DB) ([]*models.Project, error) {
	projects, err := store.ListProjects(db)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	return projects, nil
}
