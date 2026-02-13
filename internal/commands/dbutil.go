package commands

import (
	"database/sql"
	"errors"
	"log/slog"

	"github.com/dotcommander/vibe/internal/app"
	"github.com/dotcommander/vibe/internal/store"
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

	db, err := store.InitDBWithPath(dbPath)
	if err != nil {
		return nil, nil, err
	}

	return db, func() { _ = db.Close() }, nil
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

func cmdErr(err error) error {
	if err == nil {
		return nil
	}
	attrs := []any{"error", err.Error()}
	type slogAttrError interface {
		SlogAttrs() []any
	}
	var detailed slogAttrError
	if errors.As(err, &detailed) {
		attrs = append(attrs, detailed.SlogAttrs()...)
	}
	slog.Error("command error", attrs...)
	return printedError{err: err}
}
