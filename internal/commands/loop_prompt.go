package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const loopPromptFilename = "loop_prompt.json"

// loopPromptVersion is the current on-disk format version.
// Bump when the default prompt fields change; a file with an older
// (or missing) version is treated as stale and replaced by LoadOrWriteLoopPrompt.
const loopPromptVersion = 1

// loopPrompt holds the autonomous-mode behavioral text injected into the
// agent prompt by buildAgentPrompt. The JSON field names form a stable contract;
// do not rename them.
type loopPrompt struct {
	Header          string   `json:"header"`
	Intro           string   `json:"intro"`
	ContractHeading string   `json:"contract_heading"`
	ContractSteps   []string `json:"contract_steps"`
}

// loopPromptFile is the on-disk envelope: {"version":N,"loop_prompt":{...}}.
type loopPromptFile struct {
	Version    int        `json:"version"`
	LoopPrompt loopPrompt `json:"loop_prompt"`
}

// buildLoopPrompt returns the built-in default loop prompt.
// This is the single source of truth for default text; Go source hardcodes
// the autonomous-mode section nowhere else — values are always derived from
// this function or the loop_prompt.json file.
func buildLoopPrompt() loopPrompt {
	return loopPrompt{
		Header:          "== AUTONOMOUS MODE ==",
		Intro:           "There is no human to ask questions. You must work independently.",
		ContractHeading: "Execution contract:",
		ContractSteps: []string{
			`1. Work only on "Your current task" and its task_id.`,
			"2. Optional: emit progress logs with LOG.",
			"3. Before stopping, run exactly one terminal command:",
			`   - DONE: vybe done <id> --note "<summary>"  (marks the task completed), OR`,
			`   - STUCK: vybe block <id> --reason "<why>"  (marks the task blocked).`,
			"4. Do not use 'vybe task complete' in autonomous mode.",
		},
	}
}

// render reproduces the exact byte sequence that buildAgentPrompt wrote via
// WriteString calls (lines 40-48 of loop_options.go), now sourced from struct fields.
//
// Original sequence:
//
//	b.WriteString("\n== AUTONOMOUS MODE ==\n")
//	b.WriteString("There is no human to ask questions. You must work independently.\n\n")
//	b.WriteString("Execution contract:\n")
//	b.WriteString("1. Work only on \"Your current task\" and its task_id.\n")
//	b.WriteString("2. Optional: emit progress logs with LOG.\n")
//	b.WriteString("3. Before stopping, run exactly one terminal command:\n")
//	b.WriteString("   - DONE: vybe done <id> --note \"<summary>\"  (marks the task completed), OR\n")
//	b.WriteString("   - STUCK: vybe block <id> --reason \"<why>\"  (marks the task blocked).\n")
//	b.WriteString("4. Do not use 'vybe task complete' in autonomous mode.\n")
func (p loopPrompt) render() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(p.Header)
	b.WriteString("\n")
	b.WriteString(p.Intro)
	b.WriteString("\n\n")
	b.WriteString(p.ContractHeading)
	b.WriteString("\n")
	for _, step := range p.ContractSteps {
		b.WriteString(step)
		b.WriteString("\n")
	}
	return b.String()
}

// LoadLoopPrompt is a pure read — it NEVER writes to disk.
//
//   - File absent                              → built-in defaults in memory (no write, nil error).
//   - File present + version == current        → file's loop prompt.
//   - File present + version missing/older     → built-in defaults in memory (no write, nil error).
//   - File present + malformed JSON            → built-in defaults in memory plus a wrapped error
//     (callers that must not fail may ignore the error and use the returned defaults).
func LoadLoopPrompt(configDir string) (loopPrompt, error) {
	path := filepath.Join(configDir, loopPromptFilename)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return buildLoopPrompt(), nil
	}
	if err != nil {
		return buildLoopPrompt(), fmt.Errorf("read %s: %w", path, err)
	}

	var lpf loopPromptFile
	if jsonErr := json.Unmarshal(data, &lpf); jsonErr != nil {
		return buildLoopPrompt(), fmt.Errorf("parse %s: %w", path, jsonErr)
	}

	if lpf.Version < loopPromptVersion {
		return buildLoopPrompt(), nil
	}

	return lpf.LoopPrompt, nil
}

// LoadOrWriteLoopPrompt = LoadLoopPrompt + persist.
// If the file is absent OR carries a stale version, it writes the current
// defaults (with the current version stamp) and returns them. If the file is
// present and current, it returns the file's prompt unchanged.
// Write errors are non-fatal: defaults are returned even when persistence fails.
func LoadOrWriteLoopPrompt(configDir string) (loopPrompt, error) {
	path := filepath.Join(configDir, loopPromptFilename)

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return buildLoopPrompt(), fmt.Errorf("read %s: %w", path, err)
	}

	if err == nil {
		var lpf loopPromptFile
		if jsonErr := json.Unmarshal(data, &lpf); jsonErr != nil {
			return buildLoopPrompt(), fmt.Errorf("parse %s: %w", path, jsonErr)
		}
		if lpf.Version >= loopPromptVersion {
			return lpf.LoopPrompt, nil
		}
		// Stale: fall through to rewrite with defaults.
	}

	defaults := buildLoopPrompt()
	_ = writeLoopPrompt(path, defaults) // non-fatal: return defaults even on write failure
	return defaults, nil
}

// writeLoopPrompt serializes the prompt to path with the current version stamp.
func writeLoopPrompt(path string, p loopPrompt) error {
	lpf := loopPromptFile{Version: loopPromptVersion, LoopPrompt: p}
	out, err := json.MarshalIndent(lpf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal loop prompt: %w", err)
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o600)
}
