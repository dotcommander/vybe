package store

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vibe/internal/models"
)

// GenerateProjectID generates a project ID using pattern: proj_<unix_nano>_<random_hex>.
func GenerateProjectID() string {
	return generatePrefixedID("proj")
}

// CreateProject inserts a new project and returns the created record.
func CreateProject(db *sql.DB, name, metadata string) (*models.Project, error) {
	var project *models.Project

	err := Transact(db, func(tx *sql.Tx) error {
		createdProject, err := CreateProjectTx(tx, name, metadata)
		if err != nil {
			return err
		}
		project = createdProject
		return nil
	})

	if err != nil {
		return nil, err
	}

	return project, nil
}

// CreateProjectTx inserts and returns a project inside an existing transaction.
func CreateProjectTx(tx *sql.Tx, name, metadata string) (*models.Project, error) {
	projectID := GenerateProjectID()
	meta := any(nil)
	if metadata != "" {
		meta = metadata
	}

	result, err := tx.Exec(`
		INSERT INTO projects (id, name, metadata, created_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, projectID, name, meta)
	if err != nil {
		return nil, fmt.Errorf("failed to insert project: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, fmt.Errorf("failed to insert project: no rows affected")
	}

	var project models.Project
	var metaCol sql.NullString
	err = tx.QueryRow(`
		SELECT id, name, metadata, created_at
		FROM projects WHERE id = ?
	`, projectID).Scan(&project.ID, &project.Name, &metaCol, &project.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch created project: %w", err)
	}
	if metaCol.Valid {
		project.Metadata = metaCol.String
	}

	return &project, nil
}

// EnsureProjectByID creates a project with the given ID if it doesn't exist,
// then returns the (possibly pre-existing) row. Uses INSERT OR IGNORE for
// idempotent creation.
func EnsureProjectByID(db *sql.DB, id, name string) (*models.Project, error) {
	if id == "" {
		return nil, fmt.Errorf("project id is required")
	}
	if name == "" {
		return nil, fmt.Errorf("project name is required")
	}

	var project models.Project
	err := Transact(db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO projects (id, name, created_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
		`, id, name); err != nil {
			return fmt.Errorf("failed to ensure project: %w", err)
		}

		var metaCol sql.NullString
		if err := tx.QueryRow(`
			SELECT id, name, metadata, created_at
			FROM projects WHERE id = ?
		`, id).Scan(&project.ID, &project.Name, &metaCol, &project.CreatedAt); err != nil {
			return fmt.Errorf("failed to fetch project: %w", err)
		}
		if metaCol.Valid {
			project.Metadata = metaCol.String
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &project, nil
}

// GetProject retrieves a project by ID.
func GetProject(db *sql.DB, projectID string) (*models.Project, error) {
	var project models.Project
	var metadata sql.NullString

	err := RetryWithBackoff(func() error {
		return db.QueryRow(`
			SELECT id, name, metadata, created_at
			FROM projects WHERE id = ?
		`, projectID).Scan(&project.ID, &project.Name, &metadata, &project.CreatedAt)
	})

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project not found: %s", projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query project: %w", err)
	}

	if metadata.Valid {
		project.Metadata = metadata.String
	}

	return &project, nil
}

// ListProjects retrieves all projects ordered by creation time (newest first).
func ListProjects(db *sql.DB) ([]*models.Project, error) {
	var projects []*models.Project

	err := RetryWithBackoff(func() error {
		rows, err := db.Query(`
			SELECT id, name, metadata, created_at
			FROM projects
			ORDER BY created_at DESC
		`)
		if err != nil {
			return fmt.Errorf("failed to query projects: %w", err)
		}
		defer rows.Close()

		projects = make([]*models.Project, 0)
		for rows.Next() {
			var p models.Project
			var metadata sql.NullString
			err := rows.Scan(&p.ID, &p.Name, &metadata, &p.CreatedAt)
			if err != nil {
				return fmt.Errorf("failed to scan project row: %w", err)
			}
			if metadata.Valid {
				p.Metadata = metadata.String
			}
			projects = append(projects, &p)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}

	return projects, nil
}
