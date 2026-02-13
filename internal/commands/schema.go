package commands

import (
	"strconv"
	"strings"

	"github.com/dotcommander/vibe/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type commandArgSchema struct {
	Command     string                 `json:"command"`
	Description string                 `json:"description,omitempty"`
	ArgsSchema  map[string]interface{} `json:"args_schema"`
}

func newSchemaCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Show command argument schemas",
		Long:  "Output machine-readable JSON schema for command arguments and flags.",
		RunE: func(cmd *cobra.Command, args []string) error {
			type resp struct {
				Commands []commandArgSchema `json:"commands"`
			}

			schemas := make([]commandArgSchema, 0)
			collectCommandSchemas(root, &schemas)
			return output.PrintSuccess(resp{Commands: schemas})
		},
	}

	return cmd
}

func collectCommandSchemas(cmd *cobra.Command, out *[]commandArgSchema) {
	if cmd.Name() != "" && cmd.Name() != "vibe" && cmd.Name() != "schema" && !cmd.Hidden {
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
		Command:     cmd.CommandPath(),
		Description: cmd.Short,
		ArgsSchema:  argsSchema,
	}
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
