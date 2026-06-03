package commands

import (
	"log/slog"

	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/spf13/cobra"
)

// NewHookCmd creates the hook parent command.
func NewHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "event",
		Aliases: []string{"hook"}, // back-compat: installed settings.json call `vybe hook <sub>`
		Short:   "Event handlers and installers for Claude/OpenCode",
		Args:    cobra.NoArgs,
	}

	cmd.PersistentFlags().String("host", "", "Host protocol: claude (default) | generic; or set VYBE_HOST")

	cmd.AddCommand(newHookInstallCmd())
	cmd.AddCommand(newHookUninstallCmd())
	cmd.AddCommand(hookcmd.NewExportCmd())

	// Hook handler subcommands — called by the hook system, not agents directly.
	// Hidden from help output to reduce command surface noise.
	for _, sub := range []*cobra.Command{
		newHookSessionStartCmd(),
		newHookPromptCmd(),
		newHookToolFailureCmd(),
		newHookMaintenanceCmd("checkpoint", "PreCompact hook — checkpoint maintenance",
			func(agentName, _ string) string { return hookRequestID("checkpoint", agentName) }),
		newHookTaskCompletedCmd(),
		newHookMaintenanceCmd("session-end", "SessionEnd hook — best-effort checkpoint",
			func(agentName, sessionID string) string {
				return stableHookRequestID("session_end", agentName, sessionID)
			}),
	} {
		sub.Hidden = true
		cmd.AddCommand(sub)
	}

	namespaceIndex(cmd)
	return cmd
}

// hookPolicy records how a hook db-call-site treats DB errors.
//
// swallowErrors=true  → use withDBSilent: never pollute stdout, retry openDB once,
//
//	log at warn level. Used where Claude Code stdout must stay clean.
//
// swallowErrors=false → use withDB: on error emit structured JSON to stdout (via cmdErr)
//
//	and run the call-site's diagnostic logger, then exit clean.
//
// The table is keyed per db-call-site (not per handler) because session-start makes two
// calls with different policies in one handler: the compact branch must stay silent while
// the resume branch behaves like the other withDB handlers.
type hookPolicy struct {
	swallowErrors bool
}

// hookPolicies is the single source of truth for hook DB error handling.
// Each key is a db-call-site name passed to runHookDB.
var hookPolicies = map[string]hookPolicy{ //nolint:gochecknoglobals // static policy table; read-only after init
	"session-start-compact": {swallowErrors: true},
	"prompt":                {swallowErrors: true},
	"session-start-resume":  {swallowErrors: false},
	"tool-failure":          {swallowErrors: false},
	"task-completed":        {swallowErrors: false},
	"maintenance":           {swallowErrors: false},
}

// runHookDB looks up the policy for the named hook db-call-site and dispatches
// to the correct DB wrapper. For swallow policies it calls withDBSilent and ignores
// onErr. For propagate policies it calls withDB and, on error, invokes onErr with the
// error so the call site can emit its site-specific diagnostic; the error is not
// returned to cobra (hooks must never block Claude Code).
//
// An unknown name is treated as a propagate policy (fail-loud in logs) — this should
// never happen because every call site uses a key present in hookPolicies.
func runHookDB(name string, fn func(db *DB) error, onErr func(error)) {
	policy, ok := hookPolicies[name]
	if !ok {
		slog.Default().Error("unknown hook policy name", "name", name)
	}
	if policy.swallowErrors {
		withDBSilent(fn)
		return
	}
	if err := withDB(fn); err != nil && onErr != nil {
		onErr(err)
	}
}
