package store

import (
	"database/sql"
	"encoding/json"
)

func decodeEventMetadata(meta sql.NullString) json.RawMessage {
	if !meta.Valid || meta.String == "" {
		return nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(meta.String), &decoded); err != nil {
		return nil
	}

	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil
	}

	return json.RawMessage(normalized)
}
