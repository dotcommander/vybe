package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

const maxRetrospectiveJobErrorLen = 2048

// EnqueueRetrospectiveJobTx creates a queued retrospective job.
// If sessionID is non-empty and a duplicate exists for (agent_name, session_id),
// it returns the existing job instead.
func EnqueueRetrospectiveJobTx(tx *sql.Tx, agentName, projectID, sessionID string, sinceEventID, untilEventID int64, maxAttempts int) (*models.RetrospectiveJob, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if sinceEventID < 0 {
		sinceEventID = 0
	}
	if untilEventID < 0 {
		untilEventID = 0
	}
	if untilEventID < sinceEventID {
		untilEventID = sinceEventID
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	jobID := generatePrefixedID("retro")
	_, err := tx.ExecContext(context.Background(), `
		INSERT INTO retrospective_jobs (
			id, agent_name, project_id, session_id, since_event_id, until_event_id, status, attempt, max_attempts,
			next_run_at, claimed_by, claim_expires_at, last_error, created_at, updated_at, completed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, CURRENT_TIMESTAMP, NULL, NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, NULL)
	`, jobID, agentName, nullableText(projectID), nullableText(sessionID), sinceEventID, untilEventID, models.RetrospectiveJobQueued, maxAttempts)
	if err != nil {
		if IsUniqueConstraintErr(err) && sessionID != "" {
			return getRetrospectiveJobByAgentSessionTx(tx, agentName, sessionID)
		}
		return nil, fmt.Errorf("failed to enqueue retrospective job: %w", err)
	}

	return getRetrospectiveJobByIDTx(tx, jobID)
}

// ClaimNextRetrospectiveJobTx claims the next due retrospective job.
// Returns (nil, nil) when no due job is available.
func ClaimNextRetrospectiveJobTx(tx *sql.Tx, workerName string, leaseSeconds int) (*models.RetrospectiveJob, error) {
	if workerName == "" {
		return nil, errors.New("worker name is required")
	}
	if leaseSeconds <= 0 {
		leaseSeconds = 60
	}
	if leaseSeconds > 3600 {
		leaseSeconds = 3600
	}

	for range 5 {
		var candidateID string
		err := tx.QueryRowContext(context.Background(), `
			SELECT id
			FROM retrospective_jobs
			WHERE status IN (?, ?)
			  AND next_run_at <= CURRENT_TIMESTAMP
			  AND (claimed_by IS NULL OR claim_expires_at IS NULL OR claim_expires_at < CURRENT_TIMESTAMP)
			ORDER BY next_run_at ASC, created_at ASC
			LIMIT 1
		`, models.RetrospectiveJobQueued, models.RetrospectiveJobRetry).Scan(&candidateID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to select retrospective job candidate: %w", err)
		}

		result, err := tx.ExecContext(context.Background(), `
			UPDATE retrospective_jobs
			SET status = ?,
			    claimed_by = ?,
			    claim_expires_at = datetime(CURRENT_TIMESTAMP, '+' || ? || ' seconds'),
			    attempt = attempt + 1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
			  AND status IN (?, ?)
			  AND next_run_at <= CURRENT_TIMESTAMP
			  AND (claimed_by IS NULL OR claim_expires_at IS NULL OR claim_expires_at < CURRENT_TIMESTAMP)
		`, models.RetrospectiveJobRunning, workerName, leaseSeconds, candidateID, models.RetrospectiveJobQueued, models.RetrospectiveJobRetry)
		if err != nil {
			return nil, fmt.Errorf("failed to claim retrospective job: %w", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("failed to check claim rows affected: %w", err)
		}
		if rowsAffected == 0 {
			continue
		}

		return getRetrospectiveJobByIDTx(tx, candidateID)
	}

	return nil, nil
}

// MarkRetrospectiveJobSucceededTx marks a claimed job as terminal success.
func MarkRetrospectiveJobSucceededTx(tx *sql.Tx, jobID string) error {
	if jobID == "" {
		return errors.New("job id is required")
	}

	_, err := tx.ExecContext(context.Background(), `
		UPDATE retrospective_jobs
		SET status = ?,
		    claimed_by = NULL,
		    claim_expires_at = NULL,
		    last_error = NULL,
		    completed_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, models.RetrospectiveJobSucceeded, jobID)
	if err != nil {
		return fmt.Errorf("failed to mark retrospective job succeeded: %w", err)
	}
	return nil
}

// MarkRetrospectiveJobRetryTx releases the claim and schedules a retry.
func MarkRetrospectiveJobRetryTx(tx *sql.Tx, jobID, errorMsg string, backoffSeconds int) error {
	if jobID == "" {
		return errors.New("job id is required")
	}
	if backoffSeconds <= 0 {
		backoffSeconds = 30
	}
	if backoffSeconds > 86400 {
		backoffSeconds = 86400
	}

	_, err := tx.ExecContext(context.Background(), `
		UPDATE retrospective_jobs
		SET status = ?,
		    claimed_by = NULL,
		    claim_expires_at = NULL,
		    next_run_at = datetime(CURRENT_TIMESTAMP, '+' || ? || ' seconds'),
		    last_error = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, models.RetrospectiveJobRetry, backoffSeconds, truncateRetrospectiveError(errorMsg), jobID)
	if err != nil {
		return fmt.Errorf("failed to mark retrospective job retry: %w", err)
	}
	return nil
}

// MarkRetrospectiveJobDeadTx releases the claim and marks job permanently failed.
func MarkRetrospectiveJobDeadTx(tx *sql.Tx, jobID, errorMsg string) error {
	if jobID == "" {
		return errors.New("job id is required")
	}

	_, err := tx.ExecContext(context.Background(), `
		UPDATE retrospective_jobs
		SET status = ?,
		    claimed_by = NULL,
		    claim_expires_at = NULL,
		    last_error = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, models.RetrospectiveJobDead, truncateRetrospectiveError(errorMsg), jobID)
	if err != nil {
		return fmt.Errorf("failed to mark retrospective job dead: %w", err)
	}
	return nil
}

func getRetrospectiveJobByAgentSessionTx(tx *sql.Tx, agentName, sessionID string) (*models.RetrospectiveJob, error) {
	row := tx.QueryRowContext(context.Background(), `
		SELECT id, agent_name, project_id, session_id, since_event_id, until_event_id, status, attempt, max_attempts,
		       next_run_at, claimed_by, claim_expires_at, last_error,
		       created_at, updated_at, completed_at
		FROM retrospective_jobs
		WHERE agent_name = ? AND session_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, agentName, sessionID)

	job, err := scanRetrospectiveJobRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("retrospective job not found for agent=%q session=%q", agentName, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query retrospective job by agent/session: %w", err)
	}
	return job, nil
}

func getRetrospectiveJobByIDTx(tx *sql.Tx, jobID string) (*models.RetrospectiveJob, error) {
	row := tx.QueryRowContext(context.Background(), `
		SELECT id, agent_name, project_id, session_id, since_event_id, until_event_id, status, attempt, max_attempts,
		       next_run_at, claimed_by, claim_expires_at, last_error,
		       created_at, updated_at, completed_at
		FROM retrospective_jobs
		WHERE id = ?
	`, jobID)

	job, err := scanRetrospectiveJobRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("retrospective job not found: %s", jobID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query retrospective job: %w", err)
	}
	return job, nil
}

func scanRetrospectiveJobRow(row *sql.Row) (*models.RetrospectiveJob, error) {
	var (
		projectID      sql.NullString
		sessionID      sql.NullString
		claimedBy      sql.NullString
		claimExpiresAt sql.NullTime
		lastError      sql.NullString
		completedAt    sql.NullTime
	)

	job := &models.RetrospectiveJob{}
	err := row.Scan(
		&job.ID,
		&job.AgentName,
		&projectID,
		&sessionID,
		&job.SinceEventID,
		&job.UntilEventID,
		&job.Status,
		&job.Attempt,
		&job.MaxAttempts,
		&job.NextRunAt,
		&claimedBy,
		&claimExpiresAt,
		&lastError,
		&job.CreatedAt,
		&job.UpdatedAt,
		&completedAt,
	)
	if err != nil {
		return nil, err
	}

	if projectID.Valid {
		job.ProjectID = projectID.String
	}
	if sessionID.Valid {
		job.SessionID = sessionID.String
	}
	if claimedBy.Valid {
		job.ClaimedBy = claimedBy.String
	}
	if claimExpiresAt.Valid {
		t := claimExpiresAt.Time
		job.ClaimExpiresAt = &t
	}
	if lastError.Valid {
		job.LastError = lastError.String
	}
	if completedAt.Valid {
		t := completedAt.Time
		job.CompletedAt = &t
	}

	return job, nil
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func truncateRetrospectiveError(s string) string {
	if len(s) <= maxRetrospectiveJobErrorLen {
		return s
	}
	return s[:maxRetrospectiveJobErrorLen]
}
