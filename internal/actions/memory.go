package actions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// resolveScopeID fills an empty scope_id for task/project scopes from the
// agent's focus state. global and agent scopes are returned unchanged.
// When scope is task|project, scope_id is empty, and no focus exists, it returns
// an actionable error rather than writing under an empty scope_id (which would
// silently land in the wrong bucket). Single source of truth for focus-based
// scope inference across set/get/list/delete/pin.
func resolveScopeID(db *sql.DB, agentName, scope, scopeID string) (string, error) {
	if scopeID != "" || scope == "global" || scope == "agent" {
		return scopeID, nil
	}
	if scope != "task" && scope != "project" {
		return scopeID, nil // unknown scope: let validateScope report it
	}
	if agentName == "" {
		return "", fmt.Errorf("%s scope requires --scope-id (no agent set to infer focus from; pass --agent or --scope-id)", scope)
	}
	state, err := store.GetAgentState(db, agentName)
	if err != nil {
		return "", fmt.Errorf("resolve focus for %s scope: %w", scope, err)
	}
	if state == nil {
		return "", fmt.Errorf("%s scope requires --scope-id (agent %q has no focus state; run `vybe task begin` or pass --scope-id)", scope, agentName)
	}
	var focus string
	if scope == "task" {
		focus = state.FocusTaskID
	} else {
		focus = state.FocusProjectID
	}
	if focus == "" {
		return "", fmt.Errorf("%s scope requires --scope-id (agent %q has no focus %s; pass --scope-id)", scope, agentName, scope)
	}
	return focus, nil
}

// MemorySetIdempotent stores a memory entry idempotently.
// kind must be "" (defaults to "fact"), "fact", "directive", or "lesson". Any other value returns a structured error.
// halfLifeDays is nil to preserve any stored value, or a non-negative float to override decay rate.
// sourceTaskID is optional provenance; pass "" when not known. source_event_id is NOT auto-populated
// here — doing so would be circular (memory → the event that created it).
func MemorySetIdempotent(db *sql.DB, agentName, requestID, key, value, valueType, scope, scopeID string, expiresAt *time.Time, pinned bool, kind string, halfLifeDays *float64, sourceTaskID string) (int64, error) { //nolint:revive // argument-limit: memory params are distinct; struct degrades call-site readability
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}
	var err error
	scopeID, err = resolveScopeID(db, agentName, scope, scopeID)
	if err != nil {
		return 0, err
	}
	if kind == "" {
		kind = string(models.MemoryKindFact)
	}
	if err := ValidateMemoryKind(kind); err != nil {
		return 0, err
	}
	if halfLifeDays != nil && *halfLifeDays < 0 {
		return 0, fmt.Errorf("half_life_days must be >= 0, got %g", *halfLifeDays)
	}
	return store.UpsertMemoryWithEventIdempotent(db, agentName, requestID, key, value, valueType, scope, scopeID, expiresAt, pinned, kind, halfLifeDays, sourceTaskID)
}

// ValidateMemoryKind reports whether kind is valid. Returns a structured error whose Error()
// names the field and the accepted values so CLI output is self-describing.
func ValidateMemoryKind(kind string) error {
	if models.MemoryKind(kind).IsValid() {
		return nil
	}
	return fmt.Errorf("invalid kind: %q (must be one of: fact, directive, lesson)", kind)
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
// When scope is task|project and scopeID is empty, it infers scope_id from agentName's focus state.
func MemoryGet(db *sql.DB, agentName, key, scope, scopeID string) (*models.Memory, error) {
	var err error
	scopeID, err = resolveScopeID(db, agentName, scope, scopeID)
	if err != nil {
		return nil, err
	}
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
// When scope is task|project and scopeID is empty, it infers scope_id from agentName's focus state.
func MemoryList(db *sql.DB, agentName, scope, scopeID string) ([]*models.Memory, error) {
	var err error
	scopeID, err = resolveScopeID(db, agentName, scope, scopeID)
	if err != nil {
		return nil, err
	}
	return store.ListMemory(db, scope, scopeID)
}

// MemoryPinIdempotent sets or clears the pinned flag on an existing memory entry.
func MemoryPinIdempotent(ctx context.Context, db *sql.DB, agentName, requestID, key, scope, scopeID string, pin bool) (int64, error) {
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}
	var err error
	scopeID, err = resolveScopeID(db, agentName, scope, scopeID)
	if err != nil {
		return 0, err
	}
	return store.PinMemoryIdempotent(ctx, db, agentName, requestID, key, scope, scopeID, pin)
}

// MemoryDeleteIdempotent deletes a memory entry idempotently.
func MemoryDeleteIdempotent(ctx context.Context, db *sql.DB, agentName, requestID, key, scope, scopeID string) (int64, error) { //nolint:revive // argument-limit: all params are required and distinct
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}
	var err error
	scopeID, err = resolveScopeID(db, agentName, scope, scopeID)
	if err != nil {
		return 0, err
	}
	return store.DeleteMemoryWithEventIdempotent(ctx, db, agentName, requestID, key, scope, scopeID)
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
