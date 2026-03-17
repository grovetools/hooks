package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/process"
	notifications "github.com/grovetools/notify"
	notificationsconfig "github.com/grovetools/notify/pkg/config"
)

// Phase 3 Thin Client: hooks/commands/discovery.go
// This file is now a "View Controller" that fetches sessions from the daemon client
// and merges with the local SQLite DB for historical/archival data.
// All heavy scanning logic has been moved to core/pkg/sessions/discovery.go.

// EnableBackgroundRefresh is a no-op in Phase 3.
// The daemon now handles background session monitoring.
func EnableBackgroundRefresh() {
	// No-op: daemon handles this now
}

// StartBackgroundRefresh is a no-op in Phase 3.
// The daemon now handles background session monitoring.
func StartBackgroundRefresh() {
	// No-op: daemon handles this now
}

// GetAllSessions fetches sessions from the daemon (or LocalClient fallback).
// The daemon is the single source of truth — no SQLite merging.
func GetAllSessions(client daemon.Client, hideCompleted bool) ([]*models.Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	allSessions, err := client.GetSessions(ctx)
	if err != nil {
		if os.Getenv("GROVE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "Warning: failed to get sessions from daemon: %v\n", err)
		}
		allSessions = []*models.Session{}
	}

	// Filter by hideCompleted if requested
	if hideCompleted {
		var filtered []*models.Session
		for _, s := range allSessions {
			if s.Status != "completed" && s.Status != "failed" && s.Status != "error" && s.Status != "interrupted" {
				filtered = append(filtered, s)
			}
		}
		allSessions = filtered
	}

	// Sort: Running > Pending/Idle > Others, then by LastActivity desc
	sort.Slice(allSessions, func(i, j int) bool {
		p1 := getStatusPriority(allSessions[i].Status)
		p2 := getStatusPriority(allSessions[j].Status)

		if p1 != p2 {
			return p1 < p2
		}

		iTime := allSessions[i].LastActivity
		if iTime.IsZero() {
			iTime = allSessions[i].StartedAt
		}
		jTime := allSessions[j].LastActivity
		if jTime.IsZero() {
			jTime = allSessions[j].StartedAt
		}
		return iTime.After(jTime)
	})

	return allSessions, nil
}

// getStatusPriority returns a sort priority for session statuses
func getStatusPriority(status string) int {
	switch status {
	case "running":
		return 1
	case "pending_user", "idle":
		return 2
	default:
		return 3
	}
}

// DispatchStateChangeNotifications compares old and new sessions and sends notifications for relevant state changes
func DispatchStateChangeNotifications(oldSessions, newSessions []*models.Session) {
	// Build a map of old session states for quick lookup
	oldStatusMap := make(map[string]string)
	for _, s := range oldSessions {
		oldStatusMap[s.ID] = s.Status
	}

	// Check each new session for relevant state changes
	for _, newSession := range newSessions {
		oldStatus, wasTracked := oldStatusMap[newSession.ID]
		if !wasTracked {
			// This is a new session, not a state change
			continue
		}

		// Rule: Notify when a chat job transitions from running to pending_user
		if newSession.Type == "chat" && newSession.Status == "pending_user" && oldStatus == "running" {
			sendJobReadyNotification(newSession)
		}

		// Rule: Notify when an interactive_agent or isolated_agent job transitions from running to idle
		if (newSession.Type == "interactive_agent" || newSession.Type == "isolated_agent") && newSession.Status == "idle" && oldStatus == "running" {
			sendJobReadyNotification(newSession)
		}

		// Rule: Notify when a oneshot job completes
		if newSession.Type == "oneshot" && newSession.Status == "completed" && oldStatus == "running" {
			sendJobReadyNotification(newSession)
		}

		// Future notification rules can be added here
		// For example:
		// - Job failures: oldStatus == "running" && newSession.Status == "failed"
	}
}

