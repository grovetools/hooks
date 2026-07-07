package hooks

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/errors"
	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/util/delegation"
	"github.com/sirupsen/logrus"
)

// Hook implementations

func RunNotificationHook() {
	ctx, err := NewHookContext()
	if err != nil {
		log.Printf("Error initializing hook context: %v", err)
		os.Exit(1)
	}

	var data NotificationInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		os.Exit(1)
	}

	// Log the event
	eventData := map[string]any{
		"notification_type": data.Type,
		"message":           data.Message,
		"level":             data.Level,
	}

	if err := ctx.LogEvent(models.EventTypeNotification, eventData); err != nil {
		log.Printf("Failed to log event: %v", err)
	}

	// Send system notification if appropriate
	if shouldSendSystemNotification(ctx, data) {
		if sendSystemNotification(data) {
			data.SystemNotificationSent = true
		}
	}

	// Log notification to storage
	notification := &models.ClaudeNotification{
		Type:                   data.Type,
		Message:                data.Message,
		Level:                  data.Level,
		SystemNotificationSent: data.SystemNotificationSent,
		Timestamp:              time.Now(),
	}

	if err := ctx.Storage.LogNotification(data.SessionID, notification); err != nil {
		log.Printf("Failed to log notification: %v", err)
	}

	// If this notification indicates the agent is blocked waiting on the user
	// (permission prompt, plan approval, or AskUserQuestion survey), mark the
	// session pending_user so flow surfaces it instead of "running". This is an
	// independent concern from OS notifications, so it is NOT gated behind
	// shouldSendSystemNotification. The next PreToolUse flips it back to running
	// (see EnsureSessionExists), and the Stop hook handles genuine turn-end.
	if isWaitingNotification(data.Message) {
		// For interactive_agent sessions the directory is named with the Claude
		// UUID, but the real session_id is the flow job ID. Mirror the Stop
		// hook: read metadata.json to resolve the actual session id.
		actualSessionID := data.SessionID
		metadataFile := filepath.Join(paths.StateDir(), "hooks", "sessions", data.SessionID, "metadata.json")
		if metadataContent, err := os.ReadFile(metadataFile); err == nil {
			var metadata struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(metadataContent, &metadata); err == nil && metadata.SessionID != "" {
				actualSessionID = metadata.SessionID
			}
		}

		slog := logging.NewLogger("hooks.notification")
		if err := ctx.Storage.UpdateSessionStatus(actualSessionID, "pending_user"); err != nil {
			slog.WithFields(logrus.Fields{
				"session_id": actualSessionID,
				"message":    data.Message,
				"error":      err.Error(),
			}).Warn("Failed to set session status to pending_user")
		} else {
			slog.WithFields(logrus.Fields{
				"session_id": actualSessionID,
				"message":    data.Message,
			}).Debug("Set session status to pending_user (agent waiting on user)")
		}

		// Push an ntfy notification so the user knows an agent is blocked
		// waiting on them (the "waiting-on-you" half of the notify scope).
		sendWaitingNtfyNotification(ctx, actualSessionID, data.Message)
	}
}

func RunPreToolUseHook() {
	ctx, err := NewHookContext()
	if err != nil {
		log.Printf("Error initializing hook context: %v", err)
		os.Exit(1)
	}

	var data PreToolUseInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		os.Exit(1)
	}

	// Ensure session exists
	if err := ctx.EnsureSessionExists(data.SessionID, data.TranscriptPath); err != nil {
		log.Printf("Failed to ensure session exists: %v", err)
	}

	// Get working directory
	workingDir := ""
	if envVar, ok := data.ToolInput["__working_directory"].(string); ok && envVar != "" {
		workingDir = envVar
	}

	// Apply tool-specific validation
	response := validateTool(data.ToolName, data.ToolInput, workingDir)

	// Log the event
	eventData := map[string]any{
		"tool_name":      data.ToolName,
		"tool_input":     data.ToolInput,
		"approved":       response.Approved,
		"blocked_reason": response.Message,
		"working_dir":    workingDir,
	}

	if err := ctx.LogEvent(models.EventPreToolUse, eventData); err != nil {
		log.Printf("Failed to log event: %v", err)
	}

	// Record the Bash command attempt to commands.jsonl. Pre rows are written
	// as "pending"; the viewer marks them "blocked" when no matching post row
	// arrives (e.g. the command was denied or blocked before execution). The
	// link id bridges this pre row to its post row — Claude does not send a
	// tool_use_id at PreToolUse, so the recorder generates and stashes its own.
	if data.ToolName == "Bash" {
		preCwd := data.Cwd
		if preCwd == "" {
			preCwd = workingDir
		}
		linkID := newCommandLinkID(data.SessionID)
		storeCommandLinkID(data.SessionID, linkID)
		if entry, ok := buildPreCommandEntry(data.ToolName, data.ToolInput, linkID, preCwd, time.Now()); ok {
			appendCommandEntries(data.SessionID, []commandEntry{entry})
		}
	}

	// Create tool execution record if approved
	var toolID string
	if response.Approved {
		// Generate a simple tool ID
		toolID = fmt.Sprintf("%s_%d", data.SessionID, time.Now().UnixNano())

		// Use the tool input as parameters
		args := data.ToolInput

		tool := &models.ToolExecution{
			ID:            toolID,
			SessionID:     data.SessionID,
			ToolName:      data.ToolName,
			Parameters:    args,
			Approved:      response.Approved,
			BlockedReason: response.Message,
			StartedAt:     time.Now(),
		}

		if err := ctx.Storage.LogToolUsage(data.SessionID, tool); err != nil {
			log.Printf("Failed to log tool usage: %v", err)
		} else {
			storeToolID(data.SessionID, toolID)
		}
	}

	// Return response
	responseJSON, _ := json.Marshal(response)
	fmt.Print(string(responseJSON))
}

