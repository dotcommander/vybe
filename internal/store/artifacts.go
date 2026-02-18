package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// generateArtifactID returns a unique artifact identifier with the "artifact" prefix.
func generateArtifactID() string {
	return generatePrefixedID("artifact")
}

// AddArtifact creates an artifact linked to a task by first appending an event and then inserting the artifact row
// in the same transaction. Returns the artifact and the event id.
func AddArtifact(db *sql.DB, agentName, taskID, filePath, contentType string) (*models.Artifact, int64, error) {
	if agentName == "" {
		return nil, 0, errors.New("agent name is required")
	}
	if taskID == "" {
		return nil, 0, errors.New("task ID is required")
	}
	if filePath == "" {
		return nil, 0, errors.New("file path is required")
	}

	artifactID := generateArtifactID()
	meta := struct {
		ArtifactID  string `json:"artifact_id"`
		FilePath    string `json:"file_path"`
		ContentType string `json:"content_type,omitempty"`
	}{
		ArtifactID:  artifactID,
		FilePath:    filePath,
		ContentType: contentType,
	}
	metaBytes, _ := json.Marshal(meta)

	var (
		eventID  int64
		artifact *models.Artifact
	)

	err := Transact(db, func(tx *sql.Tx) error {
		projectID, err := resolveTaskProjectIDTx(tx, taskID)
		if err != nil {
			return err
		}

		id, err := InsertEventTx(tx, models.EventKindArtifactAdded, agentName, taskID, fmt.Sprintf("Artifact added: %s", filePath), string(metaBytes))
		if err != nil {
			return fmt.Errorf("failed to append event: %w", err)
		}
		eventID = id

		_, err = tx.ExecContext(context.Background(), `
			INSERT INTO artifacts (id, task_id, project_id, event_id, file_path, content_type, created_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, artifactID, taskID, projectID, eventID, filePath, nullIfEmpty(contentType))
		if err != nil {
			return fmt.Errorf("failed to insert artifact: %w", err)
		}

		var a models.Artifact
		var ct sql.NullString
		err = tx.QueryRowContext(context.Background(), `
			SELECT id, task_id, event_id, file_path, content_type, created_at
			FROM artifacts
			WHERE id = ?
		`, artifactID).Scan(&a.ID, &a.TaskID, &a.EventID, &a.FilePath, &ct, &a.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to fetch artifact: %w", err)
		}
		if ct.Valid {
			a.ContentType = ct.String
		}
		artifact = &a

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return artifact, eventID, nil
}

// AddArtifactIdempotent performs AddArtifact once per (agent_name, request_id).
// On retries with the same request id, it returns the originally created artifact + event id.
//nolint:revive // argument-limit: all artifact params are required and distinct; a struct would add boilerplate at every callsite
func AddArtifactIdempotent(db *sql.DB, agentName, requestID, taskID, filePath, contentType string) (*models.Artifact, int64, error) {
	if agentName == "" {
		return nil, 0, errors.New("agent name is required")
	}
	if taskID == "" {
		return nil, 0, errors.New("task ID is required")
	}
	if filePath == "" {
		return nil, 0, errors.New("file path is required")
	}

	type idemResult struct {
		ArtifactID string `json:"artifact_id"`
		EventID    int64  `json:"event_id"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "artifact.add", func(tx *sql.Tx) (idemResult, error) {
		artifactID := generateArtifactID()
		projectID, err := resolveTaskProjectIDTx(tx, taskID)
		if err != nil {
			return idemResult{}, err
		}

		meta := struct {
			ArtifactID  string `json:"artifact_id"`
			FilePath    string `json:"file_path"`
			ContentType string `json:"content_type,omitempty"`
		}{
			ArtifactID:  artifactID,
			FilePath:    filePath,
			ContentType: contentType,
		}
		metaBytes, _ := json.Marshal(meta)

		eventID, err := InsertEventTx(tx, models.EventKindArtifactAdded, agentName, taskID, fmt.Sprintf("Artifact added: %s", filePath), string(metaBytes))
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		_, err = tx.ExecContext(context.Background(), `
			INSERT INTO artifacts (id, task_id, project_id, event_id, file_path, content_type, created_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, artifactID, taskID, projectID, eventID, filePath, nullIfEmpty(contentType))
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to insert artifact: %w", err)
		}

		return idemResult{ArtifactID: artifactID, EventID: eventID}, nil
	})
	if err != nil {
		return nil, 0, err
	}

	artifact, err := GetArtifact(db, r.ArtifactID)
	if err != nil {
		return nil, 0, err
	}

	return artifact, r.EventID, nil
}

// GetArtifact retrieves a single artifact by ID.
func GetArtifact(db *sql.DB, id string) (*models.Artifact, error) {
	var a models.Artifact
	var ct sql.NullString
	err := RetryWithBackoff(func() error {
		return db.QueryRowContext(context.Background(), `
			SELECT id, task_id, event_id, file_path, content_type, created_at
			FROM artifacts
			WHERE id = ?
		`, id).Scan(&a.ID, &a.TaskID, &a.EventID, &a.FilePath, &ct, &a.CreatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("artifact not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get artifact: %w", err)
	}
	if ct.Valid {
		a.ContentType = ct.String
	}
	return &a, nil
}

// ListArtifactsByTask returns artifacts linked to a task, newest first.
func ListArtifactsByTask(db *sql.DB, taskID string, limit int) ([]*models.Artifact, error) {
	if taskID == "" {
		return nil, errors.New("task ID is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	var out []*models.Artifact
	err := RetryWithBackoff(func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, task_id, event_id, file_path, content_type, created_at
			FROM artifacts
			WHERE task_id = ?
			ORDER BY created_at DESC
			LIMIT ?
		`, taskID, limit)
		if err != nil {
			return fmt.Errorf("failed to list artifacts: %w", err)
		}
		defer func() { _ = rows.Close() }()

		out = make([]*models.Artifact, 0)
		for rows.Next() {
			var a models.Artifact
			var ct sql.NullString
			if err := rows.Scan(&a.ID, &a.TaskID, &a.EventID, &a.FilePath, &ct, &a.CreatedAt); err != nil {
				return fmt.Errorf("failed to scan artifact: %w", err)
			}
			if ct.Valid {
				a.ContentType = ct.String
			}
			out = append(out, &a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func resolveTaskProjectIDTx(tx *sql.Tx, taskID string) (any, error) {
	if taskID == "" {
		return nil, nil
	}

	var projectID sql.NullString
	err := tx.QueryRowContext(context.Background(), `SELECT project_id FROM tasks WHERE id = ?`, taskID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to resolve artifact project: %w", err)
	}
	if !projectID.Valid {
		return nil, nil
	}
	return projectID.String, nil
}
