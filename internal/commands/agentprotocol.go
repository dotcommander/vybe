package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const agentProtocolFilename = "agent_protocol.json"

// agentProtocolVersion is the current on-disk format version.
// Bump when the default protocol fields change; a file with an older
// (or missing) version is treated as stale and replaced by LoadOrWriteAgentProtocol.
const agentProtocolVersion = 1

// agentProtocol is the agent-facing protocol guidance emitted under
// `data.agent_protocol` by `vybe schema`. The JSON field names form a
// stable contract consumed by external callers (e.g. the opencode bridge);
// do not rename them.
type agentProtocol struct {
	ResumeCommand           string   `json:"resume_command"`
	FocusTaskField          string   `json:"focus_task_field"`
	FocusCommand            string   `json:"focus_command"`
	TerminalStatusCommand   string   `json:"terminal_status_command"`
	BlockCommand            string   `json:"block_command"`
	TerminalStatuses        []string `json:"terminal_statuses"`
	OptionalProgressCommand string   `json:"optional_progress_command"`
	RememberCommand         string   `json:"remember_command"`
	Rule                    string   `json:"rule"`
}

// agentProtocolFile is the on-disk envelope: {"version":N,"agent_protocol":{...}}.
type agentProtocolFile struct {
	Version       int           `json:"version"`
	AgentProtocol agentProtocol `json:"agent_protocol"`
}

// buildAgentProtocol returns the built-in default agent protocol.
// This is the single source of truth for default protocol values; Go source
// hardcodes them nowhere else — they are always derived from this function or
// the agent_protocol.json file.
func buildAgentProtocol() agentProtocol {
	return agentProtocol{
		ResumeCommand:           "vybe resume --agent <AGENT>",
		FocusTaskField:          "data.focus_task_id",
		FocusCommand:            "vybe focus --agent <AGENT>",
		TerminalStatusCommand:   "vybe done <TASK_ID> --note \"<summary>\"",
		BlockCommand:            "vybe block <TASK_ID> --reason \"<why>\" [--failure]",
		TerminalStatuses:        []string{"completed", "blocked"},
		OptionalProgressCommand: "vybe note <TASK_ID> \"<message>\"",
		RememberCommand:         "vybe remember \"<key>=<value>\" [--scope task --scope-id <TASK_ID>]",
		Rule:                    "Per loop step, close the focus task with exactly one terminal: `vybe done <id>` (completed) or `vybe block <id> --reason ...` (blocked). Omit --request-id unless deliberately retrying.",
	}
}

// LoadAgentProtocol is a pure read — it NEVER writes to disk.
//
//   - File absent                              → built-in defaults in memory (no write, nil error).
//   - File present + version == current        → file's protocol.
//   - File present + version missing/older      → built-in defaults in memory (no write, nil error).
//   - File present + malformed JSON             → built-in defaults in memory plus a wrapped error
//     (callers that must not fail, like schema, may ignore the error and use the returned defaults).
func LoadAgentProtocol(configDir string) (agentProtocol, error) {
	path := filepath.Join(configDir, agentProtocolFilename)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return buildAgentProtocol(), nil
	}
	if err != nil {
		return buildAgentProtocol(), fmt.Errorf("read %s: %w", path, err)
	}

	var apf agentProtocolFile
	if jsonErr := json.Unmarshal(data, &apf); jsonErr != nil {
		return buildAgentProtocol(), fmt.Errorf("parse %s: %w", path, jsonErr)
	}

	if apf.Version < agentProtocolVersion {
		return buildAgentProtocol(), nil
	}

	return apf.AgentProtocol, nil
}

// LoadOrWriteAgentProtocol = LoadAgentProtocol + persist.
// If the file is absent OR carries a stale version, it writes the current
// defaults (with the current version stamp) and returns them. If the file is
// present and current, it returns the file's protocol unchanged.
// Write errors are non-fatal: defaults are returned even when persistence fails.
func LoadOrWriteAgentProtocol(configDir string) (agentProtocol, error) {
	path := filepath.Join(configDir, agentProtocolFilename)

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return buildAgentProtocol(), fmt.Errorf("read %s: %w", path, err)
	}

	if err == nil {
		var apf agentProtocolFile
		if jsonErr := json.Unmarshal(data, &apf); jsonErr != nil {
			return buildAgentProtocol(), fmt.Errorf("parse %s: %w", path, jsonErr)
		}
		if apf.Version >= agentProtocolVersion {
			return apf.AgentProtocol, nil
		}
		// Stale: fall through to rewrite with defaults.
	}

	defaults := buildAgentProtocol()
	_ = writeAgentProtocol(path, defaults) // non-fatal: return defaults even on write failure
	return defaults, nil
}

// writeAgentProtocol serializes the protocol to path with the current version stamp.
func writeAgentProtocol(path string, p agentProtocol) error {
	apf := agentProtocolFile{Version: agentProtocolVersion, AgentProtocol: p}
	out, err := json.MarshalIndent(apf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent protocol: %w", err)
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o600)
}
