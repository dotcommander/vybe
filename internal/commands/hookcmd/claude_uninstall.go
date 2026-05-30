package hookcmd

type claudeUninstallResult struct {
	Path    string   `json:"path"`
	Removed []string `json:"removed"`
}

// uninstallClaudeHooks removes vybe hook entries from the Claude Code settings file.
func uninstallClaudeHooks(projectScoped bool) (*claudeUninstallResult, error) {
	path := resolveClaudeSettingsPath(projectScoped)

	var removed []string
	noHooks := false

	if err := withLockedSettings(path, func(settings map[string]any) error {
		hooksObj, _ := settings["hooks"].(map[string]any)
		if hooksObj == nil {
			noHooks = true
			return errSkipWrite
		}

		for _, eventName := range VybeHookEventNames() {
			entries, ok := hooksObj[eventName].([]any)
			if !ok {
				continue
			}

			kept, removedAny := filterVybeEntries(entries)

			if removedAny {
				removed = append(removed, eventName)
			}

			if len(kept) == 0 {
				delete(hooksObj, eventName)
			} else {
				hooksObj[eventName] = kept
			}
		}

		settings["hooks"] = hooksObj
		return nil
	}); err != nil {
		return nil, err
	}

	if noHooks {
		return &claudeUninstallResult{Path: path, Removed: []string{}}, nil
	}
	return &claudeUninstallResult{Path: path, Removed: removed}, nil
}
