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

	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/process"
	"github.com/grovetools/core/pkg/sessions"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/hooks/internal/storage"
	"github.com/grovetools/hooks/internal/utils"
	"github.com/grovetools/nav/pkg/tmux"
)

// BaseHookInput contains fields common to all hooks
type BaseHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	HookEventName  string `json:"hook_event_name"`
	// Current transcript position (if available)
	CurrentUUID string `json:"current_uuid,omitempty"`
	ParentUUID  string `json:"parent_uuid,omitempty"`
	// Cwd is the Claude Code session's working directory as reported by
	// the harness. Used to scope the hook's daemon client to the same
	// ecosystem as the user-facing session, regardless of where the
	// short-lived hook process happened to inherit its cwd from.
	Cwd string `json:"cwd,omitempty"`
}

// HookContext provides common functionality for all hooks
// NotificationsConfig is a placeholder for notification configuration
type NotificationsConfig struct {
	Ntfy struct {
		Enabled bool
		URL     string
		Topic   string
	}
	System struct {
		Levels []string
	}
}

type HookContext struct {
	Input        BaseHookInput
	RawInput     []byte
	Storage      *storage.DaemonBackend
	DaemonClient daemon.Client
	StartTime    time.Time
	Config       *NotificationsConfig
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

	// Create daemon-backed storage. Client inherits GROVE_SCOPE from env.
	backend := storage.NewDaemonBackend()

	// Load configuration (placeholder for now)
	loadedCfg := &NotificationsConfig{}

	return &HookContext{
		Input:        baseInput,
		RawInput:     inputData,
		Storage:      backend,
		DaemonClient: backend.Client(),
		StartTime:    time.Now(),
		Config:       loadedCfg,
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
	// Create sessions directory if it doesn't exist
	groveSessionsDir := filepath.Join(paths.StateDir(), "hooks", "sessions")
	if err := os.MkdirAll(groveSessionsDir, 0755); err != nil {
		return fmt.Errorf("failed to create sessions directory: %w", err)
	}

	sessionDir := filepath.Join(groveSessionsDir, sessionID)
	pidFile := filepath.Join(sessionDir, "pid.lock")
	metadataFile := filepath.Join(sessionDir, "metadata.json")

	// Check if session directory already exists
	if _, err := os.Stat(sessionDir); err == nil {
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

		// Check if daemon knows this session in a non-terminal state.
		// The PID in pid.lock may be stale (short-lived hooks process), but the
		// daemon tracks the session independently. If the daemon says it's idle,
		// transition to running — the agent is resuming after an idle period.
		if existingSessionData, err := hc.Storage.GetSession(actualSessionID); err == nil && existingSessionData != nil {
			if session, ok := existingSessionData.(*models.Session); ok && session != nil {
				if session.Status == "idle" {
					hc.Storage.UpdateSessionStatus(actualSessionID, "running")
				}
				if session.Status == "idle" || session.Status == "running" || session.Status == "pending_user" {
					return nil
				}
			}
		}

		// Also check PID liveness as fallback (for sessions not yet in daemon)
		if content, err := os.ReadFile(pidFile); err == nil {
			var pid int
			if _, err := fmt.Sscanf(string(content), "%d", &pid); err == nil {
				if process.IsProcessAlive(pid) {
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
	configDir := utils.ExpandPath("~/.config/tmux-claude-hud")
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
	flowJobID := os.Getenv("GROVE_FLOW_JOB_ID")
	if flowJobID != "" {
		coreMetadata.ClaudeSessionID = sessionID // Preserve the original claude_code UUID
		coreMetadata.SessionID = flowJobID       // Use the job ID as the session ID for unification
		coreMetadata.JobTitle = os.Getenv("GROVE_FLOW_JOB_TITLE")
		coreMetadata.PlanName = os.Getenv("GROVE_FLOW_PLAN_NAME")
		coreMetadata.JobFilePath = os.Getenv("GROVE_FLOW_JOB_PATH")

		// Note: Session confirmation is handled by flow's discoverAndRegisterSessionAsync.
		// Hooks should NOT call ConfirmSession because each hook runs in a new process
		// with a different PID, which would spam the monitor with false confirmations.
	}

	// Create session with workspace context
	session := &models.Session{
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
		Provider:         provider,
	}

	// Check for grove-flow integration environment variables
	if flowJobID := os.Getenv("GROVE_FLOW_JOB_ID"); flowJobID != "" {
		session.ClaudeSessionID = sessionID // Preserve the original claude_code UUID
		session.ID = flowJobID              // Use the job ID as the session ID for unification
		// Check if this is an isolated agent
		if os.Getenv("GROVE_FLOW_ISOLATED") == "true" {
			session.Type = "isolated_agent"
		} else {
			session.Type = "interactive_agent"
		}
		session.JobTitle = os.Getenv("GROVE_FLOW_JOB_TITLE")
		session.PlanName = os.Getenv("GROVE_FLOW_PLAN_NAME")
		session.JobFilePath = os.Getenv("GROVE_FLOW_JOB_PATH")
	}

	// Register with daemon
	daemonErr := hc.Storage.EnsureSessionExists(session)

	// Always write filesystem registry — aglogs and flow depend on it to find
	// the transcript path (the daemon doesn't store LogFilePath).
	registry, regErr := sessions.NewFileSystemRegistry()
	if regErr != nil {
		if daemonErr != nil {
			return fmt.Errorf("daemon registration failed (%v), and failed to create fallback registry: %w", daemonErr, regErr)
		}
		return fmt.Errorf("failed to create session registry: %w", regErr)
	}
	if regErr := registry.Register(coreMetadata); regErr != nil {
		if daemonErr != nil {
			return fmt.Errorf("daemon registration failed (%v), and fallback registration failed: %w", daemonErr, regErr)
		}
		return fmt.Errorf("failed to register session: %w", regErr)
	}

	return nil
}

// getClaudePID attempts to find the Claude process PID from the environment.
// It first checks for a CLAUDE_PID environment variable, falling back to the
// parent process ID of the current hook.
func getClaudePID() int {
	// First check if CLAUDE_PID is set in environment
	if pidStr := os.Getenv("CLAUDE_PID"); pidStr != "" {
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			if os.Getenv("GROVE_DEBUG") != "" {
				// Debug output - using simple fmt for GROVE_DEBUG mode
				fmt.Printf("Using CLAUDE_PID from env: %d\n", pid)
			}
			return pid
		}
	}

	// For now, use parent PID as a simple approach
	// In the future, we could use more sophisticated process tree walking
	ppid := os.Getppid()
	if os.Getenv("GROVE_DEBUG") != "" {
		// Debug output - using simple fmt for GROVE_DEBUG mode
		fmt.Printf("Using parent PID: %d (current PID: %d)\n", ppid, os.Getpid())
	}
	return ppid
}

// GetSession retrieves a session from the daemon.
func (hc *HookContext) GetSession(sessionID string) (*models.Session, error) {
	sessionData, err := hc.Storage.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	if session, ok := sessionData.(*models.Session); ok {
		return session, nil
	}

	return nil, fmt.Errorf("unexpected session type: %T", sessionData)
}
