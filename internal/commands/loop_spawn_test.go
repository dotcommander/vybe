package commands

import "testing"

func TestIsClaudeCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "simple", command: "claude", want: true},
		{name: "absolute path", command: "/usr/local/bin/claude", want: true},
		{name: "uppercase", command: "CLAUDE", want: true},
		{name: "trimmed", command: "  claude  ", want: true},
		{name: "non-claude", command: "opencode", want: false},
		{name: "empty", command: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isClaudeCommand(tc.command)
			if got != tc.want {
				t.Fatalf("isClaudeCommand(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}
