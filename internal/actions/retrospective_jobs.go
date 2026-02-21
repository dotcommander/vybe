package actions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// EnqueueRetrospectiveJobIdempotent creates a durable retrospective queue job.
func EnqueueRetrospectiveJobIdempotent(db *sql.DB, agentName, requestID, projectID, sessionID string, maxAttempts int) (*models.RetrospectiveJob, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	type idemResult struct {
		JobID string `json:"job_id"`
	}

	r, err := store.RunIdempotent(db, agentName, requestID, "retrospective.enqueue", func(tx *sql.Tx) (idemResult, error) {
		cursor, err := store.LoadOrCreateAgentCursorAndFocusTx(tx, agentName)
		if err != nil {
			return idemResult{}, fmt.Errorf("load agent cursor: %w", err)
		}

		effectiveProjectID := strings.TrimSpace(projectID)
		if effectiveProjectID == "" {
			effectiveProjectID = cursor.ProjectID
		}

		untilEventID, err := store.GetMaxActiveEventIDTx(tx, effectiveProjectID)
		if err != nil {
			return idemResult{}, err
		}

		job, err := store.EnqueueRetrospectiveJobTx(tx, agentName, effectiveProjectID, sessionID, cursor.Cursor, untilEventID, maxAttempts)
		if err != nil {
			return idemResult{}, err
		}

		metadata, err := json.Marshal(map[string]any{
			"job_id":         job.ID,
			"session_id":     sessionID,
			"project_id":     effectiveProjectID,
			"since_event_id": job.SinceEventID,
			"until_event_id": job.UntilEventID,
		})
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to marshal enqueue metadata: %w", err)
		}

		_, err = store.InsertEventTx(
			tx,
			models.EventKindProgress,
			agentName,
			"",
			"Retrospective job enqueued",
			string(metadata),
		)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append enqueue event: %w", err)
		}

		return idemResult{JobID: job.ID}, nil
	})
	if err != nil {
		return nil, err
	}

	var out *models.RetrospectiveJob
	if txErr := store.Transact(db, func(tx *sql.Tx) error {
		job, getErr := getRetrospectiveJobByIDTx(tx, r.JobID)
		if getErr != nil {
			return getErr
		}
		out = job
		return nil
	}); txErr != nil {
		return nil, txErr
	}

	return out, nil
}

// RunOneRetrospectiveJobResult captures one worker execution attempt.
type RunOneRetrospectiveJobResult struct {
	Processed bool                     `json:"processed"`
	Outcome   string                   `json:"outcome,omitempty"`
	Job       *models.RetrospectiveJob `json:"job,omitempty"`
}

// RunOneRetrospectiveJob claims and processes a single queued retrospective job.
// Returns Processed=false when no due job exists.
func RunOneRetrospectiveJob(db *sql.DB, workerName string, leaseSeconds int) (*RunOneRetrospectiveJobResult, error) {
	if workerName == "" {
		return nil, errors.New("worker name is required")
	}

	var claimed *models.RetrospectiveJob
	if err := store.Transact(db, func(tx *sql.Tx) error {
		job, claimErr := store.ClaimNextRetrospectiveJobTx(tx, workerName, leaseSeconds)
		if claimErr != nil {
			return claimErr
		}
		claimed = job
		return nil
	}); err != nil {
		return nil, err
	}

	if claimed == nil {
		return &RunOneRetrospectiveJobResult{Processed: false}, nil
	}

	requestIDPrefix := fmt.Sprintf("retro_job_%s_%d", claimed.ID, time.Now().UnixMilli())
	_, runErr := SessionRetrospectiveRuleOnlyWindow(
		db,
		claimed.AgentName,
		claimed.ProjectID,
		claimed.SinceEventID,
		claimed.UntilEventID,
		requestIDPrefix,
	)

	outcome := "succeeded"
	if err := store.Transact(db, func(tx *sql.Tx) error {
		if runErr == nil {
			if setErr := store.MarkRetrospectiveJobSucceededTx(tx, claimed.ID); setErr != nil {
				return setErr
			}
			return nil
		}

		if claimed.Attempt >= claimed.MaxAttempts {
			outcome = "dead"
			return store.MarkRetrospectiveJobDeadTx(tx, claimed.ID, runErr.Error())
		}

		outcome = "retry"
		return store.MarkRetrospectiveJobRetryTx(tx, claimed.ID, runErr.Error(), retryBackoffSeconds(claimed.Attempt))
	}); err != nil {
		return nil, err
	}

	var final *models.RetrospectiveJob
	if err := store.Transact(db, func(tx *sql.Tx) error {
		job, getErr := getRetrospectiveJobByIDTx(tx, claimed.ID)
		if getErr != nil {
			return getErr
		}
		final = job
		return nil
	}); err != nil {
		return nil, err
	}

	return &RunOneRetrospectiveJobResult{
		Processed: true,
		Outcome:   outcome,
		Job:       final,
	}, nil
}

func retryBackoffSeconds(attempt int) int {
	if attempt < 1 {
		attempt = 1
	}
	backoff := 30
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff >= 300 {
			return 300
		}
	}
	if backoff > 300 {
		return 300
	}
	return backoff
}

func getRetrospectiveJobByIDTx(tx *sql.Tx, jobID string) (*models.RetrospectiveJob, error) {
	row := tx.QueryRow(`
		SELECT id, agent_name, project_id, session_id, since_event_id, until_event_id, status, attempt, max_attempts,
		       next_run_at, claimed_by, claim_expires_at, last_error,
		       created_at, updated_at, completed_at
		FROM retrospective_jobs
		WHERE id = ?
	`, jobID)

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
