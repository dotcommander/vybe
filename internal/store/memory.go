package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

var memoryKeyWhitespace = regexp.MustCompile(`\s+`)

const memoryCompactionSummaryKey = "memory_summary"

// compactionCandidate represents a memory row eligible for compaction.
type compactionCandidate struct {
	ID         int64
	Key        string
	Value      string
	ValueType  string
	Confidence float64
	LastSeenAt time.Time
	CreatedAt  time.Time
}

// NormalizeMemoryKey converts a key to a canonical form for matching and deduplication.
// Exported for use by batch operations in actions layer.
func NormalizeMemoryKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = memoryKeyWhitespace.ReplaceAllString(normalized, "_")

	b := strings.Builder{}
	b.Grow(len(normalized))
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}

	clean := b.String()
	if clean == "" {
		return normalized
	}

	return clean
}

// ClampConfidence ensures confidence values are in [0, 1] range.
// Exported for use by batch operations in actions layer.
func ClampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// SetMemory creates or updates a memory entry with type inference and scoping.
// Uses UPSERT on conflict to handle updates.
//
//nolint:revive // argument-limit: all memory params (key, value, type, scope, scope_id, expires) are required and distinct
func SetMemory(db *sql.DB, key, value, valueType, scope, scopeID string, expiresAt *time.Time) error {
	// Validate explicit value type
	if err := validateValueType(valueType); err != nil {
		return err
	}

	// Infer value type if not specified
	if valueType == "" {
		valueType = inferValueType(value)
	}

	// Validate scope
	if err := validateScope(scope, scopeID); err != nil {
		return err
	}

	canonicalKey := NormalizeMemoryKey(key)

	return RetryWithBackoff(func() error {
		query := `
			INSERT INTO memory (key, canonical_key, value, value_type, scope, scope_id, expires_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(scope, scope_id, key) DO UPDATE SET
				canonical_key = excluded.canonical_key,
				value = excluded.value,
				value_type = excluded.value_type,
				expires_at = excluded.expires_at,
				last_seen_at = CURRENT_TIMESTAMP
		`
		_, err := db.ExecContext(context.Background(), query, key, canonicalKey, value, valueType, scope, scopeID, expiresAt)
		if err != nil {
			return fmt.Errorf("failed to set memory: %w", err)
		}
		return nil
	})
}

