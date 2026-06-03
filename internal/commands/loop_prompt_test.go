package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoopPromptLoadOrWrite verifies the loader writes defaults on first call
// and reads back the persisted file on the second call (idempotent), and that
// the persisted values equal the built-in defaults.
func TestLoopPromptLoadOrWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// First call: file absent — build defaults and write them.
	got, err := LoadOrWriteLoopPrompt(dir)
	require.NoError(t, err)
	require.Equal(t, buildLoopPrompt(), got)

	// File should now exist.
	path := filepath.Join(dir, loopPromptFilename)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "loop_prompt.json should have been written")

	// Second call: read from file, same result (idempotent read-back).
	got2, err := LoadLoopPrompt(dir)
	require.NoError(t, err)
	require.Equal(t, buildLoopPrompt(), got2)
}

// TestLoopPromptLoadAbsentReturnsDefaults verifies the pure read returns built-in
// defaults when the file is absent and does NOT create the file.
func TestLoopPromptLoadAbsentReturnsDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, loopPromptFilename)

	got, err := LoadLoopPrompt(dir)
	require.NoError(t, err)
	require.Equal(t, buildLoopPrompt(), got)

	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "LoadLoopPrompt must not create the file")
}

// TestLoopPromptRenderMatchesLegacy verifies that render() reproduces the exact
// byte sequence from the original hardcoded WriteString calls in loop_options.go.
// If this test fails, that is a real drift bug — do not weaken the assertion.
func TestLoopPromptRenderMatchesLegacy(t *testing.T) {
	t.Parallel()

	legacy := "\n== AUTONOMOUS MODE ==\n" +
		"There is no human to ask questions. You must work independently.\n\n" +
		"Execution contract:\n" +
		"1. Work only on \"Your current task\" and its task_id.\n" +
		"2. Optional: emit progress logs with LOG.\n" +
		"3. Before stopping, run exactly one terminal command:\n" +
		"   - DONE: vybe done <id> --note \"<summary>\"  (marks the task completed), OR\n" +
		"   - STUCK: vybe block <id> --reason \"<why>\"  (marks the task blocked).\n" +
		"4. Do not use 'vybe task complete' in autonomous mode.\n"

	require.Equal(t, legacy, buildLoopPrompt().render())
}
