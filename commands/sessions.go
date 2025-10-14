package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/process"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/spf13/cobra"
)

func NewSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Manage and query local agent sessions",
	}

	cmd.AddCommand(newSessionsListCmd())
	cmd.AddCommand(newSessionsGetCmd())
	cmd.AddCommand(NewBrowseCmd())
	cmd.AddCommand(NewCleanupCmd())
	cmd.AddCommand(newSessionsArchiveCmd())
	cmd.AddCommand(newMarkInterruptedCmd())
	cmd.AddCommand(newKillCmd())
	cmd.AddCommand(newSetStatusCmd())

	return cmd
}

func newSessionsListCmd() *cobra.Command {
	var (
		statusFilter  string
		planFilter    string
		typeFilter    string
		jsonOutput    bool
		limit         int
		hideCompleted bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all sessions (primarily interactive Claude sessions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Clean up dead sessions first
			_, _ = CleanupDeadSessions(storage)

			// Fetch all sessions using the centralized discovery function
			sessions, err := GetAllSessions(storage, hideCompleted)
			if err != nil {
				return fmt.Errorf("failed to get all sessions: %w", err)
			}

			// Filter by status if requested
			if statusFilter != "" {
				var filtered []*models.Session
				for _, s := range sessions {
					if s.Status == statusFilter {
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			// Filter by plan name if requested
			if planFilter != "" {
				var filtered []*models.Session
				for _, s := range sessions {
					if s.PlanName == planFilter {
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			// Filter by type if requested
			if typeFilter != "" {
				var filtered []*models.Session
				for _, s := range sessions {
					sessionType := s.Type
					if sessionType == "" || sessionType == "claude_session" {
						sessionType = "claude_code"
					}
					// Normalize job type names. 'job' is an alias for 'oneshot_job'.
					isJob := sessionType == "oneshot_job"

					if (typeFilter == "job" && isJob) || sessionType == typeFilter {
						filtered = append(filtered, s)
					} else if (typeFilter == "claude" || typeFilter == "claude_code") && !isJob {
						// If user filters for 'claude' or 'claude_code', include sessions that are not jobs.
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			// Sorting and filtering by 'hideCompleted' is now handled by GetAllSessions.

			// Apply limit
			if limit > 0 && len(sessions) > limit {
				sessions = sessions[:limit]
			}

			// Output results
			if jsonOutput {
				// Enhance sessions with state duration info
				type SessionWithStateDuration struct {
					*models.Session
					StateDuration        string `json:"state_duration"`
					StateDurationSeconds int64  `json:"state_duration_seconds"`
				}

				enhancedSessions := make([]SessionWithStateDuration, len(sessions))
				now := time.Now()

				for i, s := range sessions {
					enhanced := SessionWithStateDuration{Session: s}

					// Calculate time in current state
					if s.Status == "running" || s.Status == "idle" {
						// For active sessions, time since last activity
						duration := now.Sub(s.LastActivity)
						enhanced.StateDuration = duration.Round(time.Second).String()
						enhanced.StateDurationSeconds = int64(duration.Seconds())
					} else if s.EndedAt != nil {
						// For completed sessions, show how long they ran
						duration := s.EndedAt.Sub(s.StartedAt)
						enhanced.StateDuration = duration.Round(time.Second).String()
						enhanced.StateDurationSeconds = int64(duration.Seconds())
					} else {
						// Fallback to time since started
						duration := now.Sub(s.StartedAt)
						enhanced.StateDuration = duration.Round(time.Second).String()
						enhanced.StateDurationSeconds = int64(duration.Seconds())
					}

					enhancedSessions[i] = enhanced
				}

				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(enhancedSessions)
			}

			// Table output
			if len(sessions) == 0 {
				fmt.Println("No sessions found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SESSION ID\tTYPE\tSTATUS\tCONTEXT\tUSER\tSTARTED\tDURATION\tIN STATE")

			for _, s := range sessions {
				// Derive status for oneshot jobs based on ended_at
				displayStatus := s.Status
				if s.Type == "oneshot_job" && s.EndedAt == nil {
					// For oneshot jobs, if ended_at is NULL, show as running
					displayStatus = "running"
				}

				duration := "running"
				if s.EndedAt != nil {
					duration = s.EndedAt.Sub(s.StartedAt).Round(time.Second).String()
				} else if displayStatus == "idle" {
					duration = "idle"
				}

				// Calculate time in current state
				inState := ""
				if displayStatus == "running" || displayStatus == "idle" {
					// For active sessions, time since last activity
					inState = time.Since(s.LastActivity).Round(time.Second).String()
				} else if s.EndedAt != nil {
					// For completed sessions, show how long they ran
					inState = s.EndedAt.Sub(s.StartedAt).Round(time.Second).String()
				} else {
					// Fallback to time since started
					inState = time.Since(s.StartedAt).Round(time.Second).String()
				}

				started := s.StartedAt.Format("2006-01-02 15:04:05")

				// Format context based on session type with enhanced worktree information
				context := ""
				sessionType := s.Type
				if sessionType == "" || sessionType == "claude_session" {
					sessionType = "claude_code"
				}
				if s.Type == "oneshot_job" {
					sessionType = "job"
					// For jobs, show repo/branch with worktree indicator
					if s.Repo != "" && s.Branch != "" {
						if s.Branch == "main" || s.Branch == "master" {
							context = s.Repo
						} else {
							context = fmt.Sprintf("%s (wt:%s)", s.Repo, s.Branch)
						}
						// Optionally append title if it fits
						if s.JobTitle != "" && len(context)+len(s.JobTitle)+3 <= 30 {
							context = fmt.Sprintf("%s:%s", context, s.JobTitle)
						}
					} else if s.Repo != "" {
						context = s.Repo
						if s.JobTitle != "" && len(context)+len(s.JobTitle)+3 <= 30 {
							context = fmt.Sprintf("%s:%s", context, s.JobTitle)
						}
					} else if s.PlanName != "" {
						// Show plan name when no repo info
						context = s.PlanName
						if s.JobTitle != "" && len(context)+len(s.JobTitle)+3 <= 30 {
							context = fmt.Sprintf("%s:%s", context, s.JobTitle)
						}
					} else if s.JobTitle != "" {
						// Fallback to title alone
						context = s.JobTitle
					} else {
						context = "oneshot"
					}
				} else {
					// Claude session - show worktree indicator
					if s.Repo != "" && s.Branch != "" {
						if s.Branch == "main" || s.Branch == "master" {
							context = s.Repo
						} else {
							context = fmt.Sprintf("%s (wt:%s)", s.Repo, s.Branch)
						}
					} else if s.Repo != "" {
						context = s.Repo
					} else {
						context = "n/a"
					}
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					truncate(s.ID, 12),
					sessionType,
					displayStatus,
					truncate(context, 30),
					s.User,
					started,
					duration,
					inState,
				)
			}

			return w.Flush()
		},
	}

	cmd.Flags().StringVarP(&statusFilter, "status", "s", "", "Filter by status (running, idle, completed, failed)")
	cmd.Flags().StringVarP(&planFilter, "plan", "p", "", "Filter by plan name")
	cmd.Flags().StringVarP(&typeFilter, "type", "t", "", "Filter by session type (claude, job, oneshot_job)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "Limit number of results")
	cmd.Flags().BoolVar(&hideCompleted, "active", false, "Show only active sessions (hide completed/failed)")

	return cmd
}

func newSessionsGetCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "get <session-id>",
		Short: "Get details of a specific session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Get session
			sessionData, err := storage.GetSession(sessionID)
			if err != nil {
				return fmt.Errorf("failed to get session: %w", err)
			}

			// Output results
			if jsonOutput {
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(sessionData)
			}

			// Handle both regular and extended sessions
			var baseSession *models.Session
			var sessionType string = "claude_session"
			var planName, planDirectory, jobTitle, jobFilePath string

			if extSession, ok := sessionData.(*disk.ExtendedSession); ok {
				baseSession = &extSession.Session
				if extSession.Type != "" {
					sessionType = extSession.Type
				}
				planName = extSession.PlanName
				planDirectory = extSession.PlanDirectory
				jobTitle = extSession.JobTitle
				jobFilePath = extSession.JobFilePath
			} else if session, ok := sessionData.(*models.Session); ok {
				baseSession = session
			} else {
				return fmt.Errorf("unexpected session type: %T", sessionData)
			}

			// Normalize session type for display
			if sessionType == "claude_session" || sessionType == "" {
				sessionType = "claude_code"
			} else if sessionType == "oneshot_job" {
				sessionType = "job"
			}

			// Detailed text output
			fmt.Printf("Session ID: %s\n", baseSession.ID)
			fmt.Printf("Type: %s\n", sessionType)
			fmt.Printf("Status: %s\n", baseSession.Status)

			if sessionType == "job" {
				// Oneshot job specific fields
				if planName != "" {
					fmt.Printf("Plan: %s\n", planName)
				}
				if planDirectory != "" {
					fmt.Printf("Plan Directory: %s\n", planDirectory)
				}
				if jobTitle != "" {
					fmt.Printf("Job Title: %s\n", jobTitle)
				}
				if jobFilePath != "" {
					fmt.Printf("Job File: %s\n", jobFilePath)
				}
			} else {
				// Claude session specific fields
				fmt.Printf("Repository: %s\n", baseSession.Repo)
				fmt.Printf("Branch: %s\n", baseSession.Branch)
			}

			fmt.Printf("User: %s\n", baseSession.User)
			fmt.Printf("Working Directory: %s\n", baseSession.WorkingDirectory)
			fmt.Printf("PID: %d\n", baseSession.PID)
			fmt.Printf("Started: %s\n", baseSession.StartedAt.Format(time.RFC3339))

			if baseSession.EndedAt != nil {
				fmt.Printf("Ended: %s\n", baseSession.EndedAt.Format(time.RFC3339))
				fmt.Printf("Duration: %s\n", baseSession.EndedAt.Sub(baseSession.StartedAt).Round(time.Second))
			}

			if baseSession.TmuxKey != "" {
				fmt.Printf("Tmux Key: %s\n", baseSession.TmuxKey)
			}

			if baseSession.ToolStats != nil {
				fmt.Printf("\nTool Statistics:\n")
				fmt.Printf("  Total Calls: %d\n", baseSession.ToolStats.TotalCalls)
				fmt.Printf("  Bash Commands: %d\n", baseSession.ToolStats.BashCommands)
				fmt.Printf("  File Modifications: %d\n", baseSession.ToolStats.FileModifications)
				fmt.Printf("  File Reads: %d\n", baseSession.ToolStats.FileReads)
				fmt.Printf("  Search Operations: %d\n", baseSession.ToolStats.SearchOperations)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	return cmd
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func newSessionsArchiveCmd() *cobra.Command {
	var (
		archiveAll       bool
		archiveCompleted bool
		archiveFailed    bool
		archiveRunning   bool
		archiveIdle      bool
	)

	cmd := &cobra.Command{
		Use:   "archive [session-id...]",
		Short: "Archive one or more sessions",
		Long: `Archive sessions by marking them as deleted. Archived sessions are hidden from normal queries.

You can archive specific sessions by ID or use flags to archive multiple sessions:
  - Use --all to archive all sessions regardless of status
  - Use --completed to archive only completed sessions
  - Use --failed to archive only failed sessions
  - Use --running to archive only running sessions
  - Use --idle to archive only idle sessions`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			var sessionIDs []string

			// If specific session IDs provided, use those
			if len(args) > 0 {
				sessionIDs = args
			} else if archiveAll || archiveCompleted || archiveFailed || archiveRunning || archiveIdle {
				// Get all sessions
				sessions, err := storage.GetAllSessions()
				if err != nil {
					return fmt.Errorf("failed to get sessions: %w", err)
				}

				// Filter based on flags
				for _, s := range sessions {
					shouldArchive := false

					if archiveAll {
						// Archive all sessions
						shouldArchive = true
					} else if archiveCompleted && s.Status == "completed" {
						shouldArchive = true
					} else if archiveFailed && (s.Status == "failed" || s.Status == "error") {
						shouldArchive = true
					} else if archiveRunning && s.Status == "running" {
						shouldArchive = true
					} else if archiveIdle && s.Status == "idle" {
						shouldArchive = true
					}

					if shouldArchive {
						sessionIDs = append(sessionIDs, s.ID)
					}
				}
			} else {
				return fmt.Errorf("no session IDs provided and no archive flags specified. Use --all, --completed, --failed, --running, --idle, or provide session IDs")
			}

			if len(sessionIDs) == 0 {
				fmt.Println("No sessions to archive")
				return nil
			}

			// Archive the sessions
			if err := storage.ArchiveSessions(sessionIDs); err != nil {
				return fmt.Errorf("failed to archive sessions: %w", err)
			}

			fmt.Printf("Archived %d session(s)\n", len(sessionIDs))
			return nil
		},
	}

	cmd.Flags().BoolVar(&archiveAll, "all", false, "Archive all sessions regardless of status")
	cmd.Flags().BoolVar(&archiveCompleted, "completed", false, "Archive only completed sessions")
	cmd.Flags().BoolVar(&archiveFailed, "failed", false, "Archive only failed sessions")
	cmd.Flags().BoolVar(&archiveRunning, "running", false, "Archive only running sessions")
	cmd.Flags().BoolVar(&archiveIdle, "idle", false, "Archive only idle sessions")

	return cmd
}

func newMarkInterruptedCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "mark-interrupted",
		Short: "Mark all stale grove-flow jobs as interrupted",
		Long: `Find all grove-flow job files with status: running and mark them as interrupted if:
  - Their lock file is missing, OR
  - Their PID is no longer alive

This updates the job frontmatter files directly.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			updated := 0

			// Common locations to search for plans
			planDirs := []string{
				expandPath("~/Documents/nb/repos"),
				expandPath("~/Code/nb/repos"),
				expandPath("~/Code"),
			}

			for _, baseDir := range planDirs {
				if _, err := os.Stat(baseDir); os.IsNotExist(err) {
					continue
				}

				// Walk the directory tree looking for plan directories
				err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return nil // Skip errors
					}

					// Look for "plans" directories
					if !info.IsDir() || info.Name() != "plans" {
						return nil
					}

					// Check subdirectories of the plans directory
					planEntries, err := os.ReadDir(path)
					if err != nil {
						return nil
					}

					for _, planEntry := range planEntries {
						if !planEntry.IsDir() {
							continue
						}

						planDir := filepath.Join(path, planEntry.Name())
						count, err := markInterruptedJobsInPlan(planDir, dryRun)
						if err != nil {
							// Log error but continue
							fmt.Fprintf(os.Stderr, "Warning: error processing plan %s: %v\n", planDir, err)
							continue
						}
						updated += count
					}

					return filepath.SkipDir // Don't descend into plans directories
				})

				if err != nil {
					// Continue with other base directories
					continue
				}
			}

			if dryRun {
				fmt.Printf("Dry run: would update %d job(s)\n", updated)
			} else {
				fmt.Printf("Updated %d job(s) to interrupted status\n", updated)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be updated without making changes")

	return cmd
}

func newKillCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "kill <session-id>",
		Short: "Kill a running Claude session",
		Long: `Kill a running Claude session by sending a SIGTERM signal to its process.
The session directory will be cleaned up after killing the process.

WARNING: This will terminate the Claude process immediately.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]

			// First, try to find the session in the filesystem
			groveSessionsDir := expandPath("~/.grove/hooks/sessions")
			sessionDir := filepath.Join(groveSessionsDir, sessionID)
			pidFile := filepath.Join(sessionDir, "pid.lock")

			// Check if session directory exists
			if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
				return fmt.Errorf("session not found: %s", sessionID)
			}

			// Read PID from lock file
			pidContent, err := os.ReadFile(pidFile)
			if err != nil {
				return fmt.Errorf("failed to read PID file: %w", err)
			}

			var pid int
			if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err != nil {
				return fmt.Errorf("invalid PID in lock file: %w", err)
			}

			// Check if process is alive
			if !process.IsProcessAlive(pid) {
				fmt.Printf("Session %s (PID %d) is not running\n", sessionID, pid)
				// Clean up the stale directory
				if err := os.RemoveAll(sessionDir); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to remove stale session directory: %v\n", err)
				} else {
					fmt.Printf("Cleaned up stale session directory\n")
				}
				return nil
			}

			// Confirm before killing unless --force is used
			if !force {
				fmt.Printf("Kill session %s (PID %d)? [y/N] ", sessionID, pid)
				var response string
				fmt.Scanln(&response)
				if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			// Kill the process using SIGTERM
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed to kill process: %w", err)
			}

			fmt.Printf("Killed session %s (PID %d)\n", sessionID, pid)

			// Clean up the session directory
			if err := os.RemoveAll(sessionDir); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to remove session directory: %v\n", err)
			} else {
				fmt.Printf("Cleaned up session directory\n")
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")

	return cmd
}

func newSetStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-status <job-file-path> <status>",
		Short: "Set the status of a grove-flow job",
		Long: `Update the status field in a grove-flow job's frontmatter.

Valid statuses: pending, running, completed, failed, interrupted

Example:
  grove-hooks sessions set-status ~/Code/nb/repos/my-repo/main/plans/my-plan/01-job.md interrupted`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobFilePath := args[0]
			newStatus := args[1]

			// Validate status
			validStatuses := map[string]bool{
				"pending":     true,
				"running":     true,
				"completed":   true,
				"failed":      true,
				"interrupted": true,
			}
			if !validStatuses[newStatus] {
				return fmt.Errorf("invalid status: %s (valid: pending, running, completed, failed, interrupted)", newStatus)
			}

			// Check if file exists
			if _, err := os.Stat(jobFilePath); os.IsNotExist(err) {
				return fmt.Errorf("job file not found: %s", jobFilePath)
			}

			// Read the file
			content, err := os.ReadFile(jobFilePath)
			if err != nil {
				return fmt.Errorf("failed to read job file: %w", err)
			}

			contentStr := string(content)

			// Parse frontmatter to find current status
			lines := strings.Split(contentStr, "\n")
			inFrontmatter := false
			statusLineIdx := -1
			currentStatus := ""

			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed == "---" {
					if !inFrontmatter {
						inFrontmatter = true
						continue
					} else {
						break
					}
				}

				if inFrontmatter && strings.HasPrefix(trimmed, "status:") {
					statusLineIdx = i
					parts := strings.SplitN(trimmed, ":", 2)
					if len(parts) == 2 {
						currentStatus = strings.TrimSpace(parts[1])
					}
					break
				}
			}

			if statusLineIdx == -1 {
				return fmt.Errorf("no status field found in frontmatter")
			}

			fmt.Printf("Changing status from '%s' to '%s' in %s\n", currentStatus, newStatus, jobFilePath)

			// Update the status line
			lines[statusLineIdx] = fmt.Sprintf("status: %s", newStatus)
			newContent := strings.Join(lines, "\n")

			// Write back to file
			if err := os.WriteFile(jobFilePath, []byte(newContent), 0644); err != nil {
				return fmt.Errorf("failed to write job file: %w", err)
			}

			fmt.Printf("Successfully updated status to '%s'\n", newStatus)

			return nil
		},
	}

	return cmd
}