func RunPostToolUseHook() {
	ctx, err := NewHookContext()
	if err != nil {
		log.Printf("Error initializing hook context: %v", err)
		os.Exit(1)
	}

	var data PostToolUseInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		os.Exit(1)
	}

	// Log the event
	eventData := map[string]any{
		"tool_name":     data.ToolName,
		"tool_input":    data.ToolInput,
		"tool_response": data.ToolResponse,
		"duration_ms":   data.ToolDurationMs,
		"tool_use_id":   data.ToolUseID,
		"success":       data.ToolError == nil,
	}

	if data.ToolError != nil {
		eventData["error"] = *data.ToolError
	}

	if err := ctx.LogEvent(models.EventPostToolUse, eventData); err != nil {
		log.Printf("Failed to log event: %v", err)
	}

	// Record the Bash command outcome to commands.jsonl. The link id (stashed at
	// PreToolUse) bridges this post row to its pre row; clear the slot afterward.
	if data.ToolName == "Bash" {
		linkID := getCommandLinkID(data.SessionID)
		if entry, ok := buildPostCommandEntry(data, linkID, time.Now()); ok {
			appendCommandEntries(data.SessionID, []commandEntry{entry})
		}
		clearCommandLinkID(data.SessionID)
	}

	// Handle ExitPlanMode - save Claude plans to grove-flow
	if data.ToolName == "ExitPlanMode" {
		if err := HandleExitPlanMode(ctx, data); err != nil {
			// Log but don't fail the hook - plan preservation is best-effort
			log.Printf("Failed to preserve plan: %v", err)
		}
	}

	// Handle Edit - sync plan edits to grove-flow when editing Claude plan files
	if data.ToolName == "Edit" {
		if err := HandlePlanEdit(ctx, data); err != nil {
			// Log but don't fail the hook - plan sync is best-effort
			log.Printf("Failed to sync plan edit: %v", err)
		}
	}

	// Get stored tool ID and update completion
	if toolID := getStoredToolID(data.SessionID); toolID != "" {
		success := data.ToolError == nil
		resultSummary := buildResultSummary(data)

		// Stream file access events to JSONL for context tracking
		appendFileAccessEntries(data.SessionID, resultSummary)

		errorMsg := ""
		if data.ToolError != nil {
			errorMsg = *data.ToolError
		}

		completedAt := time.Now()
		durationMs := data.ToolDurationMs
		update := &models.ToolExecution{
			Success:     &success,
			DurationMs:  &durationMs,
			Error:       errorMsg,
			CompletedAt: &completedAt,
		}

		// Convert result summary to ToolResultSummary
		if resultMap, ok := resultSummary["modified_files"].([]string); ok {
			summary := &models.ToolResultSummary{
				ModifiedFiles: resultMap,
			}
			if files, ok := resultSummary["files_read"].([]string); ok {
				summary.FilesRead = files
			}
			update.ResultSummary = summary
		}

		if err := ctx.Storage.UpdateToolExecution(data.SessionID, toolID, update); err != nil {
			log.Printf("Failed to update tool execution: %v", err)
		}

		cleanupToolID(data.SessionID)
	}

	dispatchPostToolUseReminders(data)
}

// dispatchPostToolUseReminders loads PostToolUse reminder hooks from the
// repo's grove.toml and emits a combined additionalContext JSON response
// when one or more entries match the current tool call.
func dispatchPostToolUseReminders(data PostToolUseInput) {
	workingDir := data.Cwd
	if workingDir == "" {
		workingDir = os.Getenv("PWD")
	}
	if workingDir == "" {
		return
	}

	cfg, err := config.LoadFrom(workingDir)
	if err != nil {
		return
	}

	var hooksConfig config.HooksConfig
	if err := cfg.UnmarshalExtension("hooks", &hooksConfig); err != nil {
		return
	}
	if len(hooksConfig.PostToolUse) == 0 {
		return
	}

	toolInput, _ := data.ToolInput.(map[string]any)
	if toolInput == nil {
		return
	}

	var contexts []string
	var matchedNames []string
	for _, entry := range hooksConfig.PostToolUse {
		if entry.If == "" || entry.AdditionalContext == "" {
			continue
		}
		if !evaluatePermissionRule(entry.If, data.ToolName, toolInput) {
			continue
		}
		contexts = append(contexts, entry.AdditionalContext)
		matchedNames = append(matchedNames, entry.Name)
	}

	if len(contexts) == 0 {
		return
	}

	logPostToolUseReminders(data, matchedNames)

	response := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "PostToolUse",
			"additionalContext": strings.Join(contexts, "\n\n---\n\n"),
		},
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return
	}
	fmt.Print(string(payload))
}

