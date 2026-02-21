package actions

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// MemorySetIdempotent stores a memory entry idempotently.
func MemorySetIdempotent(db *sql.DB, agentName, requestID, key, value, valueType, scope, scopeID string, expiresAt *time.Time) (int64, error) { //nolint:revive // argument-limit: memory params are distinct; struct degrades call-site readability
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}
	return store.UpsertMemoryWithEventIdempotent(db, agentName, requestID, key, value, valueType, scope, scopeID, expiresAt)
}

// MemoryGCResult holds the outcome of a memory garbage collection operation.
type MemoryGCResult struct {
	EventID int64 `json:"event_id"`
	Deleted int   `json:"deleted"`
}

// MemoryGCIdempotent runs garbage collection on expired memory entries.
func MemoryGCIdempotent(db *sql.DB, agentName, requestID string, limit int) (*MemoryGCResult, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be > 0")
	}

	eventID, deleted, err := store.GCMemoryWithEventIdempotent(db, agentName, requestID, limit)
	if err != nil {
		return nil, err
	}

	return &MemoryGCResult{EventID: eventID, Deleted: deleted}, nil
}

// MemoryGet retrieves a memory entry by key, scope, and scope_id.
func MemoryGet(db *sql.DB, key, scope, scopeID string) (*models.Memory, error) {
	mem, err := store.GetMemory(db, key, scope, scopeID)
	if err != nil {
		return nil, err
	}

	if mem == nil {
		return nil, errors.New("memory entry not found")
	}

	return mem, nil
}

// MemoryList retrieves all memory entries for a scope and scope_id.
func MemoryList(db *sql.DB, scope, scopeID string) ([]*models.Memory, error) {
	return store.ListMemory(db, scope, scopeID)
}

// MemoryDeleteIdempotent deletes a memory entry idempotently.
func MemoryDeleteIdempotent(db *sql.DB, agentName, requestID, key, scope, scopeID string) (int64, error) { //nolint:revive // argument-limit: all params are required and distinct
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}
	eventID, err := store.DeleteMemoryWithEventIdempotent(db, agentName, requestID, key, scope, scopeID)
	if err != nil {
		return 0, err
	}
	return eventID, nil
}

// ParseExpiresIn parses a duration string and returns the corresponding expiration time.
func ParseExpiresIn(duration string) (*time.Time, error) {
	if duration == "" {
		return nil, nil
	}

	d, err := parseDurationExtended(duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration format: %w", err)
	}

	expiresAt := time.Now().Add(d)
	return &expiresAt, nil
}

func parseDurationExtended(input string) (time.Duration, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return 0, errors.New("empty duration")
	}

	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Support day/week shorthands: 7d, 2w.
	last := s[len(s)-1]
	suffix := string(last)
	if suffix != "d" && suffix != "w" {
		return 0, fmt.Errorf("unsupported duration: %q", input)
	}

	n, err := strconv.ParseInt(strings.TrimSpace(s[:len(s)-1]), 10, 64)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("duration must be positive")
	}

	if suffix == "w" {
		n *= 7
	}

	return time.Duration(n) * 24 * time.Hour, nil
}
