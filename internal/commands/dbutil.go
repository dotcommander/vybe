package commands

import (
	"database/sql"
	"errors"
	"log/slog"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
)

// DB is an alias so command code doesn't need to import database/sql.
type DB = sql.DB

type printedError struct {
	err error
}

func (e printedError) Error() string {
	// Intentionally hide the original error: the JSON error response is the output.
	return "error already printed"
}

func openDB() (*DB, func(), error) {
	dbPath, err := app.GetDBPath()
	if err != nil {
		return nil, nil, err
	}

	db, err := store.OpenDB(dbPath)
	if err != nil {
		return nil, nil, err
	}

	if err := store.MigrateDB(db, dbPath); err != nil {
		_ = store.CloseDB(db)
		return nil, nil, err
	}

	return db, func() { _ = store.CloseDB(db) }, nil
}

func withDB(fn func(db *DB) error) error {
	db, closeDB, err := openDB()
	if err != nil {
		return cmdErr(err)
	}
	defer closeDB()

	if err := fn(db); err != nil {
		return cmdErr(err)
	}
	return nil
}

// withDBSilent opens the database and runs fn, logging errors to slog only.
// Used in hook handlers where stdout must never be corrupted with error JSON.
func withDBSilent(fn func(db *DB) error) {
	db, closeDB, err := openDB()
	if err != nil {
		slog.Default().Warn("hook db open failed", "error", err)
		return
	}
	defer closeDB()
	if err := fn(db); err != nil {
		slog.Default().Warn("hook db operation failed", "error", err)
	}
}

func cmdErr(err error) error {
	if err == nil {
		return nil
	}
	// Emit structured JSON error to stdout for agent consumption
	_ = output.PrintError(err)
	attrs := []any{"error", err.Error()}
	type slogAttrError interface {
		SlogAttrs() []any
	}
	var detailed slogAttrError
	if errors.As(err, &detailed) {
		attrs = append(attrs, detailed.SlogAttrs()...)
	}
	slog.Default().Error("command error", attrs...)
	return printedError{err: err}
}
