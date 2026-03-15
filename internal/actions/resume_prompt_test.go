package actions

import (
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestBuildPrompt_BudgetLimitsVariableSections(t *testing.T) {
	memories := make([]*models.Memory, 30)
	for i := range memories {
		memories[i] = &models.Memory{
			Key:   strings.Repeat("k", 50),
			Value: strings.Repeat("v", 200),
		}
	}

	events := make([]*models.Event, 20)
	for i := range events {
		events[i] = &models.Event{
			Kind:    "progress",
			Message: strings.Repeat("m", 200),
		}
	}

	brief := &store.BriefPacket{
		Task: &models.Task{
			ID:     "task_123",
			Title:  "Test Task",
			Status: "in_progress",
		},
		RelevantMemory: memories,
		RecentEvents:   events,
		Artifacts:      []*models.Artifact{},
	}

	prompt := buildPrompt("agent1", brief, nil)

	memLines := strings.Count(prompt, " = "+strings.Repeat("v", 200))
	eventLines := strings.Count(prompt, strings.Repeat("m", 200))

	assert.Less(t, memLines, 30, "budget should limit memory items below the full 30")
	assert.Less(t, memLines+eventLines, 30, "total variable items should be budget-limited")

	assert.Contains(t, prompt, "VYBE (task tracker)")
	assert.Contains(t, prompt, "Decision protocol")
	assert.Contains(t, prompt, "COMMANDS")
}

func TestBuildPrompt_EmptyMemoryExpandsEventBudget(t *testing.T) {
	events := make([]*models.Event, 10)
	for i := range events {
		events[i] = &models.Event{
			Kind:    "progress",
			Message: "short event message",
		}
	}

	brief := &store.BriefPacket{
		Task: &models.Task{
			ID:     "task_456",
			Title:  "Test Task",
			Status: "in_progress",
		},
		RelevantMemory: []*models.Memory{},
		RecentEvents:   events,
		Artifacts:      []*models.Artifact{},
	}

	prompt := buildPrompt("agent1", brief, nil)

	eventLines := strings.Count(prompt, "short event message")
	assert.Equal(t, 10, eventLines, "with no memories, all short events should fit in budget")
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{strings.Repeat("x", 100), 25},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, estimateTokens(tt.input), "input: %q", tt.input)
	}
}
