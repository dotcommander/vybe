package store

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// LockFile acquires an exclusive advisory flock on the given lock path.
// Blocks until the lock is available. Returns the lock file handle;
// pass to UnlockFile when done.
func LockFile(lockPath string) (*os.File, error) {
	if dir := filepath.Dir(lockPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644) //nolint:gosec // G304: lockPath derived from trusted caller path
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock %s: %w", lockPath, err)
	}
	return f, nil
}

// UnlockFile releases the advisory lock and closes the file. Nil-safe.
func UnlockFile(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
