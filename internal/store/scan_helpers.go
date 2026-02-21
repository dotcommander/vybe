package store

import (
	"database/sql"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// scanNullString converts sql.NullString to string (empty if NULL)
func scanNullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// scanNullTime converts sql.NullTime to *time.Time (nil if NULL)
func scanNullTime(nt sql.NullTime) *time.Time {
	if nt.Valid {
		return &nt.Time
	}
	return nil
}

// taskRowScanner encapsulates the common task row scanning logic.
type taskRowScanner struct {
	task          models.Task
	projID        sql.NullString
	blockedReason sql.NullString
}

func (s *taskRowScanner) scan(row interface {
	Scan(dest ...any) error
}) error {
	return row.Scan(
		&s.task.ID,
		&s.task.Title,
		&s.task.Description,
		&s.task.Status,
		&s.task.Priority,
		&s.projID,
		&s.blockedReason,
		&s.task.Version,
		&s.task.CreatedAt,
		&s.task.UpdatedAt,
	)
}

func (s *taskRowScanner) hydrate() {
	s.task.ProjectID = scanNullString(s.projID)
	if s.blockedReason.Valid {
		s.task.BlockedReason = models.BlockedReason(s.blockedReason.String)
	}
}

func (s *taskRowScanner) getTask() *models.Task {
	return &s.task
}

// scanTaskRow is a helper that scans and hydrates a task from a single row.
func scanTaskRow(row interface {
	Scan(dest ...any) error
}) (*models.Task, error) {
	scanner := &taskRowScanner{}
	if err := scanner.scan(row); err != nil {
		return nil, err
	}
	scanner.hydrate()
	return scanner.getTask(), nil
}
