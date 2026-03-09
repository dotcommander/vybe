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
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}

	// Collect .jsonl files excluding the current session.
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var candidates []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".jsonl")
		if sessionID == currentSessionID {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, fileInfo{
			path:    filepath.Join(projectDir, name),
			modTime: info.ModTime(),
		})
	}
	if len(candidates) == 0 {
		return ""
	}

	// Pick most recent by ModTime.
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.modTime.After(best.modTime) {
			best = c
		}
	}

	// Cache hit: same file, same modtime - return cached result.
	if best.path == prevSessionCachePath && best.modTime.Equal(prevSessionCacheModTime) {
		return prevSessionCacheResult
	}

	lines, err := readTailLines(best.path, 50)
	if err != nil {
		return ""
	}

	type contentItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string        `json:"role"`
		Content []contentItem `json:"content"`
	}
	type record struct {
		Type    string  `json:"type"`
		Message message `json:"message"`
	}

	const maxMsgLen = 200
	const maxTotalLen = 2000

	var sb strings.Builder
	sb.WriteString("Previous session context (last session before this one):\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec record
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
		line := fmt.Sprintf("  [%s] %s\n", role, strings.Join(textParts, " "))
		if sb.Len()+len(line) > maxTotalLen {
			break
		}
		sb.WriteString(line)
	}

	result := sb.String()
	// If nothing was appended beyond the header, cache and return empty.
	if result == "Previous session context (last session before this one):\n" {
		prevSessionCachePath = best.path
		prevSessionCacheModTime = best.modTime
		prevSessionCacheResult = ""
		return ""
	}

	// Cache the result for subsequent calls.
	prevSessionCachePath = best.path
	prevSessionCacheModTime = best.modTime
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