// sendJobReadyNotification sends a notification that a job is ready for user input
func sendJobReadyNotification(session *models.Session) {
	// Load config to check notification settings
	cfg := notificationsconfig.Load()

	// Build title from session info based on job type
	var title string
	if session.Type == "chat" {
		title = fmt.Sprintf("💬 Chat Ready: %s", session.JobTitle)
		if session.JobTitle == "" && session.PlanName != "" {
			title = fmt.Sprintf("💬 Chat Ready: %s", session.PlanName)
		} else if session.JobTitle == "" {
			title = "💬 Chat Ready"
		}
	} else if session.Type == "interactive_agent" || session.Type == "isolated_agent" {
		title = fmt.Sprintf("🤖 Agent Idle: %s", session.JobTitle)
		if session.JobTitle == "" && session.PlanName != "" {
			title = fmt.Sprintf("🤖 Agent Idle: %s", session.PlanName)
		} else if session.JobTitle == "" {
			title = "🤖 Agent Idle"
		}
	} else if session.Type == "oneshot" {
		title = fmt.Sprintf(" Oneshot Complete: %s", session.JobTitle)
		if session.JobTitle == "" && session.PlanName != "" {
			title = fmt.Sprintf(" Oneshot Complete: %s", session.PlanName)
		} else if session.JobTitle == "" {
			title = " Oneshot Complete"
		}
	} else {
		title = fmt.Sprintf("Job Ready: %s", session.JobTitle)
		if session.JobTitle == "" {
			title = "Job Ready"
		}
	}

	// Build detailed message with session context
	var messageParts []string

	// Add session ID
	if session.ID != "" {
		messageParts = append(messageParts, fmt.Sprintf("ID: %s", session.ID))
	}

	// Add job type
	if session.Type != "" {
		messageParts = append(messageParts, fmt.Sprintf("Type: %s", session.Type))
	}

	// Add repository and worktree/branch
	if session.Repo != "" {
		if session.Branch != "" {
			messageParts = append(messageParts, fmt.Sprintf("Worktree: %s/%s", session.Repo, session.Branch))
		} else {
			messageParts = append(messageParts, fmt.Sprintf("Repo: %s", session.Repo))
		}
	}

	// Add plan name if different from job title
	if session.PlanName != "" && session.PlanName != session.JobTitle {
		messageParts = append(messageParts, fmt.Sprintf("Plan: %s", session.PlanName))
	}

	message := strings.Join(messageParts, "\n")

	// Send ntfy notification if configured
	if cfg.Ntfy.Enabled && cfg.Ntfy.Topic != "" {
		_ = notifications.SendNtfy(
			cfg.Ntfy.URL,
			cfg.Ntfy.Topic,
			title,
			message,
			"default",
			nil,
		)
	}

	// Also send system notification if configured
	if len(cfg.System.Levels) > 0 {
		_ = notifications.SendSystem(title, message, "info")
	}
}

// markInterruptedJobsInPlan scans a single plan directory and marks jobs with status: running as interrupted
// if their lock file is missing or PID is dead. Returns the number of jobs updated.
// This is kept for the mark-interrupted subcommand.
func markInterruptedJobsInPlan(planDir string, dryRun bool) (int, error) {
	updated := 0

	entries, err := os.ReadDir(planDir)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()

		// Skip non-.md files
		if !strings.HasSuffix(filename, ".md") {
			continue
		}

		// Skip spec.md and other non-job files
		if filename == "spec.md" || filename == "README.md" {
			continue
		}

		filePath := filepath.Join(planDir, filename)

		// Read the file
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		contentStr := string(content)

		// Parse frontmatter to check status and type
		jobInfo := parseJobFrontmatter(contentStr)
		if jobInfo.Status != "running" {
			continue
		}

		// Skip interactive_agent and isolated_agent jobs - they may not have lock files immediately after start
		if jobInfo.Type == "interactive_agent" || jobInfo.Type == "isolated_agent" {
			continue
		}

		// For other jobs, check for lock file
		lockFile := filePath + ".lock"
		shouldMark := false

		if _, err := os.Stat(lockFile); os.IsNotExist(err) {
			// No lock file - mark as interrupted
			shouldMark = true
		} else {
			// Lock file exists - check if PID is alive
			pidContent, err := os.ReadFile(lockFile)
			if err != nil {
				shouldMark = true
			} else {
				var pid int
				if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err == nil {
					if !process.IsProcessAlive(pid) {
						shouldMark = true
					}
				} else {
					shouldMark = true
				}
			}
		}

		if shouldMark {
			if dryRun {
				fmt.Printf("Would update: %s\n", filePath)
			} else {
				// Update the frontmatter
				newContent := strings.Replace(contentStr, "status: running", "status: interrupted", 1)
				if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
					return updated, fmt.Errorf("failed to update %s: %w", filePath, err)
				}
				fmt.Printf("Updated: %s\n", filePath)
			}
			updated++
		}
	}

	return updated, nil
}

// jobInfo holds metadata parsed from a job's frontmatter.
type jobInfo struct {
	ID        string
	Title     string
	Status    string
	Type      string
	StartedAt time.Time
}

// parseJobFrontmatter extracts ID, title, status, type, and start time from frontmatter.
func parseJobFrontmatter(content string) jobInfo {
	info := jobInfo{
		Status: "pending",
		Type:   "oneshot", // Default to oneshot if not specified
	}
	lines := strings.Split(content, "\n")
	inFrontmatter := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			} else {
				break
			}
		}

		if !inFrontmatter {
			continue
		}

		// Skip lines with leading whitespace (nested YAML structures)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Strip quotes if present
		value = strings.Trim(value, `"`)

		switch key {
		case "id":
			info.ID = value
		case "title":
			info.Title = value
		case "status":
			info.Status = value
		case "type":
			info.Type = value
		case "start_time": // Grove-flow uses this field
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				info.StartedAt = t
			}
		case "updated_at": // Fallback for older jobs
			if info.StartedAt.IsZero() {
				if t, err := time.Parse(time.RFC3339, value); err == nil {
					info.StartedAt = t
				}
			}
		}
	}
	if info.ID == "" {
		// Fallback if no ID is present.
		info.ID = info.Title
	}
	return info
}

