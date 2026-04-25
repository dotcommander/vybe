package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dotcommander/vybe/internal/models"
)

// validateMemoryKind checks that kind is one of the allowed memory kinds.
// Empty string is NOT allowed here — callers must default to "fact" before calling.
func validateMemoryKind(kind string) error {
	if models.MemoryKind(kind).IsValid() {
		return nil
	}
	return fmt.Errorf("invalid kind: %q (must be one of: fact, directive, lesson)", kind)
}

// inferValueType attempts to detect the value type from the input string.
func inferValueType(value string) string {
	value = strings.TrimSpace(value)

	// Check for boolean
	if value == "true" || value == "false" {
		return "boolean"
	}

	// Check for number
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return "number"
	}

	// Check for JSON object or array
	if (strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")) ||
		(strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]")) {
		// Try to parse as JSON
		var js any
		if err := json.Unmarshal([]byte(value), &js); err == nil {
			switch js.(type) {
			case []any:
				return "array"
			case map[string]any:
				return "json"
			}
		}
	}

	// Default to string
	return "string"
}

// isValidValueType reports whether vt is an allowed memory value type.
func isValidValueType(vt string) bool {
	switch vt {
	case "string", "number", "boolean", "json", "array":
		return true
	}
	return false
}

// validateValueType checks that an explicit value type is in the allowed set.
// Empty string is allowed (triggers inference).
func validateValueType(valueType string) error {
	if valueType == "" {
		return nil
	}
	if !isValidValueType(valueType) {
		return fmt.Errorf("invalid value_type: %q (must be one of: string, number, boolean, json, array)", valueType)
	}
	return nil
}

// truncateRunes truncates s to at most maxRunes runes, appending "…" if truncated.
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// validateScope ensures scope and scope_id are valid.
func validateScope(scope, scopeID string) error {
	switch scope {
	case "global", "project", "task", "agent":
		// valid
	default:
		return fmt.Errorf("invalid scope: %s (must be one of: global, project, task, agent)", scope)
	}

	// Global scope should not have a scope_id
	if scope == "global" && scopeID != "" {
		return errors.New("global scope cannot have a scope_id")
	}

	// Non-global scopes require a scope_id
	if scope != "global" && scopeID == "" {
		return fmt.Errorf("%s scope requires a scope_id", scope)
	}

	return nil
}

// boolToInt converts bool to SQLite integer (1/0).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
