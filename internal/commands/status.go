package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
)

// NewStatusCmd creates the status command. Pass the root command so --schema can collect schemas.
// Callers in root.go must call NewStatusCmd(root) after the root command is fully wired.
//
//nolint:revive,funlen // status display requires many conditional checks for completeness; splitting degrades the linear status-collection flow
func NewStatusCmd(root *cobra.Command) *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show vybe installation status and system overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDefaultStatus(cmd, check)
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Run database connectivity check (SELECT 1)")

	return cmd
}

func runEventsMode(cmd *cobra.Command, all bool, taskID, kind string, since int64, limit int, asc, includeArchived bool) error {
	agentName := resolveActorName(cmd, "")
	if all {
		agentName = ""
	}
	if !all && agentName == "" {
		return cmdErr(errors.New("agent is required unless --all is set (set --agent or VYBE_AGENT)"))
	}

	var events []*models.Event
	if err := withDB(func(db *DB) error {
		ev, err := store.ListEvents(db, store.ListEventsParams{
			AgentName:       agentName,
			TaskID:          taskID,
			Kind:            kind,
			SinceID:         since,
			Limit:           limit,
			Desc:            !asc,
			IncludeArchived: includeArchived,
		})
		if err != nil {
			return err
		}
		events = ev
		return nil
	}); err != nil {
		return err
	}

	type resp struct {
		Agent  string          `json:"agent,omitempty"`
		TaskID string          `json:"task_id,omitempty"`
		Kind   string          `json:"kind,omitempty"`
		Since  int64           `json:"since_id,omitempty"`
		Count  int             `json:"count"`
		Events []*models.Event `json:"events"`
	}
	return output.PrintSuccess(resp{
		Agent:  agentName,
		TaskID: taskID,
		Kind:   kind,
		Since:  since,
		Count:  len(events),
		Events: events,
	})
}

func runSchemaMode(root *cobra.Command) error {
	type resp struct {
		Commands []commandArgSchema `json:"commands"`
	}
	schemas := make([]commandArgSchema, 0)
	collectCommandSchemas(root, &schemas)
	return output.PrintSuccess(resp{Commands: schemas})
}

func runArtifactsMode(taskID string, limit int) error {
	if taskID == "" {
		return cmdErr(errors.New("--task-id is required"))
	}

	var artifacts []*models.Artifact
	if err := withDB(func(db *DB) error {
		a, err := actions.ArtifactListByTask(db, taskID, limit)
		if err != nil {
			return err
		}
		artifacts = a
		return nil
	}); err != nil {
		return err
	}

	type resp struct {
		TaskID    string             `json:"task_id"`
		Count     int                `json:"count"`
		Artifacts []*models.Artifact `json:"artifacts"`
	}
	return output.PrintSuccess(resp{
		TaskID:    taskID,
		Count:     len(artifacts),
		Artifacts: artifacts,
	})
}

