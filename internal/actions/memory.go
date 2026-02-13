package actions

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// MemorySet creates or updates a memory entry.
func MemorySet(db *sql.DB, agentName, key, value, valueType, scope, scopeID string, expiresAt *time.Time) (int64, error) {
	if agentName == "" {
		return 0, fmt.Errorf("agent name is required")
	}
	requestID := fmt.Sprintf("memset_%s_%s_%s_%d", scope, scopeID, key, time.Now().UnixNano())
	result, err := MemoryUpsertIdempotent(db, agentName, requestID, key, value, valueType, scope, scopeID, expiresAt, nil, nil)
	if err != nil {
		return 0, err
	}
	return result.EventID, nil
}

func MemorySetIdempotent(db *sql.DB, agentName, requestID, key, value, valueType, scope, scopeID string, expiresAt *time.Time) (int64, error) {
	if agentName == "" {
		return 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return 0, fmt.Errorf("request id is required")
	}
	result, err := MemoryUpsertIdempotent(db, agentName, requestID, key, value, valueType, scope, scopeID, expiresAt, nil, nil)
	if err != nil {
		return 0, err
	}
	return result.EventID, nil
}

// MemoryUpsertResult captures canonical upsert behavior details.
type MemoryUpsertResult struct {
	EventID      int64   `json:"event_id"`
	Reinforced   bool    `json:"reinforced"`
	Confidence   float64 `json:"confidence"`
	CanonicalKey string  `json:"canonical_key"`
}

type MemoryCompactResult struct {
	EventID       int64  `json:"event_id"`
	Compacted     int    `json:"compacted"`
	SummaryKey    string `json:"summary_key"`
	SummaryMemory string `json:"summary_memory_id,omitempty"`
}

type MemoryGCResult struct {
	EventID int64 `json:"event_id"`
	Deleted int   `json:"deleted"`
}

func MemoryUpsertIdempotent(db *sql.DB, agentName, requestID, key, value, valueType, scope, scopeID string, expiresAt *time.Time, confidence *float64, sourceEventID *int64) (*MemoryUpsertResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}
	if confidence != nil && (*confidence < 0 || *confidence > 1) {
		return nil, fmt.Errorf("confidence must be between 0 and 1")
	}

	eventID, reinforced, appliedConfidence, canonicalKey, err := store.UpsertMemoryWithEventIdempotent(
		db, agentName, requestID, key, value, valueType, scope, scopeID, expiresAt, confidence, sourceEventID,
	)
	if err != nil {
		return nil, err
	}

	return &MemoryUpsertResult{
		EventID:      eventID,
		Reinforced:   reinforced,
		Confidence:   appliedConfidence,
		CanonicalKey: canonicalKey,
	}, nil
}

func MemoryCompactIdempotent(db *sql.DB, agentName, requestID, scope, scopeID string, maxAge time.Duration, keepTop int) (*MemoryCompactResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}
	if keepTop < 0 {
		return nil, fmt.Errorf("keep-top must be >= 0")
	}

	eventID, compacted, summaryKey, summaryMemoryID, err := store.CompactMemoryWithEventIdempotent(db, agentName, requestID, scope, scopeID, maxAge, keepTop)
	if err != nil {
		return nil, err
	}

	return &MemoryCompactResult{
		EventID:       eventID,
		Compacted:     compacted,
		SummaryKey:    summaryKey,
		SummaryMemory: summaryMemoryID,
	}, nil
}

func MemoryGCIdempotent(db *sql.DB, agentName, requestID string, limit int) (*MemoryGCResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be > 0")
	}

	eventID, deleted, err := store.GCMemoryWithEventIdempotent(db, agentName, requestID, limit)
	if err != nil {
		return nil, err
	}

	return &MemoryGCResult{EventID: eventID, Deleted: deleted}, nil
}

// MemoryGet retrieves a memory entry by key, scope, and scope_id.
func MemoryGet(db *sql.DB, key, scope, scopeID string, includeSuperseded bool) (*models.Memory, error) {
	opts := store.MemoryReadOptions{IncludeSuperseded: includeSuperseded}
	mem, err := store.GetMemoryWithOptions(db, key, scope, scopeID, opts)
	if err != nil {
		return nil, err
	}

	if mem == nil {
		return nil, fmt.Errorf("memory entry not found")
	}

	return mem, nil
}

// MemoryList retrieves all memory entries for a scope and scope_id.
func MemoryList(db *sql.DB, scope, scopeID string, includeSuperseded bool) ([]*models.Memory, error) {
	opts := store.MemoryReadOptions{IncludeSuperseded: includeSuperseded}
	return store.ListMemoryWithOptions(db, scope, scopeID, opts)
}

// MemoryTouchResult captures the result of a touch operation.
type MemoryTouchResult struct {
	EventID    int64   `json:"event_id"`
	Confidence float64 `json:"confidence"`
}

// MemoryTouchIdempotent updates last_seen_at and bumps confidence, idempotent and evented.
func MemoryTouchIdempotent(db *sql.DB, agentName, requestID, key, scope, scopeID string, confidenceBump float64) (*MemoryTouchResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}

	eventID, confidence, err := store.TouchMemoryIdempotent(db, agentName, requestID, key, scope, scopeID, confidenceBump)
	if err != nil {
		return nil, err
	}

	return &MemoryTouchResult{EventID: eventID, Confidence: confidence}, nil
}

// MemoryQuery searches memory entries by key pattern, ranked by confidence and recency.
func MemoryQuery(db *sql.DB, scope, scopeID, pattern string, limit int) ([]*models.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	return store.QueryMemory(db, scope, scopeID, pattern, limit)
}

// MemoryDelete removes a memory entry by key, scope, and scope_id.
func MemoryDelete(db *sql.DB, agentName, key, scope, scopeID string) (int64, error) {
	if agentName == "" {
		return 0, fmt.Errorf("agent name is required")
	}
	eventID, err := store.DeleteMemoryWithEvent(db, agentName, key, scope, scopeID)
	if err != nil {
		return 0, err
	}
	return eventID, nil
}

func MemoryDeleteIdempotent(db *sql.DB, agentName, requestID, key, scope, scopeID string) (int64, error) {
	if agentName == "" {
		return 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return 0, fmt.Errorf("request id is required")
	}
	eventID, err := store.DeleteMemoryWithEventIdempotent(db, agentName, requestID, key, scope, scopeID)
	if err != nil {
		return 0, err
	}
	return eventID, nil
}

// Helper function to parse duration string and return expiration time
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
		return 0, fmt.Errorf("empty duration")
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
		return 0, fmt.Errorf("duration must be positive")
	}

	if suffix == "w" {
		n *= 7
	}

	return time.Duration(n) * 24 * time.Hour, nil
}

func ParseMaxAge(input string) (time.Duration, error) {
	if strings.TrimSpace(input) == "" {
		return 0, nil
	}
	return parseDurationExtended(input)
}
