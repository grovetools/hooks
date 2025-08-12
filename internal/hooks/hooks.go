package hooks

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-hooks/internal/api"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-notifications"
)

// Common types used by hooks

type NotificationInput struct {
	SessionID              string `json:"session_id"`
	TranscriptPath         string `json:"transcript_path"`
	HookEventName          string `json:"hook_event_name"`
	Type                   string `json:"type"`
	Message                string `json:"message"`
	Level                  string `json:"level"` // info, warning, error
	SystemNotificationSent bool   `json:"system_notification_sent"`
	CurrentUUID            string `json:"current_uuid,omitempty"`
	ParentUUID             string `json:"parent_uuid,omitempty"`
}

type PreToolUseInput struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	HookEventName  string         `json:"hook_event_name"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	CurrentUUID    string         `json:"current_uuid,omitempty"`
	ParentUUID     string         `json:"parent_uuid,omitempty"`
}

type PostToolUseInput struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath string  `json:"transcript_path"`
	HookEventName  string  `json:"hook_event_name"`
	ToolName       string  `json:"tool_name"`
	ToolInput      any     `json:"tool_input"`
	ToolResponse   any     `json:"tool_response"`
	ToolOutput     any     `json:"tool_output"` // Legacy field
	ToolDurationMs int64   `json:"tool_duration_ms"`
	ToolError      *string `json:"tool_error"`
	ToolUseID      string  `json:"tool_use_id,omitempty"`
	CurrentUUID    string  `json:"current_uuid,omitempty"`
	ParentUUID     string  `json:"parent_uuid,omitempty"`
}

type StopInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	ExitReason     string `json:"exit_reason"`
	DurationMs     int64  `json:"duration_ms"`
	CurrentUUID    string `json:"current_uuid,omitempty"`
	ParentUUID     string `json:"parent_uuid,omitempty"`
}

type SubagentStopInput struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath string  `json:"transcript_path"`
	HookEventName  string  `json:"hook_event_name"`
	SubagentID     string  `json:"subagent_id"`
	SubagentTask   string  `json:"subagent_task"`
	DurationMs     int64   `json:"duration_ms"`
	Status         string  `json:"status"`
	Result         any     `json:"result"`
	Error          *string `json:"error"`
	CurrentUUID    string  `json:"current_uuid,omitempty"`
	ParentUUID     string  `json:"parent_uuid,omitempty"`
}

type PreToolUseResponse struct {
	Approved bool   `json:"approved"`
	Message  string `json:"message,omitempty"`
}

// Hook implementations

func RunNotificationHook() {
	ctx, err := api.NewHookContext()
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

	// Update API
	if err := ctx.APIClient.LogNotification(data.SessionID, data.Type, data.Level,
		data.Message, data.SystemNotificationSent); err != nil {
		log.Printf("Failed to update API: %v", err)
	}
}

func RunPreToolUseHook() {
	ctx, err := api.NewHookContext()
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
	if err := ctx.APIClient.EnsureSessionExists(data.SessionID, data.TranscriptPath); err != nil {
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

	// Store tool ID for post-tool correlation
	var toolID string
	if response.Approved {
		toolID, err = ctx.APIClient.LogToolUsage(data.SessionID, data.ToolName,
			data.ToolInput, response.Approved, response.Message)
		if err != nil {
			log.Printf("Failed to log tool usage: %v", err)
		} else if toolID != "" {
			storeToolID(data.SessionID, toolID)
		}
	}

	// Return response
	responseJSON, _ := json.Marshal(response)
	fmt.Print(string(responseJSON))
}

func RunPostToolUseHook() {
	ctx, err := api.NewHookContext()
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

		if err := ctx.APIClient.UpdateToolExecution(data.SessionID, toolID,
			data.ToolDurationMs, success, resultSummary, errorMsg); err != nil {
			log.Printf("Failed to update tool execution: %v", err)
		}

		cleanupToolID(data.SessionID)
	}
}

func RunStopHook() {
	ctx, err := api.NewHookContext()
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
	session, err := ctx.APIClient.GetSession(data.SessionID)
	if err != nil {
		log.Printf("Failed to get session details: %v", err)
	} else if session != nil && session.WorkingDirectory != "" {
		// Execute repository-specific hook commands
		log.Printf("Checking for .canopy.yaml in working directory: %s", session.WorkingDirectory)
		if err := ctx.ExecuteRepoHookCommands(session.WorkingDirectory); err != nil {
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

	// Update API based on exit reason
	// Mark as completed for actual completion or errors
	// Otherwise, set to idle (for normal end-of-turn stops)
	if data.ExitReason == "completed" || data.ExitReason == "error" || data.ExitReason == "interrupted" || data.ExitReason == "killed" {
		durationSeconds := int(data.DurationMs / 1000)
		if err := ctx.APIClient.CompleteSession(data.SessionID, durationSeconds,
			data.ExitReason, summary); err != nil {
			log.Printf("Failed to complete session: %v", err)
		}

		// Send ntfy notification for completed sessions
		sendNtfyNotification(ctx, data, "completed")
	} else {
		// Normal end-of-turn stop (empty exit_reason or other) - set to idle
		if err := ctx.APIClient.UpdateSession(data.SessionID, "idle"); err != nil {
			log.Printf("Failed to update session status to idle: %v", err)
		}

		// Send ntfy notification for normal session stops too
		sendNtfyNotification(ctx, data, "stopped")
	}
}

func RunSubagentStopHook() {
	ctx, err := api.NewHookContext()
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

	// Update API
	durationSeconds := int(data.DurationMs / 1000)
	if err := ctx.APIClient.LogSubagent(data.SessionID, data.SubagentID,
		data.SubagentTask, taskType, durationSeconds, data.Status, result); err != nil {
		log.Printf("Failed to log subagent: %v", err)
	}
}

// Helper functions

func shouldSendSystemNotification(data NotificationInput) bool {
	// Send for errors and warnings
	return data.Level == "error" || data.Level == "warning"
}

func sendSystemNotification(data NotificationInput) bool {
	err := notifications.SendSystem("Claude Code", data.Message, data.Level)
	return err == nil
}


func validateTool(toolName string, toolInput map[string]any, workingDir string) PreToolUseResponse {
	// Tool validation logic here
	// For now, approve all tools
	return PreToolUseResponse{
		Approved: true,
	}
}

func storeToolID(sessionID, toolID string) {
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("claude-tool-%s.json", sessionID))
	data, _ := json.Marshal(map[string]string{"tool_id": toolID})
	os.WriteFile(tmpFile, data, 0644)
}

func getStoredToolID(sessionID string) string {
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("claude-tool-%s.json", sessionID))
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return ""
	}
	var stored map[string]string
	json.Unmarshal(data, &stored)
	return stored["tool_id"]
}

func cleanupToolID(sessionID string) {
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("claude-tool-%s.json", sessionID))
	os.Remove(tmpFile)
}

func buildResultSummary(data PostToolUseInput) map[string]any {
	// Build result summary from tool response
	summary := make(map[string]any)
	summary["tool_name"] = data.ToolName
	summary["duration_ms"] = data.ToolDurationMs

	// Extract tool-specific information
	if inputMap, ok := data.ToolInput.(map[string]any); ok {
		switch data.ToolName {
		case "Bash":
			if command, ok := inputMap["command"].(string); ok {
				summary["command"] = command
			}
		case "Edit", "Write", "MultiEdit":
			if filePath, ok := inputMap["file_path"].(string); ok {
				summary["modified_files"] = []string{filePath}
			}
		case "Read":
			if filePath, ok := inputMap["file_path"].(string); ok {
				summary["files_read"] = []string{filePath}
			}
		}
	}

	return summary
}

func sendNtfyNotification(ctx *api.HookContext, data StopInput, status string) {
	// Get notification settings from config
	config, err := ctx.LoadConfig()
	if err != nil {
		log.Printf("Failed to load config for ntfy: %v", err)
		return
	}

	// Check if ntfy is enabled in the config
	ntfyConfig, ok := config["notifications"].(map[string]interface{})
	if !ok {
		return
	}

	ntfy, ok := ntfyConfig["ntfy"].(map[string]interface{})
	if !ok {
		return
	}

	enabled, _ := ntfy["enabled"].(bool)
	if !enabled {
		return
	}

	topic, _ := ntfy["topic"].(string)
	if topic == "" {
		return
	}

	// Get session info
	session, err := ctx.APIClient.GetSession(data.SessionID)
	if err != nil {
		log.Printf("Failed to get session for ntfy: %v", err)
		return
	}

	// Create ntfy notifier
	ntfyURL := "https://ntfy.sh"
	if url, ok := ntfy["url"].(string); ok && url != "" {
		ntfyURL = url
	}

	// Prepare notification message
	message := fmt.Sprintf("Claude finished: %s", session.Repo)
	if session.Branch != "" {
		message = fmt.Sprintf("Claude finished: %s (%s)", session.Repo, session.Branch)
	}

	// Send notification
	if err := notifications.SendNtfy(ntfyURL, topic, "Claude Session Completed", message, "default", []string{"claude", status}); err != nil {
		log.Printf("Failed to send ntfy notification: %v", err)
	} else {
		log.Printf("Sent ntfy notification: %s", message)
	}
}

func determineTaskType(task string) string {
	task = strings.ToLower(task)
	if strings.Contains(task, "search") || strings.Contains(task, "find") {
		return "search"
	} else if strings.Contains(task, "implement") || strings.Contains(task, "create") {
		return "implementation"
	} else if strings.Contains(task, "debug") || strings.Contains(task, "fix") {
		return "debugging"
	}
	return "analysis"
}

func generateSessionSummary(data StopInput) map[string]any {
	// Generate session summary
	summary := map[string]any{
		"session_id":  data.SessionID,
		"exit_reason": data.ExitReason,
		"duration_ms": data.DurationMs,
	}
	return summary
}
