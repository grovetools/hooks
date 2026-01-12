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

	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/utils"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
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
}

func RunStopHook() {
	slog := logging.NewLogger("hooks.stop")
	slog.Info("RunStopHook() called")

	// Write to a known debug file for troubleshooting
	debugFile, _ := os.OpenFile(os.ExpandEnv("$HOME/.grove/hooks-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
	groveSessionsDir := utils.ExpandPath("~/.grove/hooks/sessions")
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

	// Then try SQLite as fallback/supplement (for sessions not registered by grove-flow)
	// Only override values that weren't set from filesystem metadata
	sessionData, err := ctx.Storage.GetSession(actualSessionID)
	if err != nil {
		slog.WithFields(logrus.Fields{
			"session_id": actualSessionID,
			"error":      err.Error(),
		}).Debug("SQLite session lookup failed (may be expected for grove-flow sessions)")
	} else if sessionData != nil {
		// Check if it's an extended session with a type
		if extSession, ok := sessionData.(*disk.ExtendedSession); ok {
			// Only override if not already set from filesystem
			if sessionType == "claude_session" && extSession.Type != "" {
				sessionType = extSession.Type
			}
			if workingDir == "" {
				workingDir = extSession.WorkingDirectory
			}
			if jobFilePath == "" {
				jobFilePath = extSession.JobFilePath
			}
			if provider == "" {
				provider = extSession.Provider
			}

			slog.WithFields(logrus.Fields{
				"session_id":    actualSessionID,
				"session_type":  sessionType,
				"provider":      provider,
				"job_file_path": jobFilePath,
				"working_dir":   workingDir,
			}).Info("Session details after SQLite lookup")
		} else if session, ok := sessionData.(*models.Session); ok {
			if workingDir == "" {
				workingDir = session.WorkingDirectory
			}
			slog.WithFields(logrus.Fields{
				"session_id":   actualSessionID,
				"session_type": sessionType,
				"working_dir":  workingDir,
			}).Info("Session details retrieved (basic session)")
		}

		// Execute repository-specific hook commands if we have a working directory
		if workingDir != "" {
			log.Printf("Checking for .grove-hooks.yaml in working directory: %s", workingDir)
			if err := ExecuteRepoHookCommands(ctx, workingDir); err != nil {
				// Check if this is a blocking error from exit code 2
				if blockingErr, ok := err.(*HookBlockingError); ok {
					log.Printf("Hook command returned blocking error: %s", blockingErr.Message)
					// Write the error message to stderr and exit with code 2
					fmt.Fprintf(os.Stderr, "%s\n", blockingErr.Message)
					os.Exit(2)
				}
				log.Printf("Failed to execute repo hook commands: %v", err)
			}
		}
	}

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
	finalStatus := "idle"
	isComplete := false

	// Log the critical decision inputs
	slog.WithFields(logrus.Fields{
		"session_type":      sessionType,
		"provider":          provider,
		"exit_reason":       data.ExitReason,
		"actual_session_id": actualSessionID,
	}).Info("Determining session status - decision inputs")

	// Debug file for troubleshooting
	if debugFile, err := os.OpenFile(os.ExpandEnv("$HOME/.grove/hooks-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		fmt.Fprintf(debugFile, "[%s] DECISION: session_type=%q provider=%q exit_reason=%q session_id=%q\n",
			time.Now().Format(time.RFC3339), sessionType, provider, data.ExitReason, actualSessionID)
		debugFile.Close()
	}

	if sessionType == "oneshot_job" {
		// For oneshot jobs, always mark as completed when stop hook is called
		finalStatus = "completed"
		isComplete = true
		slog.WithFields(logrus.Fields{
			"session_type": sessionType,
			"final_status": finalStatus,
			"is_complete":  isComplete,
			"reason":       "oneshot_job always completes on stop",
		}).Info("Status decision: oneshot job completed")
	} else if provider == "opencode" {
		// OpenCode sessions (both standalone and grove-flow managed) stay running after each turn.
		// The stop hook is triggered at the end of each assistant response, but the session
		// is NOT actually complete - it's just idle waiting for the next user message.
		//
		// IMPORTANT: Never auto-complete opencode sessions from the stop hook.
		// The user must explicitly complete them via TUI 'c' key or `flow plan complete`.
		// Only mark as failed if there's an actual error.
		if data.ExitReason == "error" || data.ExitReason == "killed" || data.ExitReason == "interrupted" {
			finalStatus = "failed"
			isComplete = true
			slog.WithFields(logrus.Fields{
				"session_type": sessionType,
				"provider":     provider,
				"exit_reason":  data.ExitReason,
				"final_status": finalStatus,
				"is_complete":  isComplete,
				"reason":       "opencode session failed",
			}).Info("Status decision: opencode session failed")
		} else {
			// For opencode, exit_reason "completed" just means the assistant finished responding.
			// The opencode process itself is still running, waiting for user input.
			// Set to idle, NOT complete. User must explicitly complete via TUI or CLI.
			finalStatus = "idle"
			isComplete = false
			slog.WithFields(logrus.Fields{
				"session_type": sessionType,
				"provider":     provider,
				"exit_reason":  data.ExitReason,
				"final_status": finalStatus,
				"is_complete":  isComplete,
				"reason":       "opencode end-of-turn, keeping idle (explicit completion required)",
			}).Info("Status decision: opencode session idle (end-of-turn)")
		}
	} else {
		// For regular claude/codex sessions, use exit reason to determine status
		if data.ExitReason == "completed" || data.ExitReason == "error" || data.ExitReason == "interrupted" || data.ExitReason == "killed" {
			finalStatus = "completed"
			isComplete = true
			slog.WithFields(logrus.Fields{
				"session_type": sessionType,
				"provider":     provider,
				"exit_reason":  data.ExitReason,
				"final_status": finalStatus,
				"is_complete":  isComplete,
				"reason":       "terminal exit reason",
			}).Info("Status decision: session completed")
		} else {
			// Normal end-of-turn stop (empty exit_reason or other) - set to idle
			finalStatus = "idle"
			slog.WithFields(logrus.Fields{
				"session_type": sessionType,
				"provider":     provider,
				"exit_reason":  data.ExitReason,
				"final_status": finalStatus,
				"is_complete":  isComplete,
				"reason":       "non-terminal exit reason",
			}).Info("Status decision: session idle")
		}
	}

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
			cmd := exec.Command("grove", "flow", "plan", "complete", jobFilePath)
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

// ExecuteRepoHookCommands executes on_stop commands from .grove-hooks.yaml
func ExecuteRepoHookCommands(hc *HookContext, workingDir string) error {
	config, err := LoadRepoHookConfig(workingDir)
	if err != nil {
		return fmt.Errorf("failed to load repo hook config: %w", err)
	}

	if config == nil || len(config.Hooks.OnStop) == 0 {
		// No commands to execute
		return nil
	}

	log.Printf("Found %d on_stop commands in .grove-hooks.yaml", len(config.Hooks.OnStop))

	for i, hookCmd := range config.Hooks.OnStop {
		log.Printf("Executing hook command %d: %s", i+1, hookCmd.Name)

		// Check run_if condition
		if hookCmd.RunIf == "changes" {
			hasChanges, err := hasGitChanges(workingDir)
			if err != nil {
				log.Printf("Failed to check git changes for command '%s': %v", hookCmd.Name, err)
				continue
			}
			if !hasChanges {
				log.Printf("Skipping command '%s' - no git changes detected", hookCmd.Name)
				continue
			}
		}

		// Execute the command
		if err := ExecuteHookCommand(workingDir, hookCmd); err != nil {
			log.Printf("Hook command '%s' failed: %v", hookCmd.Name, err)

			// Check if this is a blocking error (exit code 2)
			if blockingErr, ok := err.(*HookBlockingError); ok {
				log.Printf("Hook command '%s' returned blocking error, stopping session", hookCmd.Name)

				// Log event for blocking command
				eventData := map[string]any{
					"hook_command": hookCmd.Name,
					"command":      hookCmd.Command,
					"success":      false,
					"error":        blockingErr.Message,
					"blocking":     true,
				}
				if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
					log.Printf("Failed to log hook command blocking failure: %v", logErr)
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
				log.Printf("Failed to log hook command failure: %v", logErr)
			}

			// Continue with other commands for non-blocking errors
		} else {
			log.Printf("Hook command '%s' completed successfully", hookCmd.Name)

			// Log event for successful command
			eventData := map[string]any{
				"hook_command": hookCmd.Name,
				"command":      hookCmd.Command,
				"success":      true,
				"blocking":     false,
			}
			if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
				log.Printf("Failed to log hook command success: %v", logErr)
			}
		}
	}

	return nil
}

// LoadRepoHookConfig loads .grove-hooks.yaml from the specified directory
func LoadRepoHookConfig(workingDir string) (*models.RepoHookConfig, error) {
	configPath := filepath.Join(workingDir, ".grove-hooks.yaml")

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, nil // No config file found, not an error
	}

	// Read and parse the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read .grove-hooks.yaml: %w", err)
	}

	var config models.RepoHookConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .grove-hooks.yaml: %w", err)
	}

	return &config, nil
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
