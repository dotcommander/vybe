package store

import (
	"database/sql"

	"github.com/dotcommander/vibe/internal/models"
)

// GetAgentState loads agent state by name
func GetAgentState(db *sql.DB, agentName string) (*models.AgentState, error) {
	return LoadOrCreateAgentState(db, agentName)
}
