package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// defaultTriggerWords is the built-in trigger set. It is written to
// triggers.yaml on first run and used as the fallback when the file is
// missing or unreadable, so behavior is identical with or without the file.
//
//nolint:gochecknoglobals // static default data; read-only after init
var defaultTriggerWords = []string{
	"brief me",
	"remember",
	"remember?",
	"brief",
	"what's pending",
	"status",
}

// triggersFile is the YAML schema for triggers.yaml.
type triggersFile struct {
	Triggers []string `yaml:"triggers"`
}

const defaultTriggers = `# vybe hook trigger words
# When a user prompt (lowercased, trimmed) exactly matches one of these,
# the prompt hook emits a rich vybe brief instead of the normal flow.
triggers:
  - "brief me"
  - "remember"
  - "remember?"
  - "brief"
  - "what's pending"
  - "status"
`

// triggersOnce, triggerWords implement the sync.Once lazy-load singleton,
// matching the settings.go pattern.
//
//nolint:gochecknoglobals // sync.Once singleton is intentional process-wide state
var (
	triggersOnce sync.Once
	triggerWords map[string]struct{}
)

// ResetTriggerWordsForTest resets the trigger singleton so the next
// LoadTriggerWords call re-reads from disk. Call only in tests via t.Cleanup.
func ResetTriggerWordsForTest() {
	triggersOnce = sync.Once{}
	triggerWords = nil
}

// LoadTriggerWords loads the trigger-word set once from
// ~/.config/vybe/triggers.yaml. If the file is missing or cannot be parsed,
// it falls back to the built-in defaults so callers always get a usable set.
func LoadTriggerWords() map[string]struct{} {
	triggersOnce.Do(func() {
		triggerWords = triggerSetFromSlice(defaultTriggerWords)

		dir, err := ConfigDir()
		if err != nil {
			return
		}
		b, err := os.ReadFile(filepath.Join(dir, "triggers.yaml"))
		if err != nil {
			return
		}
		var tf triggersFile
		if err := yaml.Unmarshal(b, &tf); err != nil {
			return
		}
		if len(tf.Triggers) > 0 {
			triggerWords = triggerSetFromSlice(tf.Triggers)
		}
	})
	return triggerWords
}

func triggerSetFromSlice(words []string) map[string]struct{} {
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[strings.ToLower(strings.TrimSpace(w))] = struct{}{}
	}
	return m
}
