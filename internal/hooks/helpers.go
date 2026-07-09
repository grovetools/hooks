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

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/models"
	notifications "github.com/grovetools/notify"
)

// HookBlockingError represents an error that should block the session from stopping
type HookBlockingError struct {
	Message string
}

func (e *HookBlockingError) Error() string {
	return e.Message
}

// Helper functions

// waitingNotificationPatterns are case-insensitive substrings of Claude Code's
// Notification `message` that indicate the agent is blocked waiting on the user
// (a tool-permission prompt, a plan-approval prompt, or an AskUserQuestion
// survey). Pinned to the strings actually observed in the hook event log
// (~/.local/state/grove/hooks/events.jsonl); wording drift is a one-line edit.
//
// Observed messages this covers (blocked-on-user):
//   - "Claude needs your permission"                  -> "permission"
//   - "Claude Code needs your approval for the plan"  -> "needs your"
//   - "Claude Code needs your attention"              -> "needs your"
//
// AskUserQuestion is covered here too: a live probe of the event log
// (113 invocations) showed it emits "Claude needs your permission" in 103 cases
// and "Claude Code needs your attention" in 1 — both matched above; the
// remaining 9 emitted no notification (auto-approved, not blocked on a human).
//
// Deliberately NOT matched: "Claude is waiting for your input". The probe showed
// that string is a generic idle nudge fired ~60s after EVERY Stop (turn-end),
// not a blocked-on-survey signal. Matching it would wrongly flip every ordinary
// idle agent to pending_user, which the ticket forbids. The Stop hook already
// marks genuine turn-end as idle/completed, so ordinary idle needs no handling
// here.
var waitingNotificationPatterns = []string{
	"permission",
	"needs your",
}

// isWaitingNotification reports whether a Notification message indicates the
// agent is blocked waiting on the user, so its session should be marked
// pending_user. The match is case-insensitive substring against
// waitingNotificationPatterns.
func isWaitingNotification(msg string) bool {
	lower := strings.ToLower(msg)
	for _, pattern := range waitingNotificationPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// idleNudgeNotificationPatterns are case-insensitive substrings of the generic
// idle nudge Claude Code fires ~60s after a turn ends (see the note on
// waitingNotificationPatterns above). It is deliberately NOT a
// blocked-on-user signal, but it IS an authoritative "the agent has returned
// to the input prompt and is idle" signal — it only fires once the turn has
// actually ended, never while a permission/survey prompt is still on screen
// (that turn is suspended, not stopped). We use it to un-stick a session that
// is stranded at pending_user: on a permission DENY that ends the turn without
// firing a Stop hook (observed for hard rejects/interrupts — see
// events.jsonl session baf2c3d0, where every deny that ended a turn was
// followed by a Stop, but the harness gives no such guarantee for an
// interrupt-style deny), nothing supersedes the pending_user set by the
// Notification hook, so the drawer stays loud until the user's next turn. This
// nudge is the one reliable "now idle" signal that still arrives in that case.
var idleNudgeNotificationPatterns = []string{
	"waiting for your input",
}

// isIdleNudgeNotification reports whether a Notification message is the generic
// post-turn idle nudge (see idleNudgeNotificationPatterns).
func isIdleNudgeNotification(msg string) bool {
	lower := strings.ToLower(msg)
	for _, pattern := range idleNudgeNotificationPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func shouldSendSystemNotification(ctx *HookContext, data NotificationInput) bool {
	// Never pop a blank banner.
	if strings.TrimSpace(data.Message) == "" {
		return false
	}
	// Claude Code emits an empty level for its attention-worthy notifications
	// (permission prompts, "waiting for your input", "needs your attention").
	// An exact level match would silently drop all of those, so treat an
	// unspecified level as always-notify — the Notification hook only fires
	// when Claude actually wants the user.
	if data.Level == "" {
		return true
	}
	// Otherwise honor the configured level allowlist.
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
	_ = os.WriteFile(tmpFile, data, 0o644)
}

func getStoredToolID(sessionID string) string {
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("claude-tool-%s.json", sessionID))
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return ""
	}
	var stored map[string]string
	_ = json.Unmarshal(data, &stored)
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
		case "Edit", "Write", "MultiEdit", "Replace":
			if filePath, ok := inputMap["file_path"].(string); ok {
				summary["modified_files"] = []string{normalizeFilePath(filePath)}
			}
		case "Read", "View":
			if filePath, ok := inputMap["file_path"].(string); ok {
				summary["files_read"] = []string{normalizeFilePath(filePath)}
			}
		}
	}

	return summary
}

