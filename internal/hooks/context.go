package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-hooks/internal/config"
	"github.com/mattsolo1/grove-hooks/internal/git"
	"github.com/mattsolo1/grove-hooks/internal/process"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/mattsolo1/grove-tmux/pkg/tmux"
)

// BaseHookInput contains fields common to all hooks
type BaseHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	HookEventName  string `json:"hook_event_name"`
	// Current transcript position (if available)
	CurrentUUID string `json:"current_uuid,omitempty"`
	ParentUUID  string `json:"parent_uuid,omitempty"`
}

// HookContext provides common functionality for all hooks
type HookContext struct {
	Input     BaseHookInput
	RawInput  []byte
	Storage   interfaces.SessionStorer
	StartTime time.Time
	Config    *config.NotificationsConfig
}

// NewHookContext creates a new hook context with local storage
func NewHookContext() (*HookContext, error) {
	// Read stdin
	inputData, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}

	// Parse base input
	var baseInput BaseHookInput
	if err := json.Unmarshal(inputData, &baseInput); err != nil {
		return nil, err
	}

	// Create storage
	storage, err := disk.NewSQLiteStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	// Load configuration
	loadedCfg := config.Load()

	return &HookContext{
		Input:     baseInput,
		RawInput:  inputData,
		Storage:   storage,
		StartTime: time.Now(),
		Config:    loadedCfg,
	}, nil
}

// LogEvent logs an event to local storage
func (hc *HookContext) LogEvent(eventType models.EventType, data map[string]any) error {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	event := &models.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      dataJSON,
		Source:    hc.Input.HookEventName,
		Metadata: models.EventMetadata{
			Version: "1.0",
			Source:  hc.Input.HookEventName,
		},
		TranscriptPath: hc.Input.TranscriptPath,
		TranscriptUUID: hc.Input.CurrentUUID,
		ParentUUID:     hc.Input.ParentUUID,
	}

	return hc.Storage.LogEvent(hc.Input.SessionID, event)
}

