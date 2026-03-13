package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// prevSessionCache caches readPreviousSessionContext results to avoid redundant disk I/O
// across multiple SessionStart invocations within the same process.
//
//nolint:gochecknoglobals // cache shared across hook invocations; same pattern as hookSeqCounter
var (
	prevSessionCachePath    string
	prevSessionCacheModTime time.Time
	prevSessionCacheResult  string
)

const maxAutoMemoryChars = 2000

type transcriptContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type transcriptMessage struct {
	Role    string                  `json:"role"`
	Content []transcriptContentItem `json:"content"`
}

type transcriptRecord struct {
	Type    string            `json:"type"`
	Message transcriptMessage `json:"message"`
}

// encodeProjectPath converts a filesystem path to the Claude Code project directory
// name format, where each "/" is replaced with "-".
// Example: "/Users/vampire/go/src/vybe" -> "-Users-vampire-go-src-vybe"
func encodeProjectPath(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

// readTailLines reads the last N lines from a file without loading the entire
// file into memory. Seeks to the tail region and scans backward for newlines.
// Falls back to reading the whole file if it's smaller than the tail buffer.
func readTailLines(path string, maxLines int) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is constructed from known home dir + encoded cwd
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	// For small files, just read the whole thing.
	const tailBufSize = 64 * 1024 // 64 KB
	size := info.Size()
	if size == 0 {
		return nil, nil
	}

	offset := int64(0)
	readSize := size
	if size > tailBufSize {
		offset = size - tailBufSize
		readSize = tailBufSize
	}

	buf := make([]byte, readSize)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	buf = buf[:n]

	raw := strings.TrimRight(string(buf), "\n")
	lines := strings.Split(raw, "\n")

	// If we seeked into the middle of the file, the first "line" is likely
	// a partial line - discard it.
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return lines, nil
}

// findMostRecentTranscript scans projectDir for .jsonl transcript files,
// excludes the given session ID, and returns the most recently modified file.
func findMostRecentTranscript(projectDir, excludeSessionID string) (string, time.Time, bool) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", time.Time{}, false
	}

	var bestPath string
	var bestModTime time.Time
	found := false

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".jsonl")
		if sessionID == excludeSessionID {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !found || info.ModTime().After(bestModTime) {
			bestPath = filepath.Join(projectDir, name)
			bestModTime = info.ModTime()
			found = true
		}
	}
	return bestPath, bestModTime, found
}

// parseTranscriptExchanges parses JSONL transcript lines and builds a formatted
// string of user/assistant exchanges, truncating individual messages and total output.
func parseTranscriptExchanges(lines []string, maxMsgLen, maxTotalLen int) string {
	const header = "Previous session context (last session before this one):\n"
	var sb strings.Builder
	sb.WriteString(header)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec transcriptRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "user" && rec.Type != "assistant" {
			continue
		}
		role := rec.Message.Role
		if role == "" {
			role = rec.Type
		}
		var textParts []string
		for _, item := range rec.Message.Content {
			if item.Type != "text" || item.Text == "" {
				continue
			}
			t := item.Text
			if runes := []rune(t); len(runes) > maxMsgLen {
				t = string(runes[:maxMsgLen])
			}
			textParts = append(textParts, t)
		}
		if len(textParts) == 0 {
			continue
		}
		formatted := fmt.Sprintf("  [%s] %s\n", role, strings.Join(textParts, " "))
		if sb.Len()+len(formatted) > maxTotalLen {
			break
		}
		sb.WriteString(formatted)
	}

	result := sb.String()
	if result == header {
		return ""
	}
	return result
}

// readPreviousSessionContext finds the most recent Claude Code session transcript
// for the given working directory (excluding the current session) and returns a
// formatted string of the last few user/assistant exchanges.
//
// All errors are silently swallowed - hooks must never block Claude Code.
func readPreviousSessionContext(cwd, currentSessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	projectDir := filepath.Join(home, ".claude", "projects", encodeProjectPath(cwd))
	path, modTime, ok := findMostRecentTranscript(projectDir, currentSessionID)
	if !ok {
		return ""
	}

	if path == prevSessionCachePath && modTime.Equal(prevSessionCacheModTime) {
		return prevSessionCacheResult
	}

	lines, err := readTailLines(path, 50)
	if err != nil {
		return ""
	}

	result := parseTranscriptExchanges(lines, 200, 2000)

	prevSessionCachePath = path
	prevSessionCacheModTime = modTime
	prevSessionCacheResult = result
	return result
}

func readAutoMemory(cwd string, maxChars int) string {
	if cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".claude", "projects",
		encodeProjectPath(cwd), "memory", "MEMORY.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from known home + encoded cwd
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	if runes := []rune(content); len(runes) > maxChars {
		content = string(runes[:maxChars])
	}
	return content
}
