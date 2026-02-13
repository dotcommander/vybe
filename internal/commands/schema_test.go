package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestNormalizeFlagType(t *testing.T) {
	require.Equal(t, "integer", normalizeFlagType("int64"))
	require.Equal(t, "boolean", normalizeFlagType("bool"))
	require.Equal(t, "string", normalizeFlagType("duration"))
	require.Equal(t, "string", normalizeFlagType("string"))
}

func TestTypedFlagDefault(t *testing.T) {
	require.Equal(t, true, typedFlagDefault("bool", "true"))
	require.Equal(t, 42, typedFlagDefault("int", "42"))
	require.Equal(t, "oops", typedFlagDefault("int", "oops"))
	require.Equal(t, "abc", typedFlagDefault("string", "abc"))
}

func TestIsRequiredFlag(t *testing.T) {
	reqByAnnotation := &pflag.Flag{Annotations: map[string][]string{cobra.BashCompOneRequiredFlag: {"true"}}}
	require.True(t, isRequiredFlag(reqByAnnotation))

	reqByUsage := &pflag.Flag{Usage: "Task id (required)"}
	require.True(t, isRequiredFlag(reqByUsage))

	notReq := &pflag.Flag{Usage: "optional flag"}
	require.False(t, isRequiredFlag(notReq))
}

func TestParseEnumValues(t *testing.T) {
	require.Equal(t, []string{"pending", "in_progress", "completed"}, parseEnumValues("Status options: pending|in_progress|completed"))
	require.Equal(t, []string{"pending", "blocked"}, parseEnumValues("Set status (pending, blocked)"))
	require.Nil(t, parseEnumValues("Example only (e.g. foo, bar)"))
	require.Nil(t, parseEnumValues(""))
}

func TestNormalizeEnumParts(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, normalizeEnumParts([]string{" a ", "[b]", "skip me", "1.2"}))
	require.Nil(t, normalizeEnumParts([]string{"onlyone"}))
}

func TestBuildCommandSchema_CollectsFlagsAndRequired(t *testing.T) {
	root := &cobra.Command{Use: "vybe"}
	root.PersistentFlags().String("agent", "", "Agent name (required)")

	child := &cobra.Command{Use: "task", Short: "Task operations"}
	child.Flags().String("status", "pending", "Status options: pending|in_progress|completed")
	child.Flags().String("hidden-flag", "x", "hidden")
	require.NoError(t, child.Flags().MarkHidden("hidden-flag"))
	root.AddCommand(child)

	schema := buildCommandSchema(child)
	require.Equal(t, "vybe task", schema.Command)
	require.Equal(t, "Task operations", schema.Description)

	props := schema.ArgsSchema["properties"].(map[string]any)
	require.Contains(t, props, "agent")
	require.Contains(t, props, "status")
	require.NotContains(t, props, "hidden-flag")

	status := props["status"].(map[string]any)
	require.Equal(t, "string", status["type"])
	require.Equal(t, "pending", status["default"])
	require.Equal(t, []string{"pending", "in_progress", "completed"}, status["enum"])

	required := schema.ArgsSchema["required"].([]string)
	require.Contains(t, required, "agent")
}

func TestCollectCommandSchemas_FiltersRootSchemaAndHidden(t *testing.T) {
	root := &cobra.Command{Use: "vybe"}
	schemaCmd := &cobra.Command{Use: "schema"}
	visible := &cobra.Command{Use: "task", Short: "Task"}
	hidden := &cobra.Command{Use: "secret", Hidden: true}

	root.AddCommand(schemaCmd, visible, hidden)

	var out []commandArgSchema
	collectCommandSchemas(root, &out)

	require.Len(t, out, 1)
	require.Equal(t, "vybe task", out[0].Command)
}