func runDefaultStatus(cmd *cobra.Command, check bool) error {
	// 1. Resolve DB path
	dbPath, dbSource, err := app.ResolveDBPathDetailed()
	if err != nil {
		return cmdErr(err)
	}

	// 2. Build response structure
	type dbInfo struct {
		Path      string `json:"path"`
		Source    string `json:"source"`
		OK        bool   `json:"ok"`
		SizeBytes *int64 `json:"size_bytes,omitempty"`
		Error     string `json:"error,omitempty"`
	}

	type hooksInfo struct {
		Claude         bool            `json:"claude"`
		ClaudeEvents   map[string]bool `json:"claude_events,omitempty"`
		ClaudeSettings []string        `json:"claude_settings_paths,omitempty"`
		OpenCode       opencodeDetail  `json:"opencode"`
	}

	type resp struct {
		DB          dbInfo                       `json:"db"`
		Hooks       hooksInfo                    `json:"hooks"`
		Maintenance app.EventMaintenanceSettings `json:"maintenance"`
		Counts      *store.StatusCounts          `json:"counts,omitempty"`
		AgentState  *models.AgentState           `json:"agent_state,omitempty"`
		QueryOK     *bool                        `json:"query_ok,omitempty"`
		QueryError  string                       `json:"query_error,omitempty"`
		Hint        string                       `json:"hint,omitempty"`
		Diagnostics []store.Diagnostic           `json:"diagnostics,omitempty"`
	}

	result := resp{
		DB: dbInfo{
			Path:   dbPath,
			Source: dbSource,
		},
		Maintenance: app.EffectiveEventMaintenanceSettings(),
	}

	// 3. Check hooks
	result.Hooks.Claude, result.Hooks.ClaudeEvents, result.Hooks.ClaudeSettings = checkClaudeHook()
	result.Hooks.OpenCode = checkOpenCodeHookDetail()

	// 4. Try to open DB
	db, err := store.OpenDB(dbPath)
	if err != nil {
		result.DB.OK = false
		result.DB.Error = err.Error()
		if check {
			qOK := false
			result.QueryOK = &qOK
			result.QueryError = "db not available"
			result.Hint = "If this is running in a sandboxed environment, set db_path to a writable location or use --db-path."
		}
		return output.PrintSuccess(result)
	}

	result.DB.OK = true
	defer func() { _ = db.Close() }()

	// 5. Get DB file size
	if stat, err := os.Stat(dbPath); err == nil {
		size := stat.Size()
		result.DB.SizeBytes = &size
	}

	// 6. Get counts
	if counts, err := store.GetStatusCounts(db); err == nil {
		result.Counts = counts
	}

	// 7. Load agent state if --agent is provided
	agentName := resolveActorName(cmd, "")
	if agentName != "" {
		if state, err := store.LoadOrCreateAgentState(db, agentName); err == nil {
			result.AgentState = state
		}
	}

	// 8. Health check (--check): run SELECT 1 to verify connectivity
	if check {
		var one int
		qErr := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one)
		qOK := qErr == nil
		result.QueryOK = &qOK
		if !qOK {
			result.QueryError = qErr.Error()
		}

		// Run consistency diagnostics
		if diagnostics, diagErr := store.RunDiagnostics(db); diagErr == nil {
			result.Diagnostics = diagnostics
		}
	}

	return output.PrintSuccess(result)
}

