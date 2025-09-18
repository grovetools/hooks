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
	"github.com/mattsolo1/grove-hooks/internal/git"
	"github.com/mattsolo1/grove-hooks/internal/process"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/mattsolo1/grove-tmux/pkg/tmux"
	"gopkg.in/yaml.v3"
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

	return &HookContext{
		Input:     baseInput,
		RawInput:  inputData,
		Storage:   storage,
		StartTime: time.Now(),
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
	// Try to get existing session
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
	configDir := expandPath("~/.config/canopy")
	sessionsFile := filepath.Join(configDir, "tmux-sessions.yaml")
	tmuxMgr := tmux.NewManager(configDir, sessionsFile)
	if tmuxMgr != nil {
		tmuxKey = tmuxMgr.DetectTmuxKeyForPath(workingDir)
	}

	// Create session
	now := time.Now()
	session := &models.Session{
		ID:               sessionID,
		PID:              process.GetClaudePID(), // Use parent/Claude PID instead of hook PID
		Repo:             repo,
		Branch:           gitBranch,
		TmuxKey:          tmuxKey,
		WorkingDirectory: workingDir,
		User:             username,
		Status:           "running",
		StartedAt:        now,
		LastActivity:     now,
		IsTest:           false,
	}

	return hc.Storage.EnsureSessionExists(session)
}

// LoadConfig loads the application configuration
func (hc *HookContext) LoadConfig() (map[string]interface{}, error) {
	configPath := expandPath("~/.config/canopy/config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return config, nil
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

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(os.Getenv("HOME"), path[2:])
	}
	return path
}
