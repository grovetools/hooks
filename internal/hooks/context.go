package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/process"
	"github.com/mattsolo1/grove-core/pkg/sessions"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-notifications/pkg/config"
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

// getCurrentBranch returns the current git branch name for the given directory
func getCurrentBranch(workingDir string) string {
	cmd := exec.Command("git", "-C", workingDir, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	branch := strings.TrimSpace(string(output))
	if branch == "" {
		return "unknown"
	}
	return branch
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
					// But we need to update the status if it's currently idle (resuming from idle)
					// Read metadata to get the actual session ID (for interactive_agent jobs)
					actualSessionID := sessionID
					if metadataContent, err := os.ReadFile(metadataFile); err == nil {
						var existingMetadata struct {
							SessionID string `json:"session_id"`
						}
						if err := json.Unmarshal(metadataContent, &existingMetadata); err == nil && existingMetadata.SessionID != "" {
							actualSessionID = existingMetadata.SessionID
						}
					}

					// Check current status and update if idle
					if existingSessionData, err := hc.Storage.GetSession(actualSessionID); err == nil && existingSessionData != nil {
						var status string
						if extSession, ok := existingSessionData.(*disk.ExtendedSession); ok {
							status = extSession.Status
						} else if session, ok := existingSessionData.(*models.Session); ok {
							status = session.Status
						}

						if status == "idle" {
							hc.Storage.UpdateSessionStatus(actualSessionID, "running")
						}
					}
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

	// Get project info using grove-core workspace package
	projInfo, err := workspace.GetProjectByPath(workingDir)
	if err != nil {
		// Log the error but continue, as this is for enrichment
		// Note: We don't have a logger here, so we'll just continue silently
	}

	// Determine repo name from WorkspaceNode
	repo := ""
	if projInfo != nil {
		if projInfo.IsWorktree() && projInfo.ParentProjectPath != "" {
			// This is a worktree. The "repo" is its parent project.
			repo = filepath.Base(projInfo.ParentProjectPath)
		} else {
			// Not a worktree, so it is its own repo context.
			repo = projInfo.Name
		}
	}
	if repo == "" {
		// Fallback to directory name
		repo = filepath.Base(workingDir)
	}

	// Get current git branch
	gitBranch := getCurrentBranch(workingDir)

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

	// Get Claude PID
	pid := getClaudePID()

	// Create metadata structure
	now := time.Now()

	// Determine provider from environment, default to "claude"
	provider := os.Getenv("GROVE_AGENT_PROVIDER")
	if provider == "" {
		provider = "claude"
	}

	// Build core session metadata for the registry
	coreMetadata := sessions.SessionMetadata{
		SessionID:        sessionID,
		ClaudeSessionID:  sessionID,
		Provider:         provider,
		PID:              pid,
		Repo:             repo,
		Branch:           gitBranch,
		WorkingDirectory: workingDir,
		User:             username,
		StartedAt:        now,
		TranscriptPath:   transcriptPath,
	}

	// Check for grove-flow integration environment variables
	if flowJobID := os.Getenv("GROVE_FLOW_JOB_ID"); flowJobID != "" {
		coreMetadata.ClaudeSessionID = sessionID // Preserve the original claude_code UUID
		coreMetadata.SessionID = flowJobID       // Use the job ID as the session ID for unification
		coreMetadata.JobTitle = os.Getenv("GROVE_FLOW_JOB_TITLE")
		coreMetadata.PlanName = os.Getenv("GROVE_FLOW_PLAN_NAME")
		coreMetadata.JobFilePath = os.Getenv("GROVE_FLOW_JOB_PATH")
	}

	// Register the session with grove-core registry
	registry, err := sessions.NewFileSystemRegistry()
	if err != nil {
		return fmt.Errorf("failed to create session registry: %w", err)
	}

	if err := registry.Register(coreMetadata); err != nil {
		return fmt.Errorf("failed to register session: %w", err)
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
		Provider: provider,
	}

	// Check for grove-flow integration environment variables
	if flowJobID := os.Getenv("GROVE_FLOW_JOB_ID"); flowJobID != "" {
		session.ClaudeSessionID = sessionID // Preserve the original claude_code UUID
		session.ID = flowJobID              // Use the job ID as the session ID for unification
		session.Type = "interactive_agent"
		session.JobTitle = os.Getenv("GROVE_FLOW_JOB_TITLE")
		session.PlanName = os.Getenv("GROVE_FLOW_PLAN_NAME")
		session.JobFilePath = os.Getenv("GROVE_FLOW_JOB_PATH")
	}

	// Populate workspace context fields if available
	if projInfo != nil {
		session.ProjectName = projInfo.Name
		session.IsWorktree = projInfo.IsWorktree()
		session.IsEcosystem = projInfo.IsEcosystem()
		session.ParentEcosystemPath = projInfo.ParentEcosystemPath
	}

	return hc.Storage.EnsureSessionExists(session)
}

// getClaudePID attempts to find the Claude process PID from the environment.
// It first checks for a CLAUDE_PID environment variable, falling back to the
// parent process ID of the current hook.
func getClaudePID() int {
	// First check if CLAUDE_PID is set in environment
	if pidStr := os.Getenv("CLAUDE_PID"); pidStr != "" {
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			if os.Getenv("GROVE_DEBUG") != "" {
				fmt.Printf("Using CLAUDE_PID from env: %d\n", pid)
			}
			return pid
		}
	}

	// For now, use parent PID as a simple approach
	// In the future, we could use more sophisticated process tree walking
	ppid := os.Getppid()
	if os.Getenv("GROVE_DEBUG") != "" {
		fmt.Printf("Using parent PID: %d (current PID: %d)\n", ppid, os.Getpid())
	}
	return ppid
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