// UpsertMemoryWithEventIdempotent performs canonical-key memory upsert once per (agent_name, request_id).
// If a canonical-key match already exists in the same scope, it is updated/reinforced.
//
//nolint:gocognit,gocyclo,funlen,nestif,revive // upsert+reinforce logic requires shared event-log path across insert and update branches; splitting obscures the race-condition recovery sequence
func UpsertMemoryWithEventIdempotent(db *sql.DB, agentName, requestID, key, value, valueType, scope, scopeID string, expiresAt *time.Time, confidence *float64, sourceEventID *int64) (int64, bool, float64, string, error) {
	if err := validateValueType(valueType); err != nil {
		return 0, false, 0, "", err
	}

	if valueType == "" {
		valueType = inferValueType(value)
	}

	if err := validateScope(scope, scopeID); err != nil {
		return 0, false, 0, "", err
	}

	canonicalKey := NormalizeMemoryKey(key)
	if canonicalKey == "" {
		return 0, false, 0, "", errors.New("key cannot normalize to empty canonical key")
	}

	taskID := ""
	if scope == string(models.MemoryScopeTask) {
		taskID = scopeID
	}

	type idemResult struct {
		EventID      int64   `json:"event_id"`
		Reinforced   bool    `json:"reinforced"`
		Confidence   float64 `json:"confidence"`
		CanonicalKey string  `json:"canonical_key"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "memory.upsert", func(tx *sql.Tx) (idemResult, error) {
		var (
			existingID         int64
			existingValue      string
			existingValueType  string
			existingConfidence float64
		)

		err := tx.QueryRowContext(context.Background(), `
			SELECT id, value, value_type, confidence
			FROM memory
			WHERE scope = ? AND scope_id = ? AND canonical_key = ?
			  AND superseded_by IS NULL
			ORDER BY created_at DESC
			LIMIT 1
		`, scope, scopeID, canonicalKey).Scan(&existingID, &existingValue, &existingValueType, &existingConfidence)

		reinforced := false
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return idemResult{}, fmt.Errorf("failed to lookup canonical memory: %w", err)
		}

		newConfidence := 0.5
		if confidence != nil {
			newConfidence = ClampConfidence(*confidence)
		}

		inserted := false
		if errors.Is(err, sql.ErrNoRows) {
			_, execErr := tx.ExecContext(context.Background(), `
				INSERT INTO memory (
					key, canonical_key, value, value_type, scope, scope_id,
					expires_at, confidence, last_seen_at, source_event_id
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)
			`, key, canonicalKey, value, valueType, scope, scopeID, expiresAt, newConfidence, sourceEventID)
			if execErr != nil {
				// Race condition: another agent inserted the same canonical_key
				// Retry the lookup+update path
				if IsUniqueConstraintErr(execErr) {
					retryErr := tx.QueryRowContext(context.Background(), `
						SELECT id, value, value_type, confidence
						FROM memory
						WHERE scope = ? AND scope_id = ? AND canonical_key = ?
						  AND superseded_by IS NULL
						ORDER BY created_at DESC
						LIMIT 1
					`, scope, scopeID, canonicalKey).Scan(&existingID, &existingValue, &existingValueType, &existingConfidence)
					if retryErr != nil {
						if errors.Is(retryErr, sql.ErrNoRows) {
							// Row was deleted between our INSERT attempt and retry lookup (concurrent GC).
							// Re-attempt the INSERT since the canonical_key slot is now free.
							_, reinsertErr := tx.ExecContext(context.Background(), `
								INSERT INTO memory (
									key, canonical_key, value, value_type, scope, scope_id,
									expires_at, confidence, last_seen_at, source_event_id
								)
								VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)
							`, key, canonicalKey, value, valueType, scope, scopeID, expiresAt, newConfidence, sourceEventID)
							if reinsertErr != nil {
								return idemResult{}, fmt.Errorf("failed to re-insert memory after GC race: %w", reinsertErr)
							}
							inserted = true
						} else {
							return idemResult{}, fmt.Errorf("failed to lookup canonical memory after race: %w", retryErr)
						}
					}
					// Fall through to update logic below (only when inserted is false)
					reinforced = existingValue == value && existingValueType == valueType
					if confidence == nil {
						if reinforced {
							newConfidence = ClampConfidence(existingConfidence + 0.05)
						} else {
							newConfidence = existingConfidence
						}
					}
				} else {
					return idemResult{}, fmt.Errorf("failed to insert memory: %w", execErr)
				}
			} else {
				inserted = true
			}
		} else {
			reinforced = existingValue == value && existingValueType == valueType
			if confidence == nil {
				if reinforced {
					newConfidence = ClampConfidence(existingConfidence + 0.05)
				} else {
					newConfidence = existingConfidence
				}
			}
		}

		// Update path (shared by initial lookup and race-condition retry)
		if !inserted {
			if sourceEventID != nil {
				_, err = tx.ExecContext(context.Background(), `
					UPDATE memory
					SET key = ?,
					    canonical_key = ?,
					    value = ?,
					    value_type = ?,
					    expires_at = ?,
					    confidence = ?,
					    last_seen_at = CURRENT_TIMESTAMP,
					    source_event_id = ?
					WHERE id = ?
				`, key, canonicalKey, value, valueType, expiresAt, newConfidence, *sourceEventID, existingID)
			} else {
				_, err = tx.ExecContext(context.Background(), `
					UPDATE memory
					SET key = ?,
					    canonical_key = ?,
					    value = ?,
					    value_type = ?,
					    expires_at = ?,
					    confidence = ?,
					    last_seen_at = CURRENT_TIMESTAMP
					WHERE id = ?
				`, key, canonicalKey, value, valueType, expiresAt, newConfidence, existingID)
			}
			if err != nil {
				return idemResult{}, fmt.Errorf("failed to update memory: %w", err)
			}
		}

		eventKind := models.EventKindMemoryUpserted
		if reinforced {
			eventKind = models.EventKindMemoryReinforced
		}

		meta := struct {
			Key          string     `json:"key"`
			CanonicalKey string     `json:"canonical_key"`
			ValueType    string     `json:"value_type"`
			Scope        string     `json:"scope"`
			ScopeID      string     `json:"scope_id,omitempty"`
			ExpiresAt    *time.Time `json:"expires_at,omitempty"`
			Reinforced   bool       `json:"reinforced"`
			Confidence   float64    `json:"confidence"`
		}{
			Key:          key,
			CanonicalKey: canonicalKey,
			ValueType:    valueType,
			Scope:        scope,
			ScopeID:      scopeID,
			ExpiresAt:    expiresAt,
			Reinforced:   reinforced,
			Confidence:   newConfidence,
		}
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to marshal memory event metadata: %w", err)
		}

		eventID, err := InsertEventTx(tx, eventKind, agentName, taskID, fmt.Sprintf("Memory upserted: %s", key), string(metaBytes))
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{EventID: eventID, Reinforced: reinforced, Confidence: newConfidence, CanonicalKey: canonicalKey}, nil
	})
	if err != nil {
		return 0, false, 0, "", err
	}

	return r.EventID, r.Reinforced, r.Confidence, r.CanonicalKey, nil
}

// scanCompactionCandidates queries and scans memory rows eligible for compaction.
// Caller must ensure rows cursor is closed before further tx queries.
func scanCompactionCandidates(tx *sql.Tx, scope, scopeID string) ([]compactionCandidate, error) {
	rows, err := tx.QueryContext(context.Background(), `
		SELECT id, key, value, value_type, confidence,
		       CAST(strftime('%s', COALESCE(last_seen_at, created_at)) AS INTEGER),
		       created_at
		FROM memory
		WHERE scope = ? AND scope_id = ?
		  AND superseded_by IS NULL
		  AND key != ?
		  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
	`, scope, scopeID, memoryCompactionSummaryKey)
	if err != nil {
		return nil, fmt.Errorf("failed to query memory for compaction: %w", err)
	}

	defer func() { _ = rows.Close() }()

	candidates := make([]compactionCandidate, 0)
	for rows.Next() {
		var row compactionCandidate
		var lastSeenUnix sql.NullInt64
		scanErr := rows.Scan(&row.ID, &row.Key, &row.Value, &row.ValueType, &row.Confidence, &lastSeenUnix, &row.CreatedAt)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan compaction candidate: %w", scanErr)
		}
		if lastSeenUnix.Valid {
			row.LastSeenAt = time.Unix(lastSeenUnix.Int64, 0).UTC()
		} else {
			row.LastSeenAt = row.CreatedAt
		}
		candidates = append(candidates, row)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("failed iterating compaction candidates: %w", rowsErr)
	}

	return candidates, nil
}

// selectCompactionVictims sorts candidates by confidence/recency and returns
// those beyond keepTop that are older than maxAge.
func selectCompactionVictims(candidates []compactionCandidate, keepTop int, maxAge time.Duration) []compactionCandidate {
	// Sort by confidence desc, then lastSeen desc, then created desc
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Confidence != candidates[j].Confidence {
			return candidates[i].Confidence > candidates[j].Confidence
		}
		if !candidates[i].LastSeenAt.Equal(candidates[j].LastSeenAt) {
			return candidates[i].LastSeenAt.After(candidates[j].LastSeenAt)
		}
		return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
	})

	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().Add(-maxAge)
	}

	victims := make([]compactionCandidate, 0)
	for i := keepTop; i < len(candidates); i++ {
		if !cutoff.IsZero() && candidates[i].LastSeenAt.After(cutoff) {
			continue
		}
		victims = append(victims, candidates[i])
	}

	return victims
}

// buildCompactionSummary creates a JSON summary payload from compacted entries.
func buildCompactionSummary(victims []compactionCandidate) []byte {
	summaryEntries := make([]map[string]any, 0, len(victims))
	for _, v := range victims {
		summaryEntries = append(summaryEntries, map[string]any{
			"key":        v.Key,
			"value":      v.Value,
			"value_type": v.ValueType,
		})
	}

	summaryPayload := map[string]any{
		"compacted_count": len(victims),
		"generated_at":    time.Now().UTC().Format(time.RFC3339),
		"entries":         summaryEntries,
	}
	summaryBytes, err := json.Marshal(summaryPayload)
	if err != nil {
		// Fallback: return empty JSON object on marshal failure
		return []byte("{}")
	}
	return summaryBytes
}

// CompactMemoryWithEventIdempotent compacts memory entries for a scope, writing a summary and superseding old entries.
//
//nolint:gocognit,gocyclo,funlen,revive // compaction selects victims, writes summary, supersedes entries, and logs an event; phases are tightly coupled inside the idempotent tx
func CompactMemoryWithEventIdempotent(db *sql.DB, agentName, requestID, scope, scopeID string, maxAge time.Duration, keepTop int) (int64, int, string, string, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, 0, "", "", err
	}
	if keepTop < 0 {
		return 0, 0, "", "", errors.New("keepTop must be >= 0")
	}

	type idemResult struct {
		EventID         int64  `json:"event_id"`
		Compacted       int    `json:"compacted"`
		SummaryKey      string `json:"summary_key"`
		SummaryMemoryID string `json:"summary_memory_id,omitempty"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "memory.compact", func(tx *sql.Tx) (idemResult, error) {
		// Scan candidates
		candidates, err := scanCompactionCandidates(tx, scope, scopeID)
		if err != nil {
			return idemResult{}, err
		}

		// Select victims
		victims := selectCompactionVictims(candidates, keepTop, maxAge)

		// Early return if no victims
		summaryMemoryID := ""
		if len(victims) == 0 {
			meta := map[string]any{
				"scope":       scope,
				"scope_id":    scopeID,
				"compacted":   0,
				"keep_top":    keepTop,
				"summary_key": memoryCompactionSummaryKey,
			}
			if maxAge > 0 {
				meta["max_age_seconds"] = int(maxAge.Seconds())
			}
			var emptyMarshalErr error
			metaBytes, emptyMarshalErr := json.Marshal(meta)
			if emptyMarshalErr != nil {
				return idemResult{}, fmt.Errorf("failed to marshal compaction event metadata: %w", emptyMarshalErr)
			}

			eventID, emptyInsertErr := InsertEventTx(tx, models.EventKindMemoryCompacted, agentName, "", fmt.Sprintf("Memory compacted for %s/%s", scope, scopeID), string(metaBytes))
			if emptyInsertErr != nil {
				return idemResult{}, fmt.Errorf("failed to append memory_compacted event: %w", emptyInsertErr)
			}

			return idemResult{EventID: eventID, Compacted: 0, SummaryKey: memoryCompactionSummaryKey, SummaryMemoryID: ""}, nil
		}

		// Build summary
		summaryBytes := buildCompactionSummary(victims)
		summaryCanonical := NormalizeMemoryKey(memoryCompactionSummaryKey)

		// Insert summary row
		_, execErr := tx.ExecContext(context.Background(), `
			INSERT INTO memory (key, canonical_key, value, value_type, scope, scope_id, confidence, last_seen_at)
			VALUES (?, ?, ?, 'json', ?, ?, 0.8, CURRENT_TIMESTAMP)
			ON CONFLICT(scope, scope_id, key) DO UPDATE SET
				canonical_key = excluded.canonical_key,
				value = excluded.value,
				value_type = excluded.value_type,
				confidence = excluded.confidence,
				last_seen_at = CURRENT_TIMESTAMP
		`, memoryCompactionSummaryKey, summaryCanonical, string(summaryBytes), scope, scopeID)
		if execErr != nil {
			return idemResult{}, fmt.Errorf("failed to write memory summary: %w", execErr)
		}

		// Fetch summary ID
		var summaryID int64
		err = tx.QueryRowContext(context.Background(), `
			SELECT id FROM memory
			WHERE scope = ? AND scope_id = ? AND key = ?
		`, scope, scopeID, memoryCompactionSummaryKey).Scan(&summaryID)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to fetch summary memory id: %w", err)
		}
		summaryMemoryID = fmt.Sprintf("memory_%d", summaryID)

		// Supersede victims
		for _, v := range victims {
			_, updateErr := tx.ExecContext(context.Background(), `
				UPDATE memory
				SET superseded_by = ?
				WHERE id = ?
			`, summaryMemoryID, v.ID)
			if updateErr != nil {
				return idemResult{}, fmt.Errorf("failed to mark memory superseded: %w", updateErr)
			}
		}

		// Insert event
		meta := map[string]any{
			"scope":             scope,
			"scope_id":          scopeID,
			"compacted":         len(victims),
			"keep_top":          keepTop,
			"summary_key":       memoryCompactionSummaryKey,
			"summary_memory_id": summaryMemoryID,
		}
		if maxAge > 0 {
			meta["max_age_seconds"] = int(maxAge.Seconds())
		}
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to marshal compaction event metadata: %w", err)
		}

		eventID, err := InsertEventTx(tx, models.EventKindMemoryCompacted, agentName, "", fmt.Sprintf("Memory compacted for %s/%s", scope, scopeID), string(metaBytes))
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append memory_compacted event: %w", err)
		}

		return idemResult{EventID: eventID, Compacted: len(victims), SummaryKey: memoryCompactionSummaryKey, SummaryMemoryID: summaryMemoryID}, nil
	})
	if err != nil {
		return 0, 0, "", "", err
	}

	return r.EventID, r.Compacted, r.SummaryKey, r.SummaryMemoryID, nil
}

