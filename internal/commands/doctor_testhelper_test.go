package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/vybe/internal/commands/hookcmd"
)

// vybeHookEntry returns a settings.json hook entry whose command is the literal
// "vybe hook <subcommand>" string. IsVybeHookCommand matches on the command
// string's basename (not the running binary), so this passes the strict check.
func vybeHookEntry(subcommand string) map[string]any {
	return map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": fmt.Sprintf("vybe hook %s", subcommand),
				"timeout": float64(2000),
			},
		},
	}
}

// writeFullVybeSettings writes a settings.json at path with vybe hook entries for
// every registered event, using literal "vybe hook <subcommand>" command strings.
// event→subcommand mapping mirrors buildVybeHooks in hookcmd/manifest.go.
func writeFullVybeSettings(t *testing.T, path string) {
	t.Helper()
	eventSubcommand := map[string]string{
		"SessionStart":       "session-start",
		"UserPromptSubmit":   "prompt",
		"PostToolUseFailure": "tool-failure",
		"PreCompact":         "checkpoint",
		"SessionEnd":         "session-end",
		"TaskCompleted":      "task-completed",
	}

	hooksObj := map[string]any{}
	for _, event := range hookcmd.VybeHookEventNames() {
		sub, ok := eventSubcommand[event]
		require.True(t, ok, "no subcommand mapping for hook event %q — update writeFullVybeSettings", event)
		hooksObj[event] = []any{vybeHookEntry(sub)}
	}

	settings := map[string]any{"hooks": hooksObj}
	data, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}