// logPostToolUseReminders appends one line per matched reminder to the
// per-session post_tool_use.log under the grove hooks state dir.
func logPostToolUseReminders(data PostToolUseInput, matched []string) {
	if data.SessionID == "" {
		return
	}
	dir := filepath.Join(paths.StateDir(), "hooks", "sessions", data.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "post_tool_use.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	summary := ""
	if b, err := json.Marshal(data.ToolInput); err == nil {
		summary = string(b)
		if len(summary) > 80 {
			summary = summary[:80]
		}
	}
	ts := time.Now().Format(time.RFC3339)
	for _, name := range matched {
		fmt.Fprintf(f, "[%s] hook=%s tool=%s input_summary=%s\n", ts, name, data.ToolName, summary)
	}
}

// fileAccessEntry represents a single file access event for JSONL streaming.
type fileAccessEntry struct {
	Timestamp string `json:"timestamp"`
	Tool      string `json:"tool"`
	Path      string `json:"path"`
	Action    string `json:"action"`
}

// appendFileAccessEntries streams file read/modify events to an append-only JSONL file
// at .artifacts/<job-name>/accessed_files.jsonl within the active plan directory.
func appendFileAccessEntries(sessionID string, resultSummary map[string]any) {
	var entries []fileAccessEntry
	toolName, _ := resultSummary["tool_name"].(string)
	now := time.Now().Format(time.RFC3339)

	if files, ok := resultSummary["files_read"].([]string); ok {
		for _, f := range files {
			entries = append(entries, fileAccessEntry{Timestamp: now, Tool: toolName, Path: f, Action: "read"})
		}
	}
	if files, ok := resultSummary["modified_files"].([]string); ok {
		for _, f := range files {
			entries = append(entries, fileAccessEntry{Timestamp: now, Tool: toolName, Path: f, Action: "modified"})
		}
	}

	if len(entries) == 0 {
		return
	}

	planDir, jobName := resolveFileAccessTarget(sessionID)
	if planDir == "" {
		return
	}

	artifactsDir := filepath.Join(planDir, ".artifacts", jobName)
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return
	}

	jsonlPath := filepath.Join(artifactsDir, "accessed_files.jsonl")
	f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		_, _ = f.Write(line)
		_, _ = f.Write([]byte("\n"))
	}
}

// resolveFileAccessTarget determines the plan directory and job name for storing
// file access entries. It first checks session metadata (fast path for flow-launched
// sessions), then falls back to plan directory resolution.
func resolveFileAccessTarget(sessionID string) (planDir, jobName string) {
	// Fast path: read session metadata to get job file path directly
	groveSessionsDir := filepath.Join(paths.StateDir(), "hooks", "sessions")
	metadataFile := filepath.Join(groveSessionsDir, sessionID, "metadata.json")

	if metadataContent, err := os.ReadFile(metadataFile); err == nil {
		var metadata struct {
			SessionID   string `json:"session_id"`
			JobFilePath string `json:"job_file_path"`
		}
		if err := json.Unmarshal(metadataContent, &metadata); err == nil && metadata.JobFilePath != "" {
			dir := filepath.Dir(metadata.JobFilePath)
			name := metadata.SessionID
			if name == "" {
				name = sessionID
			}
			if _, err := os.Stat(dir); err == nil {
				return dir, name
			}
		}
	}

	// Slow path: resolve via plan preservation config + flow plan current
	workingDir := resolveWorkingDir("")
	if workingDir == "" {
		return "", ""
	}

	preservationConfig := loadPlanPreservationConfig(workingDir)
	dir, err := findActivePlanDir(workingDir, preservationConfig)
	if err != nil {
		return "", ""
	}

	return dir, sessionID
}