// GCMemoryWithEventIdempotent removes expired and superseded memory entries, emitting a gc event.
// Idempotent per (agentName, requestID).
//
//nolint:revive // cognitive-complexity: GC requires multiple classification passes on memory state
func GCMemoryWithEventIdempotent(db *sql.DB, agentName, requestID string, limit int) (int64, int, error) {
	if limit <= 0 {
		limit = 100
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
		Deleted int   `json:"deleted"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "memory.gc", func(tx *sql.Tx) (idemResult, error) {
		rows, err := tx.QueryContext(context.Background(), `
			SELECT id
			FROM memory
			WHERE (expires_at IS NOT NULL AND expires_at <= CURRENT_TIMESTAMP)
			   OR superseded_by IS NOT NULL
			ORDER BY id ASC
			LIMIT ?
		`, limit)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to query memory gc candidates: %w", err)
		}

		defer func() { _ = rows.Close() }()

		ids := make([]int64, 0)
		for rows.Next() {
			var id int64
			scanErr := rows.Scan(&id)
			if scanErr != nil {
				return idemResult{}, fmt.Errorf("failed to scan memory gc candidate: %w", scanErr)
			}
			ids = append(ids, id)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			return idemResult{}, fmt.Errorf("failed iterating memory gc candidates: %w", rowsErr)
		}

		if len(ids) > 0 {
			placeholders := strings.Repeat("?,", len(ids))
			placeholders = placeholders[:len(placeholders)-1]
			args := make([]any, len(ids))
			for i, id := range ids {
				args[i] = id
			}
			_, deleteErr := tx.ExecContext(context.Background(),
				`DELETE FROM memory WHERE id IN (`+placeholders+`)`, args...)
			if deleteErr != nil {
				return idemResult{}, fmt.Errorf("failed batch deleting %d memory rows: %w", len(ids), deleteErr)
			}
		}

		meta := map[string]any{"deleted": len(ids), "limit": limit}
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to marshal memory gc event metadata: %w", err)
		}
		eventID, err := InsertEventTx(tx, models.EventKindMemoryGC, agentName, "", fmt.Sprintf("Memory GC deleted %d rows", len(ids)), string(metaBytes))
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append memory_gc event: %w", err)
		}

		return idemResult{EventID: eventID, Deleted: len(ids)}, nil
	})
	if err != nil {
		return 0, 0, err
	}

	return r.EventID, r.Deleted, nil
}

// MemoryReadOptions controls filtering for memory read operations.
type MemoryReadOptions struct {
	IncludeSuperseded bool
}

// GetMemory retrieves a memory entry by key, scope, and scope_id.
// Returns nil if not found or expired. Excludes superseded entries by default.
func GetMemory(db *sql.DB, key, scope, scopeID string) (*models.Memory, error) {
	return GetMemoryWithOptions(db, key, scope, scopeID, MemoryReadOptions{})
}

// GetMemoryWithOptions retrieves a memory entry with configurable filtering.
func GetMemoryWithOptions(db *sql.DB, key, scope, scopeID string, opts MemoryReadOptions) (*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}

	var mem models.Memory
	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, key, canonical_key, value, value_type, scope, scope_id,
			       confidence, last_seen_at, source_event_id, superseded_by, expires_at, created_at
			FROM memory
			WHERE key = ? AND scope = ? AND scope_id = ?
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		`
		if !opts.IncludeSuperseded {
			query += ` AND superseded_by IS NULL`
		}
		var (
			lastSeenAt    sql.NullTime
			sourceEventID sql.NullInt64
			supersededBy  sql.NullString
		)
		err := db.QueryRowContext(context.Background(), query, key, scope, scopeID).Scan(
			&mem.ID,
			&mem.Key,
			&mem.Canonical,
			&mem.Value,
			&mem.ValueType,
			&mem.Scope,
			&mem.ScopeID,
			&mem.Confidence,
			&lastSeenAt,
			&sourceEventID,
			&supersededBy,
			&mem.ExpiresAt,
			&mem.CreatedAt,
		)
		if err != nil {
			return err
		}
		if lastSeenAt.Valid {
			mem.LastSeenAt = &lastSeenAt.Time
		}
		if sourceEventID.Valid {
			id := sourceEventID.Int64
			mem.SourceEventID = &id
		}
		if supersededBy.Valid {
			mem.SupersededBy = supersededBy.String
		}
		return nil
	})

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	return &mem, nil
}

