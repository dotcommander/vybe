package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ReleaseExpiredClaims clears all task claims where claim_expires_at has passed.
// Returns the number of claims released.
func ReleaseExpiredClaims(db *sql.DB) (int64, error) {
	var count int64

	err := Transact(db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(context.Background(), `
			UPDATE tasks
			SET claimed_by = NULL, claimed_at = NULL, claim_expires_at = NULL, last_heartbeat_at = NULL
			WHERE claim_expires_at IS NOT NULL AND claim_expires_at < CURRENT_TIMESTAMP
		`)
		if err != nil {
			return fmt.Errorf("failed to release expired claims: %w", err)
		}

		ra, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to count released claims: %w", err)
		}
		count = ra
		return nil
	})

	return count, err
}
