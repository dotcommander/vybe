package store

import (
	"database/sql"
	"fmt"
)

// Querier provides the common query/exec surface shared by *sql.DB and *sql.Tx.
type Querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Transact runs fn in a transaction wrapped with RetryWithBackoff.
func Transact(db *sql.DB, fn func(tx *sql.Tx) error) error {
	return RetryWithBackoff(func() error {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()

		if err := fn(tx); err != nil {
			return err
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}

		return nil
	})
}
