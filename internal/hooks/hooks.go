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

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/api"
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
	if shouldSendSystemNotification(data) {
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
	ctx, err := NewHookContext()
	if err != nil {
		log.Printf("Error initializing hook context: %v", err)
		os.Exit(1)
	}

	var data StopInput
	if err := json.Unmarshal(ctx.RawInput, &data); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		os.Exit(1)
	}

	// Get session details to obtain working directory
	session, err := ctx.GetSession(data.SessionID)
	if err != nil {
		log.Printf("Failed to get session details: %v", err)
	} else if session != nil && session.WorkingDirectory != "" {
		// Execute repository-specific hook commands
		log.Printf("Checking for .canopy.yaml in working directory: %s", session.WorkingDirectory)
		if err := ExecuteRepoHookCommands(ctx, session.WorkingDirectory); err != nil {
			// Check if this is a blocking error from exit code 2
			if blockingErr, ok := err.(*api.HookBlockingError); ok {
				log.Printf("Hook command returned blocking error: %s", blockingErr.Message)
				// Write the error message to stderr and exit with code 2
				fmt.Fprintf(os.Stderr, "%s\n", blockingErr.Message)
				os.Exit(2)
			}
			log.Printf("Failed to execute repo hook commands: %v", err)
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

	// Update session status based on exit reason
	// Mark as completed for actual completion or errors
	// Otherwise, set to idle (for normal end-of-turn stops)
	if data.ExitReason == "completed" || data.ExitReason == "error" || data.ExitReason == "interrupted" || data.ExitReason == "killed" {
		if err := ctx.Storage.UpdateSessionStatus(data.SessionID, "completed"); err != nil {
			log.Printf("Failed to complete session: %v", err)
		}

		// Send ntfy notification for completed sessions
		sendNtfyNotification(ctx, data, "completed")
	} else {
		// Normal end-of-turn stop (empty exit_reason or other) - set to idle
		if err := ctx.Storage.UpdateSessionStatus(data.SessionID, "idle"); err != nil {
			log.Printf("Failed to update session status to idle: %v", err)
		}

		// Send ntfy notification for normal session stops too
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

// ExecuteRepoHookCommands executes on_stop commands from .canopy.yaml
func ExecuteRepoHookCommands(hc *HookContext, workingDir string) error {
	config, err := LoadRepoHookConfig(workingDir)
	if err != nil {
		return fmt.Errorf("failed to load repo hook config: %w", err)
	}

	if config == nil || len(config.Hooks.OnStop) == 0 {
		// No commands to execute
		return nil
	}

	log.Printf("Found %d on_stop commands in .canopy.yaml", len(config.Hooks.OnStop))

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
			if blockingErr, ok := err.(*api.HookBlockingError); ok {
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

// LoadRepoHookConfig loads .canopy.yaml from the specified directory
func LoadRepoHookConfig(workingDir string) (*models.RepoHookConfig, error) {
	configPath := filepath.Join(workingDir, ".canopy.yaml")

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, nil // No config file found, not an error
	}

	// Read and parse the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read .canopy.yaml: %w", err)
	}

	var config models.RepoHookConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .canopy.yaml: %w", err)
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