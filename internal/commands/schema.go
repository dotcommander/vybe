package commands

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
)

// NewSchemaCmd creates the schema command. root is used to collect command schemas.
func NewSchemaCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Show command argument schemas with mutation hints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaMode(root)
		},
	}
}

func runSchemaMode(root *cobra.Command) error {
	type resp struct {
		Commands      []commandArgSchema `json:"commands"`
		AgentProtocol agentProtocol      `json:"agent_protocol"`
	}
	schemas := make([]commandArgSchema, 0)
	collectCommandSchemas(root, &schemas)

	// Read agent protocol from config; fall back to built-in defaults on any
	// failure (absent/stale/corrupt) so `schema` never breaks.
	protocol := buildAgentProtocol()
	if dir, err := app.ConfigDir(); err == nil {
		if loaded, lerr := LoadAgentProtocol(dir); lerr == nil {
			protocol = loaded
		}
	}

	return output.PrintSuccess(resp{Commands: schemas, AgentProtocol: protocol})
}

type commandArgSchema struct {
	Command           string         `json:"command"`
	Description       string         `json:"description,omitempty"`
	ArgsSchema        map[string]any `json:"args_schema"`
	Mutates           bool           `json:"mutates"`
	RequiresRequestID bool           `json:"requires_request_id"`
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
	properties := map[string]any{}
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

		flagSchema := map[string]any{
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

	argsSchema := map[string]any{
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

func typedFlagDefault(flagType, raw string) any {
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

// isRequiredFlag checks if a flag is required, first via cobra annotation
// (BashCompOneRequiredFlag), then by checking for "(required)" in usage text.
// WARNING: Changing usage text that contains "(required)" affects schema output.
func isRequiredFlag(f *pflag.Flag) bool {
	if f.Annotations != nil {
		if vals, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok && len(vals) > 0 && vals[0] == "true" {
			return true
		}
	}

	usage := strings.ToLower(strings.TrimSpace(f.Usage))
	return strings.Contains(usage, "(required)")
}

// parseEnumValues extracts enum values from flag usage text by looking for
// "description: a|b|c" patterns (pipe-separated values after a colon).
// WARNING: Changing usage text format affects schema contract for machine callers.
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