// ListMemoryWithOptions retrieves memory entries with configurable filtering.
//nolint:revive // cognitive-complexity: query building requires many filter combinations
func ListMemoryWithOptions(db *sql.DB, scope, scopeID string, opts MemoryReadOptions) ([]*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}

	var memories []*models.Memory
	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, key, canonical_key, value, value_type, scope, scope_id,
			       confidence, last_seen_at, source_event_id, superseded_by, expires_at, created_at
			FROM memory
			WHERE scope = ? AND scope_id = ?
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		`
		if !opts.IncludeSuperseded {
			query += ` AND superseded_by IS NULL`
		}
		query += ` ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC`
		rows, err := db.QueryContext(context.Background(), query, scope, scopeID)
		if err != nil {
			return fmt.Errorf("failed to list memory: %w", err)
		}
		defer func() { _ = rows.Close() }()

		memories, err = scanMemoryRows(rows)
		return err
	})

	if err != nil {
		return nil, err
	}

	return memories, nil
}

// scanMemoryRows reads all rows from a memory query result into a slice.
func scanMemoryRows(rows *sql.Rows) ([]*models.Memory, error) {
	memories := make([]*models.Memory, 0)
	for rows.Next() {
		var mem models.Memory
		var (
			lastSeenAt    sql.NullTime
			sourceEventID sql.NullInt64
			supersededBy  sql.NullString
		)
		if err := rows.Scan(
			&mem.ID,
			&mem.Key,
			&mem.Canonical,
			&mem.Value,
			&mem.ValueType,
			&mem.Scope,
			&mem.ScopeID,
			&mem.Confidence,
			&lastSeenAt,
			&sourceEventID,
			&supersededBy,
			&mem.ExpiresAt,
			&mem.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		if lastSeenAt.Valid {
			mem.LastSeenAt = &lastSeenAt.Time
		}
		if sourceEventID.Valid {
			id := sourceEventID.Int64
			mem.SourceEventID = &id
		}
		if supersededBy.Valid {
			mem.SupersededBy = supersededBy.String
		}
		memories = append(memories, &mem)
	}
	return memories, rows.Err()
}

// TouchMemoryIdempotent updates last_seen_at and bumps confidence for an active memory entry.
// Idempotent and event-logged per (agentName, requestID).
//
//nolint:revive // argument-limit: all params are required and semantically distinct
func TouchMemoryIdempotent(db *sql.DB, agentName, requestID, key, scope, scopeID string, confidenceBump float64) (int64, float64, error) {
	if confidenceBump < 0 || confidenceBump > 1 {
		return 0, 0, fmt.Errorf("confidence bump must be between 0 and 1, got %v", confidenceBump)
	}
	if err := validateScope(scope, scopeID); err != nil {
		return 0, 0, err
	}

	taskID := ""
	if scope == string(models.MemoryScopeTask) {
		taskID = scopeID
	}

	type idemResult struct {
		EventID    int64   `json:"event_id"`
		Confidence float64 `json:"confidence"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "memory.touch", func(tx *sql.Tx) (idemResult, error) {
		var currentConfidence float64
		err := tx.QueryRowContext(context.Background(), `
			SELECT confidence FROM memory
			WHERE key = ? AND scope = ? AND scope_id = ?
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
			AND superseded_by IS NULL
		`, key, scope, scopeID).Scan(&currentConfidence)
		if errors.Is(err, sql.ErrNoRows) {
			return idemResult{}, errors.New("memory entry not found")
		}
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to read memory for touch: %w", err)
		}

		newConfidence := ClampConfidence(currentConfidence + confidenceBump)
		_, err = tx.ExecContext(context.Background(), `
			UPDATE memory
			SET last_seen_at = CURRENT_TIMESTAMP, confidence = ?
			WHERE key = ? AND scope = ? AND scope_id = ?
			AND superseded_by IS NULL
		`, newConfidence, key, scope, scopeID)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to touch memory: %w", err)
		}

		meta := struct {
			Key            string  `json:"key"`
			Scope          string  `json:"scope"`
			ScopeID        string  `json:"scope_id,omitempty"`
			ConfidenceBump float64 `json:"confidence_bump"`
			NewConfidence  float64 `json:"new_confidence"`
		}{
			Key:            key,
			Scope:          scope,
			ScopeID:        scopeID,
			ConfidenceBump: confidenceBump,
			NewConfidence:  newConfidence,
		}
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to marshal touch event metadata: %w", err)
		}

		eventID, err := InsertEventTx(tx, models.EventKindMemoryTouched, agentName, taskID, fmt.Sprintf("Memory touched: %s", key), string(metaBytes))
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append memory_touched event: %w", err)
		}

		return idemResult{EventID: eventID, Confidence: newConfidence}, nil
	})
	if err != nil {
		return 0, 0, err
	}

	return r.EventID, r.Confidence, nil
}

