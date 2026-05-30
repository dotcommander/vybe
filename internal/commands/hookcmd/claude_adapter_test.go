package hookcmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrevSessionCacheVariables(t *testing.T) {
	t.Parallel()

	// Save and restore cache state so this test is order-independent.
	savedPath := prevSessionCachePath
	savedMod := prevSessionCacheModTime
	savedResult := prevSessionCacheResult
	t.Cleanup(func() {
		prevSessionCachePath = savedPath
		prevSessionCacheModTime = savedMod
		prevSessionCacheResult = savedResult
	})

	// Reset to known-empty state for this test.
	prevSessionCachePath = ""
	prevSessionCacheModTime = time.Time{}
	prevSessionCacheResult = ""

	// Verify cache variables are accessible and start empty after reset.
	require.Empty(t, prevSessionCachePath)
	require.True(t, prevSessionCacheModTime.IsZero())
	require.Empty(t, prevSessionCacheResult)

	// Verify ReadPreviousSessionContext returns empty for nonexistent path
	// (doesn't panic on cache operations).
	result := ReadPreviousSessionContext("/nonexistent/path/for/cache/test", "sess_test")
	require.Empty(t, result)
}
