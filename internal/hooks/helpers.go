package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/api"
	"github.com/mattsolo1/grove-notifications"
)

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

func sendNtfyNotification(ctx *HookContext, data StopInput, status string) {
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
	session, err := ctx.GetSession(data.SessionID)
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

// ExecuteHookCommand executes a single hook command (moved from api package)
func ExecuteHookCommand(workingDir string, hookCmd models.HookCommand) error {
	log.Printf("Running: %s", hookCmd.Command)

	cmd := exec.Command("sh", "-c", hookCmd.Command)
	cmd.Dir = workingDir

	// Capture stderr to handle exit code 2 blocking behavior
	var stderrBuf bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if err != nil {
		// Check if this is an exit error and get the exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			if ws, ok := exitError.Sys().(syscall.WaitStatus); ok {
				exitCode := ws.ExitStatus()
				stderrOutput := strings.TrimSpace(stderrBuf.String())

				// Exit code 2 means blocking error - feed stderr back to Claude
				if exitCode == 2 {
					if stderrOutput != "" {
						return &api.HookBlockingError{Message: stderrOutput}
					} else {
						return &api.HookBlockingError{Message: fmt.Sprintf("Hook command '%s' failed with blocking error (exit code 2)", hookCmd.Name)}
					}
				}

				// For other exit codes, include stderr in the error but don't block
				if stderrOutput != "" {
					return fmt.Errorf("command failed with exit code %d: %s", exitCode, stderrOutput)
				}
			}
		}

		// For other types of errors, return as-is
		return err
	}

	return nil
}