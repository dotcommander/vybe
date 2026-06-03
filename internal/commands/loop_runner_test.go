package commands

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWaitForProcessExit(t *testing.T) {
	t.Parallel()

	t.Run("returns true when done fires before timeout", func(t *testing.T) {
		t.Parallel()
		done := make(chan error, 1)
		done <- nil // pre-signal: process already exited

		start := time.Now()
		got := waitForProcessExit(done)
		elapsed := time.Since(start)

		assert.True(t, got, "expected true when done channel is ready")
		assert.Less(t, elapsed, processExitWaitTime/2, "should return well before timeout fires")
	})

	t.Run("returns false when done never fires", func(t *testing.T) {
		t.Parallel()
		done := make(chan error) // never signalled

		got := waitForProcessExit(done)

		assert.False(t, got, "expected false when timeout elapses without done")
	})
}
