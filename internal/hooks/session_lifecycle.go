package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/paths"
	"github.com/sirupsen/logrus"
)

// Provider-integration lifecycle hooks.
//
// Claude Code drives the seven native hook events; other providers bridge
// their own lifecycle into the same Go pipeline via `grove hooks <event>`
// shell-outs (the opencode plugin and pi extension are the callers). Two
// events exist beyond the native set because those integrations need them:
//
//   - session-status: a non-terminal status transition (busy/running, idle,
//     pending_user) reported by the provider, e.g. opencode's session.status
//     event or a throttled tool.execute.before activity ping.
//   - session-end: the provider destroyed the session (opencode's
//     session.deleted). Terminal — unlike the Stop hook this also cleans up
//     the filesystem registry entry, because the provider-side session (and
//     its transcript fragments) are gone.

// NormalizeProviderSessionStatus maps a provider-reported status to the
// session-store vocabulary. Provider activity states (opencode "busy",
// "retry") normalize to "running"; already-canonical statuses pass through.
// Unknown statuses are rejected so integrations can't write arbitrary
// strings into the session store.
func NormalizeProviderSessionStatus(raw string) (string, bool) {
	switch raw {
	case "busy", "retry", "running":
		return "running", true
	case "idle":
		return "idle", true
	case "pending_user":
		return "pending_user", true
	default:
		return "", false
	}
}

// resolveRegisteredSessionID maps a native provider session id (the registry
// directory name) to the actual session id recorded in metadata.json — the
// flow job id for flow-managed sessions. Mirrors the Stop pipeline's
// resolution. Falls back to the given id when no metadata exists.
func resolveRegisteredSessionID(sessionID string) string {
	if sessionID == "" {
		return sessionID
	}
	metadataFile := filepath.Join(paths.StateDir(), "hooks", "sessions", sessionID, "metadata.json")
	content, err := os.ReadFile(metadataFile)
	if err != nil {
		return sessionID
	}
	var metadata struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(content, &metadata); err != nil || metadata.SessionID == "" {
		return sessionID
	}
	return metadata.SessionID
}

// RunSessionStatusHook handles the session-status hook: a provider
// integration reporting a non-terminal status transition.
func RunSessionStatusHook() {
	slog := logging.NewLogger("hooks.session-status")

	ctx, err := NewHookContext()
	if err != nil {
		slog.WithError(err).Error("Error initializing hook context")
		os.Exit(1)
	}

	var data SessionStatusInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		slog.WithError(err).Error("Error parsing JSON")
		os.Exit(1)
	}

	status, ok := NormalizeProviderSessionStatus(data.Status)
	if !ok {
		slog.WithFields(logrus.Fields{
			"session_id": data.SessionID,
			"status":     data.Status,
		}).Warn("Ignoring unrecognized provider session status")
		return
	}

	actualSessionID := resolveRegisteredSessionID(data.SessionID)

	if err := ctx.LogEvent(models.EventType("session_status"), map[string]any{
		"session_id": actualSessionID,
		"status":     status,
		"raw_status": data.Status,
	}); err != nil {
		slog.WithError(err).Debug("Failed to log session_status event")
	}

	if err := ctx.Storage.UpdateSessionStatus(actualSessionID, status); err != nil {
		slog.WithFields(logrus.Fields{
			"session_id": actualSessionID,
			"status":     status,
			"error":      err.Error(),
		}).Warn("Failed to update session status")
		return
	}

	slog.WithFields(logrus.Fields{
		"session_id": actualSessionID,
		"directory":  data.SessionID,
		"status":     status,
	}).Info("Session status updated")
}

// RunSessionEndHook handles the session-end hook: a provider integration
// reporting that the session was destroyed on the provider side. The session
// is marked completed in the daemon and its filesystem registry entry is
// removed (parity with the pre-v2 opencode plugin, which deleted the entry on
// session.deleted — there is no transcript left to archive once opencode
// drops the session).
func RunSessionEndHook() {
	slog := logging.NewLogger("hooks.session-end")

	ctx, err := NewHookContext()
	if err != nil {
		slog.WithError(err).Error("Error initializing hook context")
		os.Exit(1)
	}

	var data SessionEndInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		slog.WithError(err).Error("Error parsing JSON")
		os.Exit(1)
	}

	if data.SessionID == "" {
		slog.Warn("session-end called without session_id")
		return
	}

	actualSessionID := resolveRegisteredSessionID(data.SessionID)

	if err := ctx.LogEvent(models.EventType("session_end"), map[string]any{
		"session_id": actualSessionID,
		"reason":     data.Reason,
	}); err != nil {
		slog.WithError(err).Debug("Failed to log session_end event")
	}

	// "completed" is terminal: DaemonBackend routes it to EndSession.
	if err := ctx.Storage.UpdateSessionStatus(actualSessionID, "completed"); err != nil {
		slog.WithFields(logrus.Fields{
			"session_id": actualSessionID,
			"error":      err.Error(),
		}).Warn("Failed to mark session completed")
	}

	// Remove the registry directory (named by the native provider session
	// id). Deliberate divergence from the Stop pipeline, which preserves the
	// directory for transcript archiving: session-end means the provider
	// deleted the session, so nothing remains to archive.
	sessionDir := filepath.Join(paths.StateDir(), "hooks", "sessions", data.SessionID)
	if err := os.RemoveAll(sessionDir); err != nil {
		slog.WithFields(logrus.Fields{
			"session_dir": sessionDir,
			"error":       err.Error(),
		}).Warn("Failed to remove session directory")
	}

	slog.WithFields(logrus.Fields{
		"session_id": actualSessionID,
		"directory":  data.SessionID,
		"reason":     data.Reason,
	}).Info("Session ended and registry entry removed")
}
