//go:build ignore
// +build ignore

// Quick verification script to check database configuration
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/dotcommander/vibe/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	db, err := store.InitDB()
	if err != nil {
		slog.Error("failed to initialize database", "error", err.Error())
		os.Exit(1)
	}
	defer db.Close()

	// Check journal mode
	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		slog.Error("failed to query journal_mode", "error", err.Error())
		os.Exit(1)
	}
	fmt.Printf("Journal mode: %s\n", journalMode)

	// Check foreign keys
	var foreignKeys int
	err = db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	if err != nil {
		slog.Error("failed to query foreign_keys", "error", err.Error())
		os.Exit(1)
	}
	fmt.Printf("Foreign keys: %d\n", foreignKeys)

	// Check busy timeout
	var busyTimeout int
	err = db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	if err != nil {
		slog.Error("failed to query busy_timeout", "error", err.Error())
		os.Exit(1)
	}
	fmt.Printf("Busy timeout: %dms\n", busyTimeout)

	// List tables
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		slog.Error("failed to query tables", "error", err.Error())
		os.Exit(1)
	}
	defer rows.Close()

	fmt.Println("\nTables:")
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			slog.Error("failed to scan table name", "error", err.Error())
			os.Exit(1)
		}
		fmt.Printf("  - %s\n", name)
	}

	fmt.Println("\nDatabase verification successful!")
}
