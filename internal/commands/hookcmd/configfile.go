package hookcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dotcommander/vybe/internal/store"
)

// errSkipWrite is returned by a transform function passed to withLockedSettings
// to signal that no mutation occurred and the file should not be rewritten.
var errSkipWrite = errors.New("skip write")

// atomicWriteFile writes data to a temp file in the same directory, then
// renames to the target path. Rename is atomic on POSIX (same-filesystem
// guarantee since temp is in same dir).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// Clean up temp file on any failure path.
	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	success = true
	return nil
}

// withLockedSettings performs an atomic read-modify-write cycle:
//  1. Acquire file lock on {path}.lock
//  2. Read current settings (or empty map if file doesn't exist)
//  3. Call transform function which mutates settings map in place
//  4. Atomic write (temp file + rename)
//  5. Release lock
//
// If transform returns errSkipWrite, no write occurs and nil is returned.
// If transform returns any other error, no write occurs and the error propagates.
func withLockedSettings(path string, fn func(settings map[string]any) error) error {
	lock, err := store.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer store.UnlockFile(lock)

	settings, err := readSettings(path)
	if err != nil {
		return err
	}

	if err := fn(settings); err != nil {
		if errors.Is(err, errSkipWrite) {
			return nil
		}
		return err
	}

	data, marshalErr := json.MarshalIndent(settings, "", "  ")
	if marshalErr != nil {
		return fmt.Errorf("marshal settings: %w", marshalErr)
	}
	data = append(data, '\n')

	return atomicWriteFile(path, data, 0o600)
}