// checkClaudeHook checks if vybe hooks are installed in Claude settings.
func checkClaudeHook() (bool, map[string]bool, []string) {
	paths := []string{claudeSettingsPath(), projectClaudeSettingsPath()}
	events := make(map[string]bool)
	for _, name := range vybeHookEventNames() {
		events[name] = false
	}

	foundPaths := make([]string, 0, len(paths))
	installedAny := false

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		foundPaths = append(foundPaths, path)

		var settings struct {
			Hooks map[string][]any `json:"hooks"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			continue
		}

		for eventName, entries := range settings.Hooks {
			if !hasVybeHook(entries) {
				continue
			}
			installedAny = true
			events[eventName] = true
		}
	}

	sort.Strings(foundPaths)
	return installedAny, events, foundPaths
}

type opencodeDetail struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Status    string `json:"status"` // "current", "modified", "missing"
}

// checkOpenCodeHookDetail checks vybe bridge plugin status in OpenCode.
func checkOpenCodeHookDetail() opencodeDetail {
	path := opencodePluginPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return opencodeDetail{Path: path, Status: "missing"}
	}
	status := "modified"
	if string(data) == opencodeBridgePluginSource {
		status = "current"
	}
	return opencodeDetail{Installed: true, Path: path, Status: status}
}

// Schema helper functions (moved from schema.go which is deleted).

type commandArgSchema struct {
	Command           string                 `json:"command"`
	Description       string                 `json:"description,omitempty"`
	ArgsSchema        map[string]interface{} `json:"args_schema"`
	Mutates           bool                   `json:"mutates"`
	RequiresRequestID bool                   `json:"requires_request_id"`
}

func collectCommandSchemas(cmd *cobra.Command, out *[]commandArgSchema) {
	if cmd.Name() != "" && cmd.Name() != "vybe" && cmd.Name() != "schema" && !cmd.Hidden {
		*out = append(*out, buildCommandSchema(cmd))
	}

	for _, child := range cmd.Commands() {
		collectCommandSchemas(child, out)
	}
}

func buildCommandSchema(cmd *cobra.Command) commandArgSchema {
	properties := map[string]interface{}{}
	required := make([]string, 0)
	seen := map[string]bool{}

	addFlag := func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true

		flagSchema := map[string]interface{}{
			"type":        normalizeFlagType(f.Value.Type()),
			"description": f.Usage,
		}

		if f.DefValue != "" {
			flagSchema["default"] = typedFlagDefault(f.Value.Type(), f.DefValue)
		}

		if enumValues := parseEnumValues(f.Usage); len(enumValues) > 0 {
			flagSchema["enum"] = enumValues
		}

		properties[f.Name] = flagSchema

		if isRequiredFlag(f) {
			required = append(required, f.Name)
		}
	}

	cmd.InheritedFlags().VisitAll(addFlag)
	cmd.NonInheritedFlags().VisitAll(addFlag)

	argsSchema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		argsSchema["required"] = required
	}

	return commandArgSchema{
		Command:           cmd.CommandPath(),
		Description:       cmd.Short,
		ArgsSchema:        argsSchema,
		Mutates:           isMutatingCommand(cmd),
		RequiresRequestID: requiresRequestID(cmd),
	}
}

// isMutatingCommand returns true if the command modifies state.
// Determined by the "mutates" annotation on the command.
func isMutatingCommand(cmd *cobra.Command) bool {
	return cmd.Annotations["mutates"] == "true"
}

// requiresRequestID returns true if the command requires --request-id for idempotency.
// Determined by the "request_id" annotation on the command.
func requiresRequestID(cmd *cobra.Command) bool {
	return cmd.Annotations["request_id"] == "true"
}

func normalizeFlagType(flagType string) string {
	switch flagType {
	case "int", "int64", "int32", "uint", "uint64", "uint32":
		return "integer"
	case "bool":
		return "boolean"
	case "duration":
		return "string"
	default:
		return "string"
	}
}

func typedFlagDefault(flagType, raw string) interface{} {
	switch flagType {
	case "bool":
		v, err := strconv.ParseBool(raw)
		if err == nil {
			return v
		}
	case "int", "int64", "int32", "uint", "uint64", "uint32":
		v, err := strconv.Atoi(raw)
		if err == nil {
			return v
		}
	}
	return raw
}

func isRequiredFlag(f *pflag.Flag) bool {
	if f.Annotations != nil {
		if vals, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok && len(vals) > 0 && vals[0] == "true" {
			return true
		}
	}

	usage := strings.ToLower(strings.TrimSpace(f.Usage))
	return strings.Contains(usage, "(required)")
}

func parseEnumValues(usage string) []string {
	usage = strings.TrimSpace(usage)
	if usage == "" {
		return nil
	}

	if idx := strings.Index(usage, ":"); idx >= 0 {
		cand := strings.TrimSpace(usage[idx+1:])
		if strings.Contains(cand, "|") {
			parts := strings.Split(cand, "|")
			return normalizeEnumParts(parts)
		}
	}

	open := strings.LastIndex(usage, "(")
	close := strings.LastIndex(usage, ")")
	if open >= 0 && close > open {
		cand := usage[open+1 : close]
		if strings.Contains(strings.ToLower(cand), "e.g.") {
			return nil
		}
		if strings.Contains(cand, ",") {
			parts := strings.Split(cand, ",")
			return normalizeEnumParts(parts)
		}
	}

	return nil
}

func normalizeEnumParts(parts []string) []string {
	values := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, "[]"))
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, ".") {
			continue
		}
		if strings.Contains(p, " ") {
			continue
		}
		values = append(values, p)
	}
	if len(values) < 2 {
		return nil
	}
	return values
}
