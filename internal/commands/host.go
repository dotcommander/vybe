package commands

import (
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type hostKind string

const (
	hostClaude  hostKind = "claude"
	hostGeneric hostKind = "generic"
)

// resolveHost selects the host protocol: --host flag > VYBE_HOST env > "claude".
// Unknown values fall back to claude (fail-safe: a typo never silently breaks an
// installed Claude hook).
func resolveHost(cmd *cobra.Command) hostKind {
	v := ""
	if f, err := cmd.Flags().GetString("host"); err == nil {
		v = f
	}
	if v == "" {
		v = os.Getenv("VYBE_HOST")
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "generic":
		return hostGeneric
	default:
		return hostClaude
	}
}

// resolveCanonical reads stdin via the selected host's parser and resolves agent +
// cwd. Returns the canonical event plus a hookContext for handler plumbing. The
// claude branch is byte-identical to the pre-Phase-1 resolveHookContext + toCanonical.
func resolveCanonical(cmd *cobra.Command, kind EventKind) (CanonicalEvent, hookContext) {
	host := resolveHost(cmd)

	var ev CanonicalEvent
	var genericAgent string
	var claudeInput hookInput

	switch host {
	case hostGeneric:
		ev, genericAgent = readGenericCanonical(kind)
	default:
		claudeInput = readHookStdin()
		ev = claudeInput.toCanonical(kind)
	}

	agentName := resolveActorName(cmd, "")
	if agentName == "" {
		switch host {
		case hostGeneric:
			agentName = genericAgent
			if agentName == "" {
				agentName = genericHostAgentName
			}
		default:
			agentName = defaultHostAgentName
		}
		// Match resolveHookContext's warning exactly for the claude branch:
		// fields "agent" + "hint", no "host" field, so the claude path stays
		// behavior-identical to pre-Phase-1.
		slog.Default().Warn("hook using default agent identity",
			"agent", agentName,
			"hint", "set VYBE_AGENT or --agent to avoid cross-session contamination")
	}

	cwd := ev.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return ev, hookContext{Input: claudeInput, AgentName: agentName, CWD: cwd}
}

// renderResult writes a ContextResult via the selected host's output renderer.
func renderResult(cmd *cobra.Command, claudeEventName string, res ContextResult) error {
	if resolveHost(cmd) == hostGeneric {
		return renderGenericResult(res)
	}
	return renderClaudeResult(claudeEventName, res)
}