func RunStopHook() {
	slog := logging.NewLogger("hooks.stop")
	slog.Debug("RunStopHook() called")

	// Write to a known debug file for troubleshooting
	debugFile, _ := os.OpenFile(os.ExpandEnv("$HOME/.grove/hooks-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if debugFile != nil {
		defer debugFile.Close()
		fmt.Fprintf(debugFile, "[%s] RunStopHook called\n", time.Now().Format(time.RFC3339))
	}

	ctx, err := NewHookContext()
	if err != nil {
		slog.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Error("Error initializing hook context")
		os.Exit(1)
	}
	runStopPipeline(ctx, slog)
}

// RunStopHookWithInput runs the Stop pipeline with an explicit payload instead
// of reading stdin. Codex's notify hook delivers its event as a process
// argument (`hooks codex notify <json>`), which the codex command translates
// into a StopInput and feeds through here.
func RunStopHookWithInput(rawInput []byte) {
	slog := logging.NewLogger("hooks.stop")
	slog.Info("RunStopHookWithInput() called")

	ctx, err := NewHookContextFromInput(rawInput)
	if err != nil {
		slog.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Error("Error initializing hook context")
		os.Exit(1)
	}
	runStopPipeline(ctx, slog)
}

// runStopPipeline is the shared body of the Stop hook: it resolves the
// session, determines the outcome, and applies status/completion side effects.
func runStopPipeline(ctx *HookContext, slog *logrus.Entry) {
	// Log the raw input to help debug exit_reason issues
	slog.WithFields(logrus.Fields{
		"raw_input_length": len(ctx.RawInput),
	}).Debug("HookContext initialized with raw input")

	var data StopInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		slog.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Error("Error parsing JSON")
		os.Exit(1)
	}

	slog.WithFields(logrus.Fields{
		"session_id":  data.SessionID,
		"exit_reason": data.ExitReason,
		"duration_ms": data.DurationMs,
	}).Debug("StopHook invoked")

	// For interactive_agent sessions, the session directory is named with the Claude UUID,
	// but the actual session_id is the flow job ID. Read metadata to get the correct ID.
	// Also read provider and type from filesystem metadata since SQLite may not have grove-flow sessions.
	actualSessionID := data.SessionID
	groveSessionsDir := filepath.Join(paths.StateDir(), "hooks", "sessions")
	metadataFile := filepath.Join(groveSessionsDir, data.SessionID, "metadata.json")

	// Initialize with defaults
	var sessionType string = "claude_session"
	var workingDir string
	var jobFilePath string
	var provider string

	// First, try to read from filesystem metadata (grove-flow sessions)
	if metadataContent, err := os.ReadFile(metadataFile); err == nil {
		var metadata struct {
			SessionID        string `json:"session_id"`
			Provider         string `json:"provider"`
			Type             string `json:"type"`
			WorkingDirectory string `json:"working_directory"`
			JobFilePath      string `json:"job_file_path"`
		}
		if err := json.Unmarshal(metadataContent, &metadata); err == nil {
			if metadata.SessionID != "" {
				actualSessionID = metadata.SessionID
			}
			if metadata.Provider != "" {
				provider = metadata.Provider
			}
			if metadata.Type != "" {
				sessionType = metadata.Type
			}
			if metadata.WorkingDirectory != "" {
				workingDir = metadata.WorkingDirectory
			}
			if metadata.JobFilePath != "" {
				jobFilePath = metadata.JobFilePath
			}
			slog.WithFields(logrus.Fields{
				"actual_session_id": actualSessionID,
				"directory":         data.SessionID,
				"provider":          provider,
				"session_type":      sessionType,
			}).Debug("Read session details from filesystem metadata")
		}
	}

	// Try daemon as supplement for session metadata not set from filesystem
	sessionData, err := ctx.Storage.GetSession(actualSessionID)
	if err != nil {
		slog.WithFields(logrus.Fields{
			"session_id": actualSessionID,
			"error":      err.Error(),
		}).Debug("Daemon session lookup failed (may be expected for new sessions)")
	}

	// Supplement metadata from daemon if available (but don't require it)
	if sessionData != nil {
		if session, ok := sessionData.(*models.Session); ok && session != nil {
			// Only override if not already set from filesystem
			if sessionType == "claude_session" && session.Type != "" {
				sessionType = session.Type
			}
			if workingDir == "" {
				workingDir = session.WorkingDirectory
			}
			if jobFilePath == "" {
				jobFilePath = session.JobFilePath
			}
			if provider == "" && session.Provider != "" {
				provider = session.Provider
			}

			slog.WithFields(logrus.Fields{
				"session_id":    actualSessionID,
				"session_type":  sessionType,
				"provider":      provider,
				"job_file_path": jobFilePath,
				"working_dir":   workingDir,
			}).Debug("Session details after daemon lookup")
		}
	}

	// Fallback: the stop/notify process runs inside the agent's process tree
	// and inherits its environment; flow exports GROVE_AGENT_PROVIDER for
	// non-claude providers (codex/opencode). This keeps provider-specific
	// outcome handling live even when neither filesystem metadata nor the
	// daemon knows the session (e.g. a manually launched codex session).
	if provider == "" {
		provider = os.Getenv("GROVE_AGENT_PROVIDER")
	}

	// Fallback to cwd from stop input if we don't have a working directory from metadata
	if workingDir == "" && data.Cwd != "" {
		workingDir = data.Cwd
		slog.WithFields(logrus.Fields{
			"working_dir": workingDir,
		}).Debug("Using cwd from stop input as working directory")
	}

	// on_stop hooks are dispatched by `grove hooks stop-async` (registered as a
	// separate Stop entry in .claude/settings.json with asyncRewake: true).
	// The sync stop hook is now pure bookkeeping; ExecuteRepoHookCommands is
	// retained for safety during rollout but no longer invoked here.
	_ = workingDir

	// Generate session summary
	summary := generateSessionSummary(data)

	// Log the event
	eventData := map[string]any{
		"exit_reason": data.ExitReason,
		"duration_ms": data.DurationMs,
		"summary":     summary,
	}

	if err := ctx.LogEvent(models.EventStop, eventData); err != nil {
		log.Printf("Failed to log event: %v", err)
	}

	// Determine final status based on exit reason and session type
	outcome := DetermineOutcome(StopContext{
		SessionType: sessionType,
		Provider:    provider,
		ExitReason:  data.ExitReason,
	})
	finalStatus := outcome.Status
	isComplete := outcome.IsComplete

	slog.WithFields(logrus.Fields{
		"event":             "session.stopped",
		"session_type":      sessionType,
		"provider":          provider,
		"exit_reason":       data.ExitReason,
		"actual_session_id": actualSessionID,
		"final_status":      finalStatus,
		"is_complete":       isComplete,
	}).Info("Session outcome determined")

	// Update database status using the actual session ID
	if err := ctx.Storage.UpdateSessionStatus(actualSessionID, finalStatus); err != nil {
		log.Printf("Failed to update session status: %v", err)
	}

	// Headless agents own their frontmatter exclusively through flow's
	// finalizer, so the Stop hook must NEVER park them at idle. This dedicated
	// branch (A2/A7) handles everything for headless: it writes a fallback
	// .status when the launcher's is absent (covering `flow plan run --local`,
	// where the launcher goroutine dies with the CLI), and for clean exits execs
	// `flow plan complete` for timely completion. It fully supersedes the
	// generic isComplete/idle logic below.
	if sessionType == "headless_agent" {
		runHeadlessStopActions(ctx, slog, data, groveSessionsDir, jobFilePath, actualSessionID, finalStatus)
		return
	}

	// Handle session completion
	// Note: session directory is always named with the original Claude UUID (data.SessionID)
	// Session directory path would be: filepath.Join(groveSessionsDir, data.SessionID)

	if isComplete {
		slog.WithFields(logrus.Fields{
			"session_id":    actualSessionID,
			"final_status":  finalStatus,
			"job_file_path": jobFilePath,
		}).Debug("Session marked as complete, processing completion actions")

		// If this session is linked to a grove-flow job, automatically complete it
		// Only do this when the session is actually complete, not when it's just going idle
		if jobFilePath != "" {
			slog.WithFields(logrus.Fields{
				"job_file_path": jobFilePath,
			}).Debug("Auto-completing linked flow job")
			cmd := delegation.Command("flow", "plan", "complete", jobFilePath)
			if output, err := cmd.CombinedOutput(); err != nil {
				// This isn't a fatal error for the hook itself, so just log it.
				// The command might fail if the job is already complete, which is fine.
				slog.WithFields(logrus.Fields{
					"job_file_path": jobFilePath,
					"error":         err.Error(),
					"output":        string(output),
				}).Warn("Failed to auto-complete flow job (may be expected)")
			} else {
				slog.WithFields(logrus.Fields{
					"job_file_path": jobFilePath,
				}).Debug("Successfully auto-completed flow job")
			}
		}

		// Session is complete - archive metadata to DB
		// The metadata has already been written to the DB by previous hooks
		// -----------------------------------------------------------------------
		// DELETION LOGIC REMOVED TO PREVENT RACE CONDITION WITH TRANSCRIPT ARCHIVING
		// Cleanup is now handled by 'grove-hooks sessions cleanup'
		// -----------------------------------------------------------------------
		// Persist the completed status to metadata.json so RecoverSessions
		// returns the terminal status instead of defaulting to "running"
		// when it sees the (still-alive parent) PID in pid.lock. The
		// directory and pid.lock are intentionally preserved — cleanup is
		// handled by `grove-hooks sessions cleanup`, and removing pid.lock
		// here would make RecoverSessions skip the entry entirely instead
		// of reporting it as completed.
		metadataPath := filepath.Join(groveSessionsDir, data.SessionID, "metadata.json")
		if metadataContent, err := os.ReadFile(metadataPath); err == nil {
			var metadata map[string]any
			if json.Unmarshal(metadataContent, &metadata) == nil {
				metadata["status"] = finalStatus
				if updated, err := json.MarshalIndent(metadata, "", "  "); err == nil {
					_ = os.WriteFile(metadataPath, updated, 0o644)
				}
			}
		}

		slog.WithFields(logrus.Fields{
			"session_id":        data.SessionID,
			"actual_session_id": actualSessionID,
		}).Debug("Session directory preserved for transcript archiving")

		// Send ntfy notification for completed sessions
		sendNtfyNotification(ctx, data, "completed")
	} else {
		slog.WithFields(logrus.Fields{
			"session_id":   actualSessionID,
			"final_status": finalStatus,
		}).Debug("Session set to idle, preserving directory for resumption")

		// Update the job file status to idle if this is a grove-flow managed session
		if jobFilePath != "" && finalStatus == "idle" {
			if err := updateJobFileStatus(jobFilePath, "idle"); err != nil {
				slog.WithFields(logrus.Fields{
					"job_file_path": jobFilePath,
					"error":         err.Error(),
				}).Warn("Failed to update job file status to idle")
			} else {
				slog.WithFields(logrus.Fields{
					"job_file_path": jobFilePath,
				}).Debug("Updated job file status to idle")
			}
		}

		// Session is idle - keep directory for later resumption.
		// Deliberately NO ntfy push here: the Stop hook fires at the end of
		// every turn, so pushing on idle would notify on every turn-end. ntfy
		// is scoped to (a) genuine completion (above) and (b) waiting-on-you,
		// which the Notification hook handles.
	}
}

