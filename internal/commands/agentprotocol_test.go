package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAgentProtocolLoadOrWrite verifies the loader writes defaults on first call
// and reads back the persisted file on the second call (idempotent), and that the
// persisted values equal the built-in defaults (no behavior change from externalization).
func TestAgentProtocolLoadOrWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// First call: file absent — build defaults and write them.
	got, err := LoadOrWriteAgentProtocol(dir)
	require.NoError(t, err)
	require.Equal(t, buildAgentProtocol(), got)

	// File should now exist.
	path := filepath.Join(dir, agentProtocolFilename)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "agent_protocol.json should have been written")

	// Second call: read from file, same result.
	got2, err := LoadOrWriteAgentProtocol(dir)
	require.NoError(t, err)
	require.Equal(t, buildAgentProtocol(), got2)
}

// TestAgentProtocolLoadAbsentReturnsDefaults verifies the pure read returns built-in
// defaults when the file is absent and does NOT create the file.
func TestAgentProtocolLoadAbsentReturnsDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, agentProtocolFilename)

	got, err := LoadAgentProtocol(dir)
	require.NoError(t, err)
	require.Equal(t, buildAgentProtocol(), got)

	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "LoadAgentProtocol must not create the file")
}

// TestAgentProtocolLoadCorruptReturnsError verifies that a malformed file yields an
// error AND still returns built-in defaults, so callers like schema can ignore the
// error and stay functional.
func TestAgentProtocolLoadCorruptReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, agentProtocolFilename)
	require.NoError(t, os.WriteFile(path, []byte("{corrupt json"), 0o600))

	got, err := LoadAgentProtocol(dir)
	require.Error(t, err, "corrupt JSON must return an error")
	require.Equal(t, buildAgentProtocol(), got, "corrupt path must still return built-in defaults")
}

// TestAgentProtocolCustomFilePreserved verifies a current-versioned file is read as-is.
func TestAgentProtocolCustomFilePreserved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	custom := agentProtocol{
		ResumeCommand:    "custom resume",
		FocusTaskField:   "data.focus_task_id",
		TerminalStatuses: []string{"done"},
	}
	apf := agentProtocolFile{Version: agentProtocolVersion, AgentProtocol: custom}
	data, err := json.MarshalIndent(apf, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	require.NoError(t, os.WriteFile(filepath.Join(dir, agentProtocolFilename), data, 0o600))

	got, err := LoadOrWriteAgentProtocol(dir)
	require.NoError(t, err)
	require.Equal(t, "custom resume", got.ResumeCommand)
	require.Equal(t, []string{"done"}, got.TerminalStatuses)
}

// TestAgentProtocolStaleVersionReturnsDefaults verifies a stale (version 0) file
// causes the pure read to return built-in defaults without rewriting, and the
// load-or-write path to rewrite with the current version.
func TestAgentProtocolStaleVersionReturnsDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, agentProtocolFilename)

	stale := agentProtocolFile{Version: 0, AgentProtocol: agentProtocol{ResumeCommand: "old"}}
	staleData, err := json.MarshalIndent(stale, "", "  ")
	require.NoError(t, err)
	staleData = append(staleData, '\n')
	require.NoError(t, os.WriteFile(path, staleData, 0o600))

	// Pure read: built-in defaults, file unchanged.
	roGot, err := LoadAgentProtocol(dir)
	require.NoError(t, err)
	require.Equal(t, buildAgentProtocol(), roGot)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, staleData, after, "LoadAgentProtocol must not write to disk")

	// Load-or-write: built-in defaults, file rewritten with current version.
	rwGot, err := LoadOrWriteAgentProtocol(dir)
	require.NoError(t, err)
	require.Equal(t, buildAgentProtocol(), rwGot)
	afterRW, err := os.ReadFile(path)
	require.NoError(t, err)
	var rewritten agentProtocolFile
	require.NoError(t, json.Unmarshal(afterRW, &rewritten))
	require.Equal(t, agentProtocolVersion, rewritten.Version)
}