// EnsureSessionExists creates a session if it doesn't exist
func (hc *HookContext) EnsureSessionExists(sessionID string, transcriptPath string) error {
	// Create ~/.grove/hooks/sessions directory if it doesn't exist
	groveSessionsDir := expandPath("~/.grove/hooks/sessions")
	if err := os.MkdirAll(groveSessionsDir, 0755); err != nil {
		return fmt.Errorf("failed to create sessions directory: %w", err)
	}

	sessionDir := filepath.Join(groveSessionsDir, sessionID)
	pidFile := filepath.Join(sessionDir, "pid.lock")
	metadataFile := filepath.Join(sessionDir, "metadata.json")

	// Check if session directory already exists
	if _, err := os.Stat(sessionDir); err == nil {
		// Directory exists - check if PID is alive
		if content, err := os.ReadFile(pidFile); err == nil {
			var pid int
			if _, err := fmt.Sscanf(string(content), "%d", &pid); err == nil {
				if process.IsProcessAlive(pid) {
					// Session is already running and tracked
					return nil
				}
			}
		}
		// Stale directory, remove it before creating a new one
		os.RemoveAll(sessionDir)
	}

	// Extract working directory
	workingDir := os.Getenv("PWD")
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}
	if workingDir == "" {
		workingDir = "."
	}

	// Get git info using the centralized utility
	gitInfo := git.GetInfo(workingDir)
	repo := gitInfo.Repository
	gitBranch := gitInfo.Branch

	// Get user info
	username := os.Getenv("USER")
	if username == "" {
		username = "unknown"
	}

	// Get tmux info
	tmuxKey := ""

	// Detect tmux key using tmux manager
	configDir := expandPath("~/.config/tmux-claude-hud")
	tmuxMgr, err := tmux.NewManager(configDir)
	if err == nil && tmuxMgr != nil {
		tmuxKey = tmuxMgr.DetectTmuxKeyForPath(workingDir)
	}

	// Get project info using grove-core workspace package
	projInfo, err := workspace.GetProjectByPath(workingDir)
	if err != nil {
		// Log the error but continue, as this is for enrichment
		// Note: We don't have a logger here, so we'll just continue silently
	}

	// Get Claude PID
	pid := process.GetClaudePID()

	// Create the session directory structure
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// Write the PID lock file
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		return fmt.Errorf("failed to write pid.lock: %w", err)
	}

	// Create metadata structure
	now := time.Now()
	metadata := struct {
		SessionID            string    `json:"session_id"`
		PID                  int       `json:"pid"`
		Repo                 string    `json:"repo,omitempty"`
		Branch               string    `json:"branch,omitempty"`
		TmuxKey              string    `json:"tmux_key,omitempty"`
		WorkingDirectory     string    `json:"working_directory"`
		User                 string    `json:"user"`
		StartedAt            time.Time `json:"started_at"`
		TranscriptPath       string    `json:"transcript_path,omitempty"`
		ProjectName          string    `json:"project_name,omitempty"`
		IsWorktree           bool      `json:"is_worktree,omitempty"`
		ParentEcosystemPath  string    `json:"parent_ecosystem_path,omitempty"`
		Type                 string    `json:"type,omitempty"`
		JobTitle             string    `json:"job_title,omitempty"`
		PlanName             string    `json:"plan_name,omitempty"`
		JobFilePath          string    `json:"job_file_path,omitempty"`
	}{
		SessionID:        sessionID,
		PID:              pid,
		Repo:             repo,
		Branch:           gitBranch,
		TmuxKey:          tmuxKey,
		WorkingDirectory: workingDir,
		User:             username,
		StartedAt:        now,
		TranscriptPath:   transcriptPath,
	}

	// Check for grove-flow integration environment variables
	if flowJobID := os.Getenv("GROVE_FLOW_JOB_ID"); flowJobID != "" {
		metadata.SessionID = flowJobID // Use the job ID as the session ID for unification
		metadata.Type = "interactive_agent"
		metadata.JobTitle = os.Getenv("GROVE_FLOW_JOB_TITLE")
		metadata.PlanName = os.Getenv("GROVE_FLOW_PLAN_NAME")
		metadata.JobFilePath = os.Getenv("GROVE_FLOW_JOB_PATH")
	}

	// Populate workspace context fields if available
	if projInfo != nil {
		metadata.ProjectName = projInfo.Name
		metadata.IsWorktree = projInfo.IsWorktree
		metadata.ParentEcosystemPath = projInfo.ParentEcosystemPath

		// For ecosystem worktrees, use the parent ecosystem name as the repo
		// This ensures consistency with flow job discovery which uses the parent workspace
		if projInfo.IsWorktree && projInfo.IsEcosystem && projInfo.ParentEcosystemPath != "" {
			// Extract the parent ecosystem name from the path
			parentName := filepath.Base(projInfo.ParentEcosystemPath)
			metadata.Repo = parentName
		}
	}

	// Write metadata.json
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataFile, metadataJSON, 0644); err != nil {
		return fmt.Errorf("failed to write metadata.json: %w", err)
	}

	// Also create a DB record for backwards compatibility (will be removed later)
	// This allows existing tools to continue working during the transition
	existingSessionData, err := hc.Storage.GetSession(sessionID)
	if err == nil && existingSessionData != nil {
		// Check the status based on the type
		var status string
		if extSession, ok := existingSessionData.(*disk.ExtendedSession); ok {
			status = extSession.Status
		} else if session, ok := existingSessionData.(*models.Session); ok {
			status = session.Status
		}

		// Session exists - update status if idle
		if status == "idle" {
			return hc.Storage.UpdateSessionStatus(sessionID, "running")
		}
		return nil
	}

	// Create extended session with workspace context
	session := &disk.ExtendedSession{
		Session: models.Session{
			ID:               sessionID,
			PID:              pid,
			Repo:             repo,
			Branch:           gitBranch,
			TmuxKey:          tmuxKey,
			WorkingDirectory: workingDir,
			User:             username,
			Status:           "running",
			StartedAt:        now,
			LastActivity:     now,
			IsTest:           false,
		},
	}

	// Check for grove-flow integration environment variables
	if flowJobID := os.Getenv("GROVE_FLOW_JOB_ID"); flowJobID != "" {
		session.ID = flowJobID // Use the job ID as the session ID for unification
		session.Type = "interactive_agent"
		session.JobTitle = os.Getenv("GROVE_FLOW_JOB_TITLE")
		session.PlanName = os.Getenv("GROVE_FLOW_PLAN_NAME")
		session.JobFilePath = os.Getenv("GROVE_FLOW_JOB_PATH")
	}

	// Populate workspace context fields if available
	if projInfo != nil {
		session.ProjectName = projInfo.Name
		session.IsWorktree = projInfo.IsWorktree
		session.ParentEcosystemPath = projInfo.ParentEcosystemPath
	}

	return hc.Storage.EnsureSessionExists(session)
}

// GetSession retrieves a session from local storage
func (hc *HookContext) GetSession(sessionID string) (*models.Session, error) {
	sessionData, err := hc.Storage.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	// Handle both regular and extended sessions
	if extSession, ok := sessionData.(*disk.ExtendedSession); ok {
		return &extSession.Session, nil
	} else if session, ok := sessionData.(*models.Session); ok {
		return session, nil
	}

	return nil, fmt.Errorf("unexpected session type: %T", sessionData)
}

// expandPath expands ~ to home directory, respecting XDG_DATA_HOME for .grove paths
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		expandedPath := path[2:]

		// If the path is for .grove, respect XDG_DATA_HOME
		if strings.HasPrefix(expandedPath, ".grove/") {
			if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
				// Use XDG_DATA_HOME/... (strip .grove/ prefix since XDG_DATA_HOME already points to .grove)
				return filepath.Join(xdgDataHome, expandedPath[7:]) // Strip ".grove/"
			}
		}

		return filepath.Join(os.Getenv("HOME"), expandedPath)
	}
	return path
}