// runHeadlessStopActions handles the Stop hook for headless_agent sessions
// (A2/A7). Frontmatter status for headless jobs is owned exclusively by flow's
// FinalizeHeadlessJob, so this NEVER writes the job file's status — in
// particular it never parks it at idle. Instead it:
//   - writes a fallback .status file when the launcher's is absent (the launcher
//     goroutine dies with the CLI under `flow plan run --local`), so adoption's
//     next boot sweep can reconcile the job to a terminal state;
//   - execs `flow plan complete` for clean exits so the job completes promptly
//     without waiting for a daemon boot (failure cases are left to adoption);
//   - persists the terminal session status to metadata.json for RecoverSessions
//     and pushes the completion ntfy — the same session bookkeeping the generic
//     complete branch performs.
func runHeadlessStopActions(ctx *HookContext, slog *logrus.Entry, data StopInput, groveSessionsDir, jobFilePath, actualSessionID, finalStatus string) {
	// Map the outcome to a .status exit code: clean end → 0, failure → 1.
	exitCode := 0
	if finalStatus != "completed" {
		exitCode = 1
	}

	// A7: fallback .status writer. Never overwrite an existing file — the
	// launcher's cmd.Wait() exit code is higher-fidelity truth.
	writeHeadlessFallbackStatus(slog, jobFilePath, actualSessionID, exitCode)

	slog.WithFields(logrus.Fields{
		"session_id":    actualSessionID,
		"final_status":  finalStatus,
		"job_file_path": jobFilePath,
	}).Debug("Headless stop: frontmatter owned by flow finalizer; not writing job status")

	// For clean exits, complete the flow job now (finalizer owns the frontmatter
	// write; `flow plan complete` routes through CompleteJob and tolerates an
	// already-terminal job). Failure cases leave frontmatter to adoption's sweep.
	if jobFilePath != "" && finalStatus == "completed" {
		cmd := delegation.Command("flow", "plan", "complete", jobFilePath)
		if output, err := cmd.CombinedOutput(); err != nil {
			slog.WithFields(logrus.Fields{
				"job_file_path": jobFilePath,
				"error":         err.Error(),
				"output":        string(output),
			}).Warn("Headless stop: failed to auto-complete flow job (may be expected)")
		} else {
			slog.WithFields(logrus.Fields{
				"job_file_path": jobFilePath,
			}).Debug("Headless stop: auto-completed flow job")
		}
	}

	// Persist the terminal session status into metadata.json so RecoverSessions
	// reports it instead of defaulting to running off the still-alive parent PID.
	metadataPath := filepath.Join(groveSessionsDir, data.SessionID, "metadata.json")
	if metadataContent, err := os.ReadFile(metadataPath); err == nil {
		var metadata map[string]any
		if json.Unmarshal(metadataContent, &metadata) == nil {
			metadata["status"] = finalStatus
			if updated, err := json.MarshalIndent(metadata, "", "  "); err == nil {
				_ = os.WriteFile(metadataPath, updated, 0o644)
			}
		}
	}

	// Push the completion ntfy (same as the generic complete branch).
	sendNtfyNotification(ctx, data, finalStatus)
}

