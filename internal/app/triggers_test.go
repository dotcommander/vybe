package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadTriggerWords_DefaultsWhenFileMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	ResetTriggerWordsForTest()
	t.Cleanup(ResetTriggerWordsForTest)

	got := LoadTriggerWords()
	for _, w := range []string{"brief me", "remember", "remember?", "brief", "what's pending", "status"} {
		_, ok := got[w]
		require.True(t, ok, "default trigger %q must be present", w)
	}
	require.Len(t, got, 6)
}

func TestLoadTriggerWords_ReadsFileAndLowercases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	ResetTriggerWordsForTest()
	t.Cleanup(ResetTriggerWordsForTest)

	dir := filepath.Join(home, ".config", "vybe")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	yaml := "triggers:\n  - \"Brief Me\"\n  - \"  custom  \"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "triggers.yaml"), []byte(yaml), 0o600))

	got := LoadTriggerWords()
	_, ok := got["brief me"]
	require.True(t, ok, "trigger keys must be lowercased+trimmed")
	_, ok = got["custom"]
	require.True(t, ok, "whitespace must be trimmed from custom triggers")
	require.Len(t, got, 2)
}
