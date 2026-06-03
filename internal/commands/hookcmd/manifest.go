package hookcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const hookManifestFilename = "hooks.json"

// hookManifestVersion is the current on-disk format version.
// Bump when the default event set or schema changes.
// A file missing this version (or with an older value) is treated as stale.
const hookManifestVersion = 2

// hookManifestFile is the on-disk envelope: {"version":N,"hooks":{...}}.
// Separating version from hooks lets callers detect staleness without
// unmarshaling the full entry map.
type hookManifestFile struct {
	Version int                  `json:"version"`
	Hooks   map[string]hookEntry `json:"hooks"`
}

// buildVybeHooks returns the built-in default hook manifest.
// This is the single source of truth for default event names, matchers,
// timeouts, and command templates. Go source never hardcodes these values
// elsewhere — they are always derived from this function or the manifest file.
func buildVybeHooks() map[string]hookEntry {
	return map[string]hookEntry{
		"SessionStart": {
			Matcher: "startup|resume|clear|compact",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("session-start"),
				Timeout: 3000,
			}},
		},
		"UserPromptSubmit": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("prompt"),
				Timeout: 2000,
			}},
		},
		"PostToolUseFailure": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("tool-failure"),
				Timeout: 2000,
			}},
		},
		"PreCompact": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("checkpoint"),
				Timeout: 4000,
			}},
		},
		"SessionEnd": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("session-end"),
				Timeout: 5000,
			}},
		},
		"TaskCompleted": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("task-completed"),
				Timeout: 2000,
			}},
		},
	}
}

// LoadHookManifest is a pure read — it NEVER writes to disk.
//
//   - File absent       → returns built-in defaults in memory (no write).
//   - File present + version == hookManifestVersion → returns file's hooks (preserves customization).
//   - File present + version missing/older (stale) → returns built-in defaults in memory (no write).
//   - File present + malformed JSON → returns a wrapped error.
func LoadHookManifest(configDir string) (map[string]hookEntry, error) {
	manifestPath := filepath.Join(configDir, hookManifestFilename)

	data, err := os.ReadFile(manifestPath)
	if os.IsNotExist(err) {
		return buildVybeHooks(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestPath, err)
	}

	var mf hookManifestFile
	if jsonErr := json.Unmarshal(data, &mf); jsonErr != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestPath, jsonErr)
	}

	// Stale (or pre-versioned) manifest: return built-in defaults without writing.
	if mf.Version < hookManifestVersion {
		return buildVybeHooks(), nil
	}

	return mf.Hooks, nil
}

// LoadOrWriteHookManifest = LoadHookManifest + persist.
// If the file is absent OR carries a stale version, it atomically writes the
// current defaults (with the current version stamp) and returns them.
// If the file is present and current, it returns the file's hooks unchanged.
// Write errors are non-fatal: defaults are returned even when persistence fails.
func LoadOrWriteHookManifest(configDir string) (map[string]hookEntry, error) {
	manifestPath := filepath.Join(configDir, hookManifestFilename)

	data, err := os.ReadFile(manifestPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", manifestPath, err)
	}

	if err == nil {
		// File exists — check version.
		var mf hookManifestFile
		if jsonErr := json.Unmarshal(data, &mf); jsonErr != nil {
			return nil, fmt.Errorf("parse %s: %w", manifestPath, jsonErr)
		}
		if mf.Version >= hookManifestVersion {
			// Current version: preserve customization, no write.
			return mf.Hooks, nil
		}
		// Stale version: fall through to rewrite with defaults.
	}

	// File absent or stale — build defaults and persist them.
	defaults := buildVybeHooks()
	_ = writeHookManifest(manifestPath, defaults) // non-fatal: return defaults even on write failure
	return defaults, nil
}

// writeHookManifest serializes hooks to path using an atomic temp+rename write.
// The envelope carries the current hookManifestVersion so staleness can be detected later.
func writeHookManifest(path string, hooks map[string]hookEntry) error {
	mf := hookManifestFile{Version: hookManifestVersion, Hooks: hooks}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hook manifest: %w", err)
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o600)
}