// writeHeadlessFallbackStatus writes the .artifacts/<jobID>/.status file a
// headless agent's launcher normally writes, for the case where the launcher
// process died before it could (e.g. `flow plan run --local`). The schema and
// path match flow's headlessStatusPath / waitAndWriteStatus writer and the
// daemon adoption reader exactly. It NEVER overwrites an existing file.
func writeHeadlessFallbackStatus(slog *logrus.Entry, jobFilePath, jobID string, exitCode int) {
	if jobFilePath == "" || jobID == "" {
		return
	}
	planDir := filepath.Dir(jobFilePath)
	statusPath := filepath.Join(planDir, ".artifacts", jobID, ".status")

	if _, err := os.Stat(statusPath); err == nil {
		// Launcher already wrote it (higher-fidelity); leave it.
		return
	}

	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		slog.WithFields(logrus.Fields{"path": statusPath, "error": err.Error()}).
			Warn("Headless stop: failed to create .status dir")
		return
	}

	payload := map[string]any{
		"exit_code": exitCode,
		"timestamp": time.Now().Format(time.RFC3339),
		"job_id":    jobID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := os.WriteFile(statusPath, data, 0o644); err != nil {
		slog.WithFields(logrus.Fields{"path": statusPath, "error": err.Error()}).
			Warn("Headless stop: failed to write fallback .status")
		return
	}
	slog.WithFields(logrus.Fields{"path": statusPath, "exit_code": exitCode}).
		Debug("Headless stop: wrote fallback .status")
}

