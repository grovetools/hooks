// Package storage provides the DaemonBackend — a thin wrapper around daemon.Client
// that replaces the SQLite-based session storage. The daemon is the single source of
// truth for session state; event logging (tools, notifications) is best-effort to a
// local JSONL file.
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/paths"
)

// DaemonBackend implements the SessionStorer interface by routing session lifecycle
// operations to the daemon and logging events to a local JSONL file.
type DaemonBackend struct {
	client   daemon.Client
	eventLog string // path to events.jsonl fallback file
}

// NewDaemonBackend creates a new daemon-backed storage instance.
//
// The daemon client inherits GROVE_SCOPE from the parent process
// environment. Claude Code inherits from its launcher (treemux pane, or
// a shell for ad-hoc sessions); treemux sets GROVE_SCOPE on startup
// based on its own launch cwd, flow propagates the plan's scope to
// spawned agents. When Claude Code is launched outside any scope-aware
// host (plain shell, no treemux), GROVE_SCOPE is unset and the hook
// uses the global/unscoped daemon — the right default for ad-hoc use.
func NewDaemonBackend() *DaemonBackend {
	client := daemon.NewWithAutoStart()
	eventLog := filepath.Join(paths.StateDir(), "hooks", "events.jsonl")
	return &DaemonBackend{
		client:   client,
		eventLog: eventLog,
	}
}

// NewDaemonBackendWithClient creates a DaemonBackend with a specific client.
func NewDaemonBackendWithClient(client daemon.Client) *DaemonBackend {
	eventLog := filepath.Join(paths.StateDir(), "hooks", "events.jsonl")
	return &DaemonBackend{
		client:   client,
		eventLog: eventLog,
	}
}

// Client returns the underlying daemon client.
func (b *DaemonBackend) Client() daemon.Client {
	return b.client
}

// Close cleans up the daemon client resources.
func (b *DaemonBackend) Close() error {
	return b.client.Close()
}

// --- Session Management (routed to daemon) ---

// EnsureSessionExists registers the session with the daemon so it can track
// status transitions (idle→running, running→idle, etc.).
// Only registers if the daemon doesn't already know about this session.
func (b *DaemonBackend) EnsureSessionExists(session interface{}) error {
	s, ok := session.(*models.Session)
	if !ok || s == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Skip if daemon already has this session (avoid re-registration with wrong PIDs)
	if existing, _ := b.client.GetSession(ctx, s.ID); existing != nil {
		return nil
	}

	// Register intent only — do NOT call ConfirmSession with PID here.
	// The hooks process PID (from getClaudePID/os.Getppid) is the short-lived
	// grove meta-tool PID, not the actual Claude Code PID. If we register that
	// PID, the SessionCollector will immediately mark it as dead/interrupted.
	// The actual Claude PID will be discovered and confirmed by flow's executor
	// (discoverAndRegisterSessionAsync) which has access to the tmux pane PID.
	return b.client.RegisterSessionIntent(ctx, daemon.SessionIntent{
		JobID:       s.ID,
		Provider:    s.Provider,
		JobFilePath: s.JobFilePath,
		PlanName:    s.PlanName,
		Title:       s.JobTitle,
		WorkDir:     s.WorkingDirectory,
	})
}

// GetSession retrieves a session by ID from the daemon.
func (b *DaemonBackend) GetSession(sessionID string) (interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := b.client.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// GetAllSessions retrieves all sessions from the daemon.
func (b *DaemonBackend) GetAllSessions() ([]*models.Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return b.client.GetSessions(ctx)
}

// UpdateSessionStatus updates the status of a session via the daemon.
func (b *DaemonBackend) UpdateSessionStatus(sessionID, status string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Terminal statuses use EndSession
	if status == "completed" || status == "failed" || status == "error" || status == "interrupted" {
		return b.client.EndSession(ctx, sessionID, status)
	}

	return b.client.UpdateSessionStatus(ctx, sessionID, status)
}

// --- Event Logging (JSONL fallback) ---

// LogToolUsage logs a tool execution event to the local JSONL file.
func (b *DaemonBackend) LogToolUsage(sessionID string, tool *models.ToolExecution) error {
	return b.appendEvent("tool_usage", sessionID, tool)
}

// UpdateToolExecution updates a tool execution record. Best-effort JSONL append.
func (b *DaemonBackend) UpdateToolExecution(sessionID, toolID string, update *models.ToolExecution) error {
	return b.appendEvent("tool_update", sessionID, map[string]interface{}{
		"tool_id": toolID,
		"update":  update,
	})
}

// GetToolExecution is not supported by the daemon backend.
func (b *DaemonBackend) GetToolExecution(sessionID, toolID string) (*models.ToolExecution, error) {
	return nil, fmt.Errorf("tool execution lookup not supported (daemon backend)")
}

// LogNotification logs a notification to the local JSONL file.
func (b *DaemonBackend) LogNotification(sessionID string, notification *models.ClaudeNotification) error {
	return b.appendEvent("notification", sessionID, notification)
}

// LogEvent logs an event to the local JSONL file.
func (b *DaemonBackend) LogEvent(sessionID string, event *models.Event) error {
	return b.appendEvent("event", sessionID, event)
}

// ArchiveSessions is a no-op. The daemon handles retention via cleanup_after config.
func (b *DaemonBackend) ArchiveSessions(sessionIDs []string) error {
	// No-op: daemon handles cleanup via cleanup_after configuration
	return nil
}

// appendEvent writes a structured event to the local JSONL file.
func (b *DaemonBackend) appendEvent(eventType, sessionID string, data interface{}) error {
	entry := map[string]interface{}{
		"type":       eventType,
		"session_id": sessionID,
		"timestamp":  time.Now().Format(time.RFC3339),
		"data":       data,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return nil // Best-effort, don't fail hooks for logging errors
	}

	// Ensure directory exists
	dir := filepath.Dir(b.eventLog)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil // Best-effort
	}

	f, err := os.OpenFile(b.eventLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil // Best-effort
	}
	defer f.Close()

	_, _ = f.Write(append(line, '\n'))
	return nil // Best-effort
}
