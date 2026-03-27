package actions

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/store"
)

// AutoSummarizeEventsIdempotent archives old events when active count exceeds threshold,
// keeping the most recent keepRecent events active.
// Returns (summaryEventID, archivedCount) or (0, 0) if below threshold.
//
//nolint:revive // argument-limit: all params (agent, req, project, threshold, keepRecent) required together
func AutoSummarizeEventsIdempotent(db *sql.DB, agentName, requestID, projectID string, threshold, keepRecent int) (summaryEventID int64, archivedCount int64, err error) {
	if agentName == "" {
		return 0, 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, 0, errors.New("request id is required")
	}

	// Count, window, and archive are separate reads — concurrent writes between them can
	// shift the exact boundary by a few events. This is intentional: the archive is bounded
	// by the computed ID range, idempotency prevents double-archiving, and the worst case
	// is keeping slightly more or fewer events than keepRecent. A single-tx approach would
	// add complexity for negligible benefit.
	count, err := store.CountActiveEvents(db, projectID)
	if err != nil {
		return 0, 0, fmt.Errorf("count active events: %w", err)
	}
	if count < int64(threshold) {
		return 0, 0, nil
	}

	fromID, toID, err := store.FindArchiveWindow(db, projectID, keepRecent)
	if err != nil {
		return 0, 0, fmt.Errorf("find archive window: %w", err)
	}
	if fromID == 0 && toID == 0 {
		return 0, 0, nil
	}

	summary := fmt.Sprintf("Auto-compressed events %d–%d (%d active exceeded threshold %d)", fromID, toID, count, threshold)

	var autoSumErr error
	summaryEventID, archivedCount, autoSumErr = store.ArchiveEventsRangeWithSummaryIdempotent(
		db, agentName, requestID, projectID, "", fromID, toID, summary,
	)
	if autoSumErr != nil {
		return 0, 0, fmt.Errorf("archive events: %w", autoSumErr)
	}

	return summaryEventID, archivedCount, nil
}

// AutoPruneArchivedEventsIdempotent permanently deletes archived events older
// than olderThanDays. Deletion is bounded by limit per call.
func AutoPruneArchivedEventsIdempotent(db *sql.DB, agentName, requestID, projectID string, olderThanDays, limit int) (deletedCount int64, err error) {
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}

	deleted, err := store.PruneArchivedEventsIdempotent(db, agentName, requestID, projectID, olderThanDays, limit)
	if err != nil {
		return 0, fmt.Errorf("prune archived events: %w", err)
	}

	return deleted, nil
}