// RunSessionStartHook handles the SessionStart hook. It registers the
// session immediately — closing the window where sessions only existed after
// the first PreToolUse (the empty-registry bug class) — and records the
// transcript path before any tool runs. SessionStart supports a response
// contract (additionalContext), so stdout must stay pristine: this handler
// writes nothing to stdout.
func RunSessionStartHook() {
	ctx, err := NewHookContext()
	if err != nil {
		log.Printf("Error initializing hook context: %v", err)
		os.Exit(1)
	}

	var data SessionStartInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		os.Exit(1)
	}

	if err := ctx.EnsureSessionExists(data.SessionID, data.TranscriptPath); err != nil {
		log.Printf("Failed to ensure session exists: %v", err)
	}
}

// RunSubagentStartHook handles the SubagentStart hook (fires for both
// workflow-spawned agents and Agent-tool spawns; CC v2.1.172 probe). It logs
// a 'subagentstart' event mirroring the stop handler and forwards an
// agent_started workflow event to the daemon, best-effort.
func RunSubagentStartHook() {
	ctx, err := NewHookContext()
	if err != nil {
		log.Printf("Error initializing hook context: %v", err)
		os.Exit(1)
	}

	var data SubagentStartInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		os.Exit(1)
	}

	eventData := map[string]any{
		"agent_id":   data.AgentID,
		"agent_type": data.AgentType,
	}
	if err := ctx.LogEvent(models.EventType("subagentstart"), eventData); err != nil {
		log.Printf("Failed to log event: %v", err)
	}

	// Suppress phantom type-registration events at the source. The harness
	// fires SubagentStart once per registered agent definition (Explore, Plan)
	// at session init with a short, non-spawn agent_id and no transcript;
	// forwarding those would create phantom idle ad-hoc subagents in the
	// daemon. Only forward genuine Task/Agent spawns.
	if !shouldForwardSubagentStart(data) {
		return
	}

	ev := workflowEventFromSubagentStart(data, time.Now())
	forwardWorkflowEvent(ctx.DaemonClient, forwardingWorkingDir(data.Cwd), ev)
}

func RunSubagentStopHook() {
	ctx, err := NewHookContext()
	if err != nil {
		log.Printf("Error initializing hook context: %v", err)
		os.Exit(1)
	}

	var data SubagentStopInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		os.Exit(1)
	}

	// Prefer the real agent_id over the legacy subagent_id (which real
	// payloads have never been observed to carry).
	agentID := data.AgentID
	if agentID == "" {
		agentID = data.SubagentID
	}

	eventData := map[string]any{
		"agent_id":         agentID,
		"agent_type":       data.AgentType,
		"stop_hook_active": data.StopHookActive,
	}
	if data.AgentTranscriptPath != nil {
		eventData["agent_transcript_path"] = *data.AgentTranscriptPath
	}
	if data.LastAssistantMessage != nil {
		eventData["last_assistant_message"] = *data.LastAssistantMessage
	}
	if len(data.BackgroundTasks) > 0 {
		eventData["background_tasks"] = data.BackgroundTasks
		// Workflow name attribution (background_tasks[] entries with
		// type == "workflow" carry the workflow name; CC v2.1.172 probe).
		if name := extractWorkflowName(data.BackgroundTasks); name != "" {
			eventData["workflow_name"] = name
		}
	}
	if len(data.SessionCrons) > 0 {
		eventData["session_crons"] = data.SessionCrons
	}

	// Legacy fields: only recorded when an old-format payload supplies them.
	if data.SubagentTask != "" {
		eventData["subagent_task"] = data.SubagentTask
		eventData["task_type"] = determineTaskType(data.SubagentTask)
	}
	if data.DurationMs != 0 {
		eventData["duration_ms"] = data.DurationMs
	}
	if data.Status != "" {
		eventData["status"] = data.Status
	}
	if data.Result != nil {
		eventData["result"] = data.Result
	}
	if data.Error != nil {
		eventData["error"] = *data.Error
	}

	if err := ctx.LogEvent(models.EventSubagentStop, eventData); err != nil {
		log.Printf("Failed to log event: %v", err)
	}

	// Forward an agent_completed workflow event to the daemon, best-effort.
	// RunID comes from the wf_<runId> dir embedded in agent_transcript_path
	// (empty RunID = ad-hoc Agent-tool spawn). Phantom workflow-wait stops
	// (the main session's turn boundaries while a background Workflow runs) are
	// filtered out so they never mint ad-hoc agent rows. The raw event was
	// already recorded above for audit; only the daemon forward is gated.
	if shouldForwardSubagentStop(data) {
		ev := workflowEventFromSubagentStop(data, time.Now())
		forwardWorkflowEvent(ctx.DaemonClient, forwardingWorkingDir(data.Cwd), ev)
	}
}

