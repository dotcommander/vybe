package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

func fetchArtifacts(db *sql.DB, taskID string) ([]*models.Artifact, error) {
	var artifacts []*models.Artifact
	err := RetryWithBackoff(context.Background(), func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, task_id, event_id, file_path, content_type, created_at
			FROM artifacts
			WHERE task_id = ?
			ORDER BY created_at DESC
			LIMIT 100
		`, taskID)
		if err != nil {
			return fmt.Errorf("failed to query artifacts: %w", err)
		}
		defer func() { _ = rows.Close() }()

		artifacts = make([]*models.Artifact, 0)
		for rows.Next() {
			var artifact models.Artifact
			var contentType sql.NullString
			if err := rows.Scan(
				&artifact.ID,
				&artifact.TaskID,
				&artifact.EventID,
				&artifact.FilePath,
				&contentType,
				&artifact.CreatedAt,
			); err != nil {
				return fmt.Errorf("failed to scan artifact: %w", err)
			}
			if contentType.Valid {
				artifact.ContentType = contentType.String
			}
			artifacts = append(artifacts, &artifact)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return artifacts, nil
}
