package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/process"
)

// SessionMetadata represents the metadata.json structure for file-based sessions
type SessionMetadata struct {
	SessionID           string    `json:"session_id"`
	PID                 int       `json:"pid"`
	Repo                string    `json:"repo,omitempty"`
	Branch              string    `json:"branch,omitempty"`
	TmuxKey             string    `json:"tmux_key,omitempty"`
	WorkingDirectory    string    `json:"working_directory"`
	User                string    `json:"user"`
	StartedAt           time.Time `json:"started_at"`
	TranscriptPath      string    `json:"transcript_path,omitempty"`
	ProjectName         string    `json:"project_name,omitempty"`
	IsWorktree          bool      `json:"is_worktree,omitempty"`
	ParentEcosystemPath string    `json:"parent_ecosystem_path,omitempty"`
}

// DiscoverLiveClaudeSessions scans ~/.claude/sessions/ directory and returns live sessions
// A session is considered live if its PID is still alive
func DiscoverLiveClaudeSessions() ([]*models.Session, error) {
	claudeSessionsDir := expandPath("~/.claude/sessions")

	// Check if directory exists
	if _, err := os.Stat(claudeSessionsDir); os.IsNotExist(err) {
		return []*models.Session{}, nil
	}

	entries, err := os.ReadDir(claudeSessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var sessions []*models.Session

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		sessionDir := filepath.Join(claudeSessionsDir, sessionID)
		pidFile := filepath.Join(sessionDir, "pid.lock")
		metadataFile := filepath.Join(sessionDir, "metadata.json")

		// Read PID
		pidContent, err := os.ReadFile(pidFile)
		if err != nil {
			// Skip if we can't read PID file
			continue
		}

		var pid int
		if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err != nil {
			// Skip if PID is invalid
			continue
		}

		// Check if process is alive
		isAlive := process.IsProcessAlive(pid)

		// Read metadata
		metadataContent, err := os.ReadFile(metadataFile)
		if err != nil {
			// Skip if we can't read metadata
			continue
		}

		var metadata SessionMetadata
		if err := json.Unmarshal(metadataContent, &metadata); err != nil {
			// Skip if metadata is invalid
			continue
		}

		// Determine status based on liveness
		status := "running"
		var endedAt *time.Time
		if !isAlive {
			status = "interrupted"
			now := time.Now()
			endedAt = &now
		}

		// Create session object
		session := &models.Session{
			ID:               sessionID,
			PID:              pid,
			Repo:             metadata.Repo,
			Branch:           metadata.Branch,
			TmuxKey:          metadata.TmuxKey,
			WorkingDirectory: metadata.WorkingDirectory,
			User:             metadata.User,
			Status:           status,
			StartedAt:        metadata.StartedAt,
			LastActivity:     metadata.StartedAt, // Use started time as last activity
			EndedAt:          endedAt,
			IsTest:           false,
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}