// ExecuteRepoHookCommands executes on_stop commands from grove.yml
func ExecuteRepoHookCommands(hc *HookContext, workingDir string) error {
	slog := logging.NewLogger("hooks.repo")

	cfg, err := config.LoadFrom(workingDir)
	if err != nil {
		// Ignore ConfigNotFound errors - proceed without hooks
		if errors.GetCode(err) == errors.ErrCodeConfigNotFound {
			return nil
		}
		return fmt.Errorf("failed to load grove config: %w", err)
	}

	// Unmarshal hooks from extensions
	var hooksConfig config.HooksConfig
	if err := cfg.UnmarshalExtension("hooks", &hooksConfig); err != nil {
		slog.WithFields(logrus.Fields{
			"working_dir": workingDir,
			"error":       err.Error(),
		}).Warn("Failed to unmarshal hooks config")
		return nil
	}

	if len(hooksConfig.OnStop) == 0 {
		return nil
	}

	slog.WithFields(logrus.Fields{
		"count":       len(hooksConfig.OnStop),
		"working_dir": workingDir,
	}).Info("Found on_stop commands in grove.yml")

	for _, hookCmd := range hooksConfig.OnStop {
		// Check run_if condition
		if hookCmd.RunIf == "changes" {
			hasChanges, err := hasGitChanges(workingDir)
			if err != nil {
				slog.WithFields(logrus.Fields{
					"name":  hookCmd.Name,
					"error": err.Error(),
				}).Warn("Failed to check git changes, skipping command")
				continue
			}
			if !hasChanges {
				slog.WithFields(logrus.Fields{
					"name": hookCmd.Name,
				}).Debug("Skipping command - no git changes detected")
				continue
			}
		}

		// Execute the command
		if err := ExecuteHookCommand(workingDir, hookCmd); err != nil {
			slog.WithFields(logrus.Fields{
				"name":  hookCmd.Name,
				"error": err.Error(),
			}).Error("Hook command failed")

			// Check if this is a blocking error (exit code 2)
			if blockingErr, ok := err.(*HookBlockingError); ok {
				slog.WithFields(logrus.Fields{
					"name":    hookCmd.Name,
					"message": blockingErr.Message,
				}).Error("Hook command returned blocking error, stopping session")

				// Log event for blocking command
				eventData := map[string]any{
					"hook_command": hookCmd.Name,
					"command":      hookCmd.Command,
					"success":      false,
					"error":        blockingErr.Message,
					"blocking":     true,
				}
				if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
					slog.WithFields(logrus.Fields{"error": logErr.Error()}).Warn("Failed to log hook command blocking failure")
				}

				// Return the blocking error to prevent session stop
				return blockingErr
			}

			// Log event for non-blocking failed command
			eventData := map[string]any{
				"hook_command": hookCmd.Name,
				"command":      hookCmd.Command,
				"success":      false,
				"error":        err.Error(),
				"blocking":     false,
			}
			if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
				slog.WithFields(logrus.Fields{"error": logErr.Error()}).Warn("Failed to log hook command failure")
			}

			// Continue with other commands for non-blocking errors
		} else {
			slog.WithFields(logrus.Fields{
				"name": hookCmd.Name,
			}).Info("Hook command completed successfully")

			// Log event for successful command
			eventData := map[string]any{
				"hook_command": hookCmd.Name,
				"command":      hookCmd.Command,
				"success":      true,
				"blocking":     false,
			}
			if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
				slog.WithFields(logrus.Fields{"error": logErr.Error()}).Warn("Failed to log hook command success")
			}
		}
	}

	return nil
}

// hasGitChanges checks if there are any git changes in the working directory
func hasGitChanges(workingDir string) (bool, error) {
	// Check for staged changes
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = workingDir
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return true, nil // Changes detected
		}
		return false, fmt.Errorf("git diff --cached failed: %w", err)
	}

	// Check for unstaged changes
	cmd = exec.Command("git", "diff", "--quiet")
	cmd.Dir = workingDir
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return true, nil // Changes detected
		}
		return false, fmt.Errorf("git diff failed: %w", err)
	}

	// Check for untracked files
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = workingDir
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files failed: %w", err)
	}

	return len(strings.TrimSpace(string(output))) > 0, nil
}
