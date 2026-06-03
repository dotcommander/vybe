package commands

import (
	"encoding/json"
	"os"
)

// genericResult is vybe-native hook stdout. Empty Context -> emit nothing.
type genericResult struct {
	Context string `json:"context"`
}

func renderGenericResult(res ContextResult) error {
	if res.Context == "" {
		return nil // emit nothing on empty, mirrors Claude's omitempty additionalContext
	}
	return json.NewEncoder(os.Stdout).Encode(genericResult{Context: res.Context})
}