// QueryMemory searches memory entries by key LIKE pattern, scoped and ranked.
// Returns only active (non-superseded, non-expired) entries.
//
// Performance note: LIKE uses SQLite's default full-scan matching.
// For acceptable performance at scale (50k+ memory rows), callers should
// use prefix-anchored patterns (e.g., "config_%") rather than infix
// wildcards (e.g., "%config%"). Prefix-anchored LIKE can leverage the
// existing index on (scope, scope_id, canonical_key).
func QueryMemory(db *sql.DB, scope, scopeID, pattern string, limit int) ([]*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}

	var memories []*models.Memory
	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, key, canonical_key, value, value_type, scope, scope_id,
			       confidence, last_seen_at, source_event_id, superseded_by, expires_at, created_at
			FROM memory
			WHERE scope = ? AND scope_id = ?
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
			AND superseded_by IS NULL
			AND (key LIKE ? OR canonical_key LIKE ?)
			ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC
			LIMIT ?
		`
		rows, err := db.QueryContext(context.Background(), query, scope, scopeID, pattern, pattern, limit)
		if err != nil {
			return fmt.Errorf("failed to query memory: %w", err)
		}
		defer func() { _ = rows.Close() }()

		memories, err = scanMemoryRows(rows)
		return err
	})

	if err != nil {
		return nil, err
	}

	return memories, nil
}

// DeleteMemoryWithEvent deletes memory and appends an event in the same transaction.
// Returns the event ID.
func DeleteMemoryWithEvent(db *sql.DB, agentName, key, scope, scopeID string) (int64, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}

	taskID := ""
	if scope == "task" {
		taskID = scopeID
	}

	meta := struct {
		Key     string `json:"key"`
		Scope   string `json:"scope"`
		ScopeID string `json:"scope_id,omitempty"`
	}{
		Key:     key,
		Scope:   scope,
		ScopeID: scopeID,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal memory delete event metadata: %w", err)
	}

	var eventID int64
	err = Transact(db, func(tx *sql.Tx) error {
		result, txErr := tx.ExecContext(context.Background(), `DELETE FROM memory WHERE key = ? AND scope = ? AND scope_id = ?`, key, scope, scopeID)
		if txErr != nil {
			return fmt.Errorf("failed to delete memory: %w", txErr)
		}

		rowsAffected, txErr := result.RowsAffected()
		if txErr != nil {
			return fmt.Errorf("failed to get rows affected: %w", txErr)
		}
		if rowsAffected == 0 {
			return errors.New("memory entry not found")
		}

		id, txErr := InsertEventTx(tx, models.EventKindMemoryDelete, agentName, taskID, fmt.Sprintf("Memory deleted: %s", key), string(metaBytes))
		if txErr != nil {
			return fmt.Errorf("failed to append event: %w", txErr)
		}
		eventID = id

		return nil
	})
	if err != nil {
		return 0, err
	}
	return eventID, nil
}

// DeleteMemoryWithEventIdempotent performs DeleteMemoryWithEvent once per (agent_name, request_id).
// On retries with the same request id, returns the originally created event id.
//
//nolint:revive // argument-limit: all params (agent, req, key, scope, scope_id) are required
func DeleteMemoryWithEventIdempotent(db *sql.DB, agentName, requestID, key, scope, scopeID string) (int64, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}

	taskID := ""
	if scope == "task" {
		taskID = scopeID
	}

	meta := struct {
		Key     string `json:"key"`
		Scope   string `json:"scope"`
		ScopeID string `json:"scope_id,omitempty"`
	}{
		Key:     key,
		Scope:   scope,
		ScopeID: scopeID,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal memory delete event metadata: %w", err)
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(db, agentName, requestID, "memory.delete", func(tx *sql.Tx) (idemResult, error) {
		result, txErr := tx.ExecContext(context.Background(), `DELETE FROM memory WHERE key = ? AND scope = ? AND scope_id = ?`, key, scope, scopeID)
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to delete memory: %w", txErr)
		}

		rowsAffected, txErr := result.RowsAffected()
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to get rows affected: %w", txErr)
		}
		if rowsAffected == 0 {
			return idemResult{}, errors.New("memory entry not found")
		}

		eventID, txErr := InsertEventTx(tx, models.EventKindMemoryDelete, agentName, taskID, fmt.Sprintf("Memory deleted: %s", key), string(metaBytes))
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", txErr)
		}

		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}

// inferValueType attempts to detect the value type from the input string.
func inferValueType(value string) string {
	value = strings.TrimSpace(value)

	// Check for boolean
	if value == "true" || value == "false" {
		return "boolean"
	}

	// Check for number
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return "number"
	}

	// Check for JSON object or array
	if (strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")) ||
		(strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]")) {
		// Try to parse as JSON
		var js any
		if err := json.Unmarshal([]byte(value), &js); err == nil {
			switch js.(type) {
			case []any:
				return "array"
			case map[string]any:
				return "json"
			}
		}
	}

	// Default to string
	return "string"
}

// isValidValueType reports whether vt is an allowed memory value type.
func isValidValueType(vt string) bool {
	switch vt {
	case "string", "number", "boolean", "json", "array":
		return true
	}
	return false
}

// validateValueType checks that an explicit value type is in the allowed set.
// Empty string is allowed (triggers inference).
func validateValueType(valueType string) error {
	if valueType == "" {
		return nil
	}
	if !isValidValueType(valueType) {
		return fmt.Errorf("invalid value_type: %q (must be one of: string, number, boolean, json, array)", valueType)
	}
	return nil
}

// validateScope ensures scope and scope_id are valid.
func validateScope(scope, scopeID string) error {
	switch scope {
	case "global", "project", "task", "agent":
		// valid
	default:
		return fmt.Errorf("invalid scope: %s (must be one of: global, project, task, agent)", scope)
	}

	// Global scope should not have a scope_id
	if scope == "global" && scopeID != "" {
		return errors.New("global scope cannot have a scope_id")
	}

	// Non-global scopes require a scope_id
	if scope != "global" && scopeID == "" {
		return fmt.Errorf("%s scope requires a scope_id", scope)
	}

	return nil
}
