package commands

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

// NewIngestCmd creates the ingest command group.
func NewIngestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest external data sources into vybe",
		Long:  "Import events from external tools (Claude Code history, etc.)",
	}

	cmd.AddCommand(newIngestHistoryCmd())
	return cmd
}

// historyEntry represents a single line from Claude Code's history.jsonl.
type historyEntry struct {
	Display   string `json:"display"`
	Timestamp int64  `json:"timestamp"`
	Project   string `json:"project"`
	SessionID string `json:"sessionId"`
}

//nolint:gocognit,gocyclo,funlen,revive // history ingestion parses mixed event kinds with different metadata shapes; each branch handles a distinct event type
func newIngestHistoryCmd() *cobra.Command {
	var (
		filePath  string
		project   string
		dryRun    bool
		since     string
		batchSize int
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Ingest Claude Code history.jsonl",
		Long:  "Reads ~/.claude/history.jsonl and imports user prompts as events",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := resolveActorName(cmd, "")
			if agentName == "" {
				agentName = "claude"
			}

			// Resolve file path
			if filePath == "" {
				homeDir, homeErr := os.UserHomeDir()
				if homeErr != nil {
					return cmdErr(fmt.Errorf("cannot resolve home directory: %w", homeErr))
				}
				filePath = filepath.Join(homeDir, ".claude", "history.jsonl")
			}

			// Parse --since
			var sinceTs int64
			if since != "" {
				parsedDate, parseErr := time.Parse(time.DateOnly, since)
				if parseErr != nil {
					return cmdErr(fmt.Errorf("--since must be YYYY-MM-DD: %w", parseErr))
				}
				sinceTs = parsedDate.UnixMilli()
			}

			// Read and filter entries
			entries, err := readHistoryFile(filePath, project, sinceTs)
			if err != nil {
				return cmdErr(err)
			}

			if dryRun {
				type dryRunResp struct {
					File       string `json:"file"`
					Total      int    `json:"total"`
					Filtered   int    `json:"filtered"`
					Project    string `json:"project,omitempty"`
					Since      string `json:"since,omitempty"`
					OldestDate string `json:"oldest_date,omitempty"`
					NewestDate string `json:"newest_date,omitempty"`
				}
				resp := dryRunResp{
					File:     filePath,
					Total:    entries.totalRead,
					Filtered: len(entries.items),
					Project:  project,
					Since:    since,
				}
				if len(entries.items) > 0 {
					resp.OldestDate = time.UnixMilli(entries.items[0].Timestamp).Format(time.DateTime)
					resp.NewestDate = time.UnixMilli(entries.items[len(entries.items)-1].Timestamp).Format(time.DateTime)
				}
				return output.PrintSuccess(resp)
			}

			// Ingest into vybe
			var imported, skipped int
			if err := withDB(func(db *DB) error {
				for i := 0; i < len(entries.items); i += batchSize {
					end := i + batchSize
					if end > len(entries.items) {
						end = len(entries.items)
					}
					batch := entries.items[i:end]

					for _, entry := range batch {
						msg := entry.Display
						if len(msg) > store.MaxEventMessageLength {
							msg = msg[:store.MaxEventMessageLength]
						}
						if strings.TrimSpace(msg) == "" {
							skipped++
							continue
						}

						// Deterministic request ID from content hash — safe to re-run.
						requestID := entryRequestID(entry)

						metadata, mErr := json.Marshal(map[string]any{
							"source":     "claude-history",
							"project":    entry.Project,
							"session_id": entry.SessionID,
							"timestamp":  entry.Timestamp,
						})
						if mErr != nil {
							skipped++
							continue
						}

						_, err := store.AppendEventWithProjectAndMetadataIdempotent(
							db, agentName, requestID,
							models.EventKindUserPrompt, entry.Project, "",
							msg, string(metadata),
						)
						if err != nil {
							skipped++
							continue
						}
						imported++
					}
				}
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Imported int    `json:"imported"`
				Skipped  int    `json:"skipped"`
				Total    int    `json:"total"`
				Source   string `json:"source"`
				Project  string `json:"project,omitempty"`
			}
			return output.PrintSuccess(resp{
				Imported: imported,
				Skipped:  skipped,
				Total:    len(entries.items),
				Source:   filePath,
				Project:  project,
			})
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "Path to history.jsonl (default: ~/.claude/history.jsonl)")
	cmd.Flags().StringVar(&project, "project", "", "Filter by project path (substring match)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be imported without writing")
	cmd.Flags().StringVar(&since, "since", "", "Only import entries after this date (YYYY-MM-DD)")
	cmd.Flags().IntVar(&batchSize, "batch-size", 500, "Number of entries per batch")

	return cmd
}

type historyResult struct {
	items     []historyEntry
	totalRead int
}

func readHistoryFile(path, projectFilter string, sinceTs int64) (historyResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return historyResult{}, fmt.Errorf("cannot open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var items []historyEntry
	var totalRead int

	scanner := bufio.NewScanner(f)
	// history.jsonl can have long lines (pasted content)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		totalRead++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry historyEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}

		// Filter by project
		if projectFilter != "" && !strings.Contains(entry.Project, projectFilter) {
			continue
		}

		// Filter by timestamp
		if sinceTs > 0 && entry.Timestamp < sinceTs {
			continue
		}

		// Skip empty/system prompts
		display := strings.TrimSpace(entry.Display)
		if display == "" {
			continue
		}

		items = append(items, entry)
	}

	if err := scanner.Err(); err != nil {
		return historyResult{}, fmt.Errorf("error reading %s: %w", path, err)
	}

	return historyResult{items: items, totalRead: totalRead}, nil
}

// entryRequestID produces a deterministic ID from content, so re-runs are idempotent.
func entryRequestID(e historyEntry) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d|%s|%s", e.Timestamp, e.Project, e.Display)
	return fmt.Sprintf("ingest_%x", h.Sum(nil)[:12])
}
