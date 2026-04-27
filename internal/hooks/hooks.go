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
	workingDir := getWorkingDirFromEnv()
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
	slog.Info("RunStopHook() called")

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
	// Log the raw input to help debug exit_reason issues
	slog.WithFields(logrus.Fields{
		"raw_input_length": len(ctx.RawInput),
		"raw_input":        string(ctx.RawInput),
	}).Info("HookContext initialized with raw input")

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
	}).Info("StopHook invoked")

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
			}).Info("Read session details from filesystem metadata")
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
			}).Info("Session details after daemon lookup")
		}
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

	// Handle session completion
	// Note: session directory is always named with the original Claude UUID (data.SessionID)
	// Session directory path would be: filepath.Join(groveSessionsDir, data.SessionID)

	if isComplete {
		slog.WithFields(logrus.Fields{
			"session_id":    actualSessionID,
			"final_status":  finalStatus,
			"job_file_path": jobFilePath,
		}).Info("Session marked as complete, processing completion actions")

		// If this session is linked to a grove-flow job, automatically complete it
		// Only do this when the session is actually complete, not when it's just going idle
		if jobFilePath != "" {
			slog.WithFields(logrus.Fields{
				"job_file_path": jobFilePath,
			}).Info("Auto-completing linked flow job")
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
				}).Info("Successfully auto-completed flow job")
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
		}).Info("Session set to idle, preserving directory for resumption")

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
				}).Info("Updated job file status to idle")
			}
		}

		// Session is idle - keep directory for later resumption
		// Just send notification
		sendNtfyNotification(ctx, data, "stopped")
	}
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

	// Determine task type
	taskType := determineTaskType(data.SubagentTask)

	// Build result object
	result := map[string]any{
		"status":    data.Status,
		"task_type": taskType,
	}
	if data.Result != nil {
		result["result"] = data.Result
	}
	if data.Error != nil {
		result["error"] = *data.Error
	}

	// Log the event
	eventData := map[string]any{
		"subagent_id":   data.SubagentID,
		"subagent_task": data.SubagentTask,
		"duration_ms":   data.DurationMs,
		"status":        data.Status,
		"task_type":     taskType,
		"result":        result,
	}

	if err := ctx.LogEvent(models.EventSubagentStop, eventData); err != nil {
		log.Printf("Failed to log event: %v", err)
	}

	// For now, we'll just log this as an event
	// In the future, we might want to add a separate subagent tracking table
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
