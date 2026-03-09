package commands

import (
	"fmt"
	"strings"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// emitRichBrief builds a comprehensive vybe summary for trigger words like "brief me" and "remember".
func emitRichBrief(db *DB, agentName, focusTaskID, projectID string) error {
	var b strings.Builder

	b.WriteString("== VYBE PROJECT SUMMARY ==\n")

	// List all non-completed tasks for this project
	tasks, err := store.ListTasks(db, "", projectID, -1)
	if err != nil {
		return err
	}

	var pending, inProgress, blocked []*models.Task
	for _, t := range tasks {
		switch t.Status {
		case "pending":
			pending = append(pending, t)
		case "in_progress":
			inProgress = append(inProgress, t)
		case "blocked":
			blocked = append(blocked, t)
		}
	}

	if len(inProgress) > 0 {
		b.WriteString("\nIn Progress:\n")
		for _, t := range inProgress {
			fmt.Fprintf(&b, "  [%s] %s", t.ID, t.Title)
			if t.ID == focusTaskID {
				b.WriteString(" <- current focus")
			}
			b.WriteString("\n")
			if t.Description != "" {
				fmt.Fprintf(&b, "    %s\n", t.Description)
			}
		}
	}

	if len(pending) > 0 {
		b.WriteString("\nPending:\n")
		for _, t := range pending {
			fmt.Fprintf(&b, "  [%s] %s\n", t.ID, t.Title)
			if t.Description != "" {
				fmt.Fprintf(&b, "    %s\n", t.Description)
			}
		}
	}

	if len(blocked) > 0 {
		b.WriteString("\nBlocked:\n")
		for _, t := range blocked {
			fmt.Fprintf(&b, "  [%s] %s", t.ID, t.Title)
			if t.BlockedReason != "" {
				fmt.Fprintf(&b, " (%s)", t.BlockedReason)
			}
			b.WriteString("\n")
		}
	}

	total := len(pending) + len(inProgress) + len(blocked)
	if total == 0 {
		b.WriteString("\nNo actionable tasks in this project.\n")
	} else {
		fmt.Fprintf(&b, "\nTotal: %d actionable (%d pending, %d in progress, %d blocked)\n",
			total, len(pending), len(inProgress), len(blocked))
	}

	// Add memory if available
	if focusTaskID != "" {
		brief, err := store.BuildBrief(db, focusTaskID, projectID, agentName)
		if err == nil && brief != nil && len(brief.RelevantMemory) > 0 {
			b.WriteString("\nSaved notes:\n")
			for _, m := range brief.RelevantMemory {
				fmt.Fprintf(&b, "  %s = %s\n", m.Key, m.Value)
			}
		}
	}

	b.WriteString("\nPresent this summary to the user and ask which task(s) they'd like to work on.\n")

	return emitHookJSON("UserPromptSubmit", b.String())
}
