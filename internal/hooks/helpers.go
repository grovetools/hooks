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
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-notifications"
)

// HookBlockingError represents an error that should block the session from stopping
type HookBlockingError struct {
	Message string
}

func (e *HookBlockingError) Error() string {
	return e.Message
}

// Helper functions

func shouldSendSystemNotification(ctx *HookContext, data NotificationInput) bool {
	// Check if the notification level is in the configured levels
	for _, enabledLevel := range ctx.Config.System.Levels {
		if data.Level == enabledLevel {
			return true
		}
	}
	return false
}

func sendSystemNotification(data NotificationInput) bool {
	err := notifications.SendSystem("Claude Code", data.Message, data.Level)
	if err != nil {
		// Log error for debugging
		log.Printf("failed to send system notification: %v", err)
	}
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
	cfg := ctx.Config.Ntfy
	if !cfg.Enabled || cfg.Topic == "" {
		return
	}

	// Get session details for context
	sessionData, err := ctx.Storage.GetSession(data.SessionID)
	var repo, branch, projectName, sessionType, jobTitle, planName string
	var isWorktree bool

	if err == nil && sessionData != nil {
		if extSession, ok := sessionData.(*disk.ExtendedSession); ok {
			repo = extSession.Repo
			branch = extSession.Branch
			projectName = extSession.ProjectName
			isWorktree = extSession.IsWorktree
			sessionType = extSession.Type
			jobTitle = extSession.JobTitle
			planName = extSession.PlanName
		} else if session, ok := sessionData.(*models.Session); ok {
			repo = session.Repo
			branch = session.Branch
		}
	}

	// Construct title based on session type
	var title string
	isJob := sessionType == "oneshot_job" || sessionType == "agent_job" || sessionType == "interactive_agent" || sessionType == "chat"

	if isJob && data.SessionID != "" {
		// For jobs, use the job name (session ID) as the title
		title = fmt.Sprintf("%s %s", data.SessionID, status)
	} else if isJob {
		title = fmt.Sprintf("Job %s", status)
	} else {
		title = fmt.Sprintf("Session %s", status)
	}

	// Build message with repo/worktree context
	var messageParts []string

	// Add job type for jobs
	if isJob && sessionType != "" {
		messageParts = append(messageParts, fmt.Sprintf("🔷 %s", sessionType))
	}

	// Add job title if different from session ID
	if jobTitle != "" && jobTitle != data.SessionID {
		messageParts = append(messageParts, fmt.Sprintf("📋 %s", jobTitle))
	}

	// Add plan name for jobs
	if planName != "" {
		messageParts = append(messageParts, fmt.Sprintf("📂 Plan: %s", planName))
	}

	// Add project/worktree info
	if projectName != "" {
		if isWorktree {
			messageParts = append(messageParts, fmt.Sprintf("📁 %s (worktree)", projectName))
		} else {
			messageParts = append(messageParts, fmt.Sprintf("📁 %s", projectName))
		}
	} else if repo != "" {
		messageParts = append(messageParts, fmt.Sprintf("📁 %s", repo))
	}

	// Add branch info
	if branch != "" {
		messageParts = append(messageParts, fmt.Sprintf("🌿 %s", branch))
	}

	// Add duration
	if data.DurationMs > 0 {
		durationSec := data.DurationMs / 1000
		messageParts = append(messageParts, fmt.Sprintf("⏱️  %ds", durationSec))
	}

	// Add exit reason if present
	if data.ExitReason != "" {
		messageParts = append(messageParts, fmt.Sprintf("Status: %s", data.ExitReason))
	}

	message := strings.Join(messageParts, "\n")

	// Send ntfy notification
	if err := notifications.SendNtfy(cfg.URL, cfg.Topic, title, message, "default", nil); err != nil {
		log.Printf("Failed to send ntfy notification: %v", err)
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
						return &HookBlockingError{Message: stderrOutput}
					} else {
						return &HookBlockingError{Message: fmt.Sprintf("Hook command '%s' failed with blocking error (exit code 2)", hookCmd.Name)}
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

// getWorkingDirectory extracts the working directory from a session interface
func getWorkingDirectory(session interface{}) string {
	// Check if it's an extended session
	if extSession, ok := session.(*disk.ExtendedSession); ok {
		return extSession.WorkingDirectory
	}

	// Check if it's a regular session
	if baseSession, ok := session.(*models.Session); ok {
		return baseSession.WorkingDirectory
	}

	return ""
}

// updateJobFileStatus updates the status field in a grove-flow job file's YAML frontmatter
func updateJobFileStatus(jobFilePath, newStatus string) error {
	if jobFilePath == "" {
		return fmt.Errorf("job file path is empty")
	}

	// Read the file
	content, err := os.ReadFile(jobFilePath)
	if err != nil {
		return fmt.Errorf("failed to read job file: %w", err)
	}

	lines := strings.Split(string(content), "\n")

	// Find the frontmatter boundaries and the status line
	inFrontmatter := false
	frontmatterStart := -1
	frontmatterEnd := -1
	statusLineIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				frontmatterStart = i
			} else {
				frontmatterEnd = i
				break
			}
		} else if inFrontmatter && strings.HasPrefix(trimmed, "status:") {
			statusLineIdx = i
		}
	}

	if frontmatterStart == -1 || frontmatterEnd == -1 {
		return fmt.Errorf("could not find YAML frontmatter in job file")
	}

	if statusLineIdx == -1 {
		return fmt.Errorf("could not find status field in frontmatter")
	}

	// Get the indentation from the original line
	originalLine := lines[statusLineIdx]
	indent := ""
	for _, ch := range originalLine {
		if ch == ' ' || ch == '\t' {
			indent += string(ch)
		} else {
			break
		}
	}

	// Update the status line
	lines[statusLineIdx] = fmt.Sprintf("%sstatus: %s", indent, newStatus)

	// Write the file back
	newContent := strings.Join(lines, "\n")
	if err := os.WriteFile(jobFilePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write job file: %w", err)
	}

	return nil
}
