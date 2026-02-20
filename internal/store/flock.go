package store

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockFile acquires an exclusive advisory lock on a .migrate.lock file
// adjacent to the database. Blocks until the lock is available.
// Returns the lock file handle; pass to unlockFile when done.
func lockFile(dbPath string) (*os.File, error) {
	lockPath := dbPath + ".migrate.lock"
	if dir := filepath.Dir(lockPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644) //nolint:gosec // G304: lockPath derived from trusted dbPath
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock %s: %w", lockPath, err)
	}
	return f, nil
}

// unlockFile releases the advisory lock and closes the file. Nil-safe.
func unlockFile(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