// normalizeFilePath makes an absolute file path relative to the working directory (git root).
func normalizeFilePath(filePath string) string {
	if !filepath.IsAbs(filePath) {
		return filePath
	}
	workingDir := resolveWorkingDir("")
	if workingDir == "" {
		return filePath
	}
	rel, err := filepath.Rel(workingDir, filePath)
	if err != nil {
		return filePath
	}
	return rel
}

// ntfySessionContext resolves the human-facing context for an ntfy message:
// the agent label (job title, falling back to the session id) and a set of
// context lines (plan, worktree, repo) so the user can tell at a glance WHICH
// agent in WHICH worktree/plan the notification is about.
func ntfySessionContext(ctx *HookContext, sessionID string) (label string, contextLines []string) {
	label = sessionID
	sessionData, err := ctx.Storage.GetSession(sessionID)
	if err != nil || sessionData == nil {
		return label, contextLines
	}
	session, ok := sessionData.(*models.Session)
	if !ok || session == nil {
		return label, contextLines
	}
	if session.JobTitle != "" {
		label = session.JobTitle
	}
	if session.PlanName != "" {
		contextLines = append(contextLines, fmt.Sprintf("📂 plan: %s", session.PlanName))
	}
	// Branch is the grove worktree name; only show it when it adds information
	// beyond the plan name (they're often identical for flow plans).
	if session.Branch != "" && session.Branch != session.PlanName {
		contextLines = append(contextLines, fmt.Sprintf("🌿 worktree: %s", session.Branch))
	}
	if session.Repo != "" {
		contextLines = append(contextLines, fmt.Sprintf("📦 repo: %s", session.Repo))
	}
	return label, contextLines
}

// sendNtfyNotification pushes an ntfy notification when an agent finishes
// (the "completion" half of the notify scope). Title carries the agent name and
// outcome; body carries plan/worktree/repo + duration.
func sendNtfyNotification(ctx *HookContext, data StopInput, status string) {
	cfg := ctx.Config.Ntfy
	if !cfg.Enabled || cfg.Topic == "" {
		return
	}

	label, lines := ntfySessionContext(ctx, data.SessionID)
	title := fmt.Sprintf("✅ %s — %s", label, status)

	if data.DurationMs > 0 {
		lines = append(lines, fmt.Sprintf("⏱️ %ds", data.DurationMs/1000))
	}
	if data.ExitReason != "" {
		lines = append(lines, fmt.Sprintf("status: %s", data.ExitReason))
	}
	message := strings.Join(lines, "\n")
	if message == "" {
		message = "Agent finished."
	}

	if err := notifications.SendNtfy(cfg.URL, cfg.Topic, title, message, "default", []string{"white_check_mark"}); err != nil {
		log.Printf("Failed to send ntfy notification: %v", err)
	}
}

// sendWaitingNtfyNotification pushes an ntfy notification when an agent blocks
// waiting on the user (permission prompt / plan approval / AskUserQuestion).
// This is the "waiting-on-you" half of the notify scope, driven by the
// Notification hook. Title carries the agent name and what it wants; body
// carries the raw prompt text + plan/worktree/repo context.
func sendWaitingNtfyNotification(ctx *HookContext, sessionID, rawMessage string) {
	cfg := ctx.Config.Ntfy
	if !cfg.Enabled || cfg.Topic == "" {
		return
	}

	label, lines := ntfySessionContext(ctx, sessionID)

	// Summarize what the agent is blocked on, from the notification wording.
	action := "waiting for your answer"
	lower := strings.ToLower(rawMessage)
	switch {
	case strings.Contains(lower, "permission"):
		action = "needs permission"
	case strings.Contains(lower, "approval"), strings.Contains(lower, "approve"):
		action = "needs plan approval"
	}
	title := fmt.Sprintf("⏳ %s — %s", label, action)

	body := lines
	if rawMessage != "" {
		body = append([]string{rawMessage}, body...)
	}
	message := strings.Join(body, "\n")
	if message == "" {
		message = "Agent is waiting on you."
	}

	if err := notifications.SendNtfy(cfg.URL, cfg.Topic, title, message, "high", []string{"hourglass"}); err != nil {
		log.Printf("Failed to send waiting ntfy notification: %v", err)
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
func ExecuteHookCommand(workingDir string, hookCmd config.HookCommand) error {
	log.Printf("Running: %s", hookCmd.Command)

	cmd := exec.Command("sh", "-c", hookCmd.Command) //nolint:gosec // command from trusted config
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
	if err := os.WriteFile(jobFilePath, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("failed to write job file: %w", err)
	}

	return nil
}
