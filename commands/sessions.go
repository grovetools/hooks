package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/grovetools/core/config"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/process"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/hooks/internal/storage/disk"
	"github.com/grovetools/hooks/internal/utils"
	"github.com/spf13/cobra"
)

var ulog = grovelogging.NewUnifiedLogger("grove-hooks.commands")

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
	cmd.AddCommand(newMarkOldCompletedCmd())

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
				// Enhance sessions with comprehensive time-based fields
				type EnhancedSession struct {
					*models.Session
					DurationSeconds        *float64 `json:"duration_seconds,omitempty"`
					DurationHuman          string   `json:"duration_human,omitempty"`
					AgeSeconds             *float64 `json:"age_seconds,omitempty"`
					AgeHuman               string   `json:"age_human,omitempty"`
					LastActivitySecondsAgo *float64 `json:"last_activity_seconds_ago,omitempty"`
					LastActivityHuman      string   `json:"last_activity_human,omitempty"`
				}

				enhancedSessions := make([]EnhancedSession, len(sessions))
				now := time.Now()

				for i, s := range sessions {
					enhanced := EnhancedSession{Session: s}

					// Duration: total runtime for completed sessions
					if s.EndedAt != nil && !s.StartedAt.IsZero() {
						duration := s.EndedAt.Sub(s.StartedAt)
						durationSecs := duration.Seconds()
						enhanced.DurationSeconds = &durationSecs
						enhanced.DurationHuman = utils.FormatDuration(duration)
					}

					// Age: time since session was created
					if !s.StartedAt.IsZero() {
						age := now.Sub(s.StartedAt)
						ageSecs := age.Seconds()
						enhanced.AgeSeconds = &ageSecs
						enhanced.AgeHuman = utils.FormatDuration(age)
					}

					// LastActivity: time since last recorded activity
					if !s.LastActivity.IsZero() {
						lastActivity := now.Sub(s.LastActivity)
						lastActivitySecs := lastActivity.Seconds()
						enhanced.LastActivitySecondsAgo = &lastActivitySecs
						enhanced.LastActivityHuman = utils.FormatDuration(lastActivity)
					}

					enhancedSessions[i] = enhanced
				}

				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(enhancedSessions)
			}

			// Table output
			if len(sessions) == 0 {
				ulog.Info("No sessions found").Emit()
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SESSION ID\tTYPE\tSTATUS\tCONTEXT\tUSER\tAGE")

			for _, s := range sessions {
				// Use status from job file as source of truth
				displayStatus := s.Status

				// Calculate AGE with context-aware logic
				age := "n/a"
				now := time.Now()

				// Terminal states (completed, failed, interrupted, etc.)
				terminalStates := map[string]bool{
					"completed":   true,
					"failed":      true,
					"interrupted": true,
					"error":       true,
					"abandoned":   true,
				}

				if terminalStates[displayStatus] {
					// For terminal sessions, show total execution duration
					if s.EndedAt != nil && !s.StartedAt.IsZero() {
						age = utils.FormatDuration(s.EndedAt.Sub(s.StartedAt))
					}
				} else if displayStatus == "running" || displayStatus == "idle" || displayStatus == "pending_user" {
					// For active sessions, show time since last activity
					if !s.LastActivity.IsZero() {
						age = utils.FormatDuration(now.Sub(s.LastActivity))
					} else if !s.StartedAt.IsZero() {
						age = utils.FormatDuration(now.Sub(s.StartedAt))
					}
				} else if displayStatus == "pending" || displayStatus == "todo" {
					// For queued sessions, show time since creation
					if !s.StartedAt.IsZero() {
						age = utils.FormatDuration(now.Sub(s.StartedAt))
					}
				}

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

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					truncate(s.ID, 12),
					sessionType,
					displayStatus,
					truncate(context, 30),
					s.User,
					age,
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

			// Human-readable text output to stdout (data output, not logs)
			fmt.Printf("Session ID: %s\n", baseSession.ID)
			fmt.Printf("Type: %s\n", sessionType)
			fmt.Printf("Status: %s\n", baseSession.Status)
			fmt.Println()

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
			fmt.Println()

			fmt.Printf("User: %s\n", baseSession.User)
			fmt.Printf("Working Directory: %s\n", baseSession.WorkingDirectory)
			fmt.Printf("PID: %d\n", baseSession.PID)
			fmt.Println()

			fmt.Printf("Started: %s\n", baseSession.StartedAt.Format(time.RFC3339))

			if baseSession.EndedAt != nil {
				duration := baseSession.EndedAt.Sub(baseSession.StartedAt).Round(time.Second)
				fmt.Printf("Ended: %s\n", baseSession.EndedAt.Format(time.RFC3339))
				fmt.Printf("Duration: %s\n", duration)
			}

			if baseSession.TmuxKey != "" {
				fmt.Printf("Tmux Key: %s\n", baseSession.TmuxKey)
			}

			if baseSession.ToolStats != nil {
				fmt.Println()
				fmt.Println("Tool Statistics:")
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
				ulog.Info("No sessions to archive").Emit()
				return nil
			}

			// Archive the sessions
			if err := storage.ArchiveSessions(sessionIDs); err != nil {
				return fmt.Errorf("failed to archive sessions: %w", err)
			}

			ulog.Success("Sessions archived").
				Field("count", len(sessionIDs)).
				Pretty(fmt.Sprintf("Archived %d session(s)", len(sessionIDs))).
				Emit()
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


			// Discover all workspaces using grove-core
			discoveryService := workspace.NewDiscoveryService(nil)
			discoveryResult, err := discoveryService.DiscoverAll()
			if err != nil {
				ulog.Warn("Failed to discover workspaces").
					Err(err).
					Emit()
				return fmt.Errorf("failed to discover workspaces: %w", err)
			}

			provider := workspace.NewProvider(discoveryResult)

			// Load config and create notebook locator
			cfg, err := config.LoadDefault()
			if err != nil {
				ulog.Warn("Failed to load config").
					Err(err).
					Emit()
				cfg = &config.Config{}
			}
			locator := workspace.NewNotebookLocator(cfg)

			// Get all plan directories across all workspaces
			scannedDirs, err := locator.ScanForAllPlans(provider)
			if err != nil {
				return fmt.Errorf("failed to scan for plans: %w", err)
			}

			if len(scannedDirs) == 0 {
				ulog.Info("No plan directories found").Emit()
				return nil
			}

			// Process each base plans directory
			for _, scannedDir := range scannedDirs {
				plansBaseDir := scannedDir.Path

				// Check subdirectories of the plans directory (each is a plan)
				planEntries, err := os.ReadDir(plansBaseDir)
				if err != nil {
					ulog.Warn("Cannot read plans directory").
						Field("path", plansBaseDir).
						Err(err).
						Emit()
					continue
				}

				for _, planEntry := range planEntries {
					if !planEntry.IsDir() {
						continue
					}

					planDir := filepath.Join(plansBaseDir, planEntry.Name())
					count, err := markInterruptedJobsInPlan(planDir, dryRun)
					if err != nil {
						// Log error but continue
						ulog.Warn("Error processing plan").
							Field("plan_dir", planDir).
							Err(err).
							Emit()
						continue
					}
					updated += count
				}
			}

			if dryRun {
				ulog.Info("Dry run results").
					Field("would_update", updated).
					Pretty(fmt.Sprintf("Dry run: would update %d job(s)", updated)).
					Emit()
			} else {
				ulog.Success("Jobs updated to interrupted status").
					Field("updated_count", updated).
					Pretty(fmt.Sprintf("Updated %d job(s) to interrupted status", updated)).
					Emit()
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
			groveSessionsDir := utils.ExpandPath("~/.grove/hooks/sessions")
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
				ulog.Info("Session is not running").
					Field("session_id", sessionID).
					Field("pid", pid).
					Pretty(fmt.Sprintf("Session %s (PID %d) is not running", sessionID, pid)).
					Emit()
				// Clean up the stale directory
				if err := os.RemoveAll(sessionDir); err != nil {
					ulog.Warn("Failed to remove stale session directory").
						Field("session_dir", sessionDir).
						Err(err).
						Emit()
				} else {
					ulog.Success("Cleaned up stale session directory").
						Field("session_dir", sessionDir).
						Emit()
				}
				return nil
			}

			// Confirm before killing unless --force is used
			if !force {
				fmt.Printf("Kill session %s (PID %d)? [y/N] ", sessionID, pid)
				var response string
				fmt.Scanln(&response)
				if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
					ulog.Info("Kill operation cancelled").Emit()
					return nil
				}
			}

			// Kill the process using SIGTERM
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed to kill process: %w", err)
			}

			ulog.Success("Session killed").
				Field("session_id", sessionID).
				Field("pid", pid).
				Pretty(fmt.Sprintf("Killed session %s (PID %d)", sessionID, pid)).
				Emit()

			// Clean up the session directory
			if err := os.RemoveAll(sessionDir); err != nil {
				ulog.Warn("Failed to remove session directory").
					Field("session_dir", sessionDir).
					Err(err).
					Emit()
			} else {
				ulog.Success("Cleaned up session directory").
					Field("session_dir", sessionDir).
					Emit()
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
  grove-hooks sessions set-status /path/to/notebook/repos/my-repo/main/plans/my-plan/01-job.md interrupted`,
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

			ulog.Info("Changing job status").
				Field("job_file", jobFilePath).
				Field("old_status", currentStatus).
				Field("new_status", newStatus).
				Pretty(fmt.Sprintf("Changing status from '%s' to '%s' in %s", currentStatus, newStatus, jobFilePath)).
				Emit()

			// Update the status line
			lines[statusLineIdx] = fmt.Sprintf("status: %s", newStatus)
			newContent := strings.Join(lines, "\n")

			// Write back to file
			if err := os.WriteFile(jobFilePath, []byte(newContent), 0644); err != nil {
				return fmt.Errorf("failed to write job file: %w", err)
			}

			ulog.Success("Status updated successfully").
				Field("new_status", newStatus).
				Field("job_file", jobFilePath).
				Pretty(fmt.Sprintf("Successfully updated status to '%s'", newStatus)).
				Emit()

			return nil
		},
	}

	return cmd
}

func newMarkOldCompletedCmd() *cobra.Command {
	var (
		dryRun     bool
		beforeDate string
	)

	cmd := &cobra.Command{
		Use:   "mark-old-completed",
		Short: "Mark old jobs as completed",
		Long: `Bulk update old flow jobs and chats to mark them as completed.

By default, marks all jobs created before today as completed (unless already completed).
Use --before to specify a different cutoff date (format: YYYY-MM-DD).

Example:
  grove-hooks sessions mark-old-completed --dry-run
  grove-hooks sessions mark-old-completed --before 2025-10-01`,
		RunE: func(cmd *cobra.Command, args []string) error {

			// Determine cutoff date
			var cutoffDate time.Time
			var err error

			if beforeDate != "" {
				cutoffDate, err = time.Parse("2006-01-02", beforeDate)
				if err != nil {
					return fmt.Errorf("invalid date format (use YYYY-MM-DD): %w", err)
				}
			} else {
				// Default to today
				now := time.Now()
				cutoffDate = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			}

			ulog.Info("Cutoff date set").
				Field("cutoff_date", cutoffDate.Format("2006-01-02")).
				Pretty(fmt.Sprintf("🗓️  Cutoff date: %s", cutoffDate.Format("2006-01-02"))).
				Emit()
			if dryRun {
				ulog.Info("Dry run mode enabled").
					Pretty(" DRY RUN MODE - No changes will be made").
					Emit()
			}
			ulog.Info("").PrettyOnly().Pretty("").Emit()

			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Get all sessions
			sessions, err := GetAllSessions(storage, false)
			if err != nil {
				return fmt.Errorf("failed to get sessions: %w", err)
			}

			// Filter and collect sessions that need to be updated
			var toUpdate []*models.Session
			skipped := 0
			invalidDate := 0

			for _, session := range sessions {
				// Skip if already in a terminal state (completed, abandoned, failed, interrupted)
				terminalStates := map[string]bool{
					"completed":   true,
					"abandoned":   true,
					"failed":      true,
					"interrupted": true,
				}
				if terminalStates[session.Status] {
					skipped++
					continue
				}

				// Skip if no job file path
				if session.JobFilePath == "" {
					skipped++
					continue
				}

				// Check if file exists
				if _, err := os.Stat(session.JobFilePath); os.IsNotExist(err) {
					skipped++
					continue
				}

				// Skip if date is invalid/zero (year 1 is the zero value for time.Time)
				if session.StartedAt.Year() == 1 {
					invalidDate++
					continue
				}

				// Skip if started at or after cutoff date
				if !session.StartedAt.Before(cutoffDate) {
					skipped++
					continue
				}

				toUpdate = append(toUpdate, session)
			}

			// Sort by StartedAt descending (most recent first)
			sort.Slice(toUpdate, func(i, j int) bool {
				return toUpdate[i].StartedAt.After(toUpdate[j].StartedAt)
			})

			// Process and display sorted sessions
			updated := 0
			errors := 0
			for _, session := range toUpdate {
				ulog.Info("Processing job").
					Field("job_file", filepath.Base(session.JobFilePath)).
					Field("started_at", session.StartedAt.Format("2006-01-02")).
					Field("status", session.Status).
					Pretty(fmt.Sprintf(" %s (started: %s, status: %s)",
						filepath.Base(session.JobFilePath),
						session.StartedAt.Format("2006-01-02"),
						session.Status)).
					Emit()

				if !dryRun {
					// Update the file
					if err := updateJobStatus(session.JobFilePath, "completed"); err != nil {
						ulog.Warn("Failed to update job").
							Field("job_file", session.JobFilePath).
							Err(err).
							Pretty(fmt.Sprintf("   WARNING:  Failed to update: %v", err)).
							Emit()
						errors++
						continue
					}
				}

				updated++
			}

			ulog.Info("").PrettyOnly().Pretty("").Emit()
			ulog.Info("Summary").Pretty(" Summary:").Emit()
			if dryRun {
				ulog.Info("Dry run summary").
					Field("would_mark_completed", updated).
					Pretty(fmt.Sprintf("   Would mark as completed: %d jobs", updated)).
					Emit()
			} else {
				ulog.Success("Jobs marked as completed").
					Field("completed_count", updated).
					Pretty(fmt.Sprintf("   Marked as completed: %d jobs", updated)).
					Emit()
			}
			ulog.Info("Skipped jobs").
				Field("skipped_count", skipped).
				Pretty(fmt.Sprintf("   Skipped: %d jobs (already in terminal state or from today)", skipped)).
				Emit()
			if invalidDate > 0 {
				ulog.Info("Skipped jobs with invalid dates").
					Field("invalid_date_count", invalidDate).
					Pretty(fmt.Sprintf("   Skipped: %d jobs (invalid/missing date)", invalidDate)).
					Emit()
			}
			if errors > 0 {
				ulog.Warn("Jobs with errors").
					Field("error_count", errors).
					Pretty(fmt.Sprintf("   WARNING:  Errors: %d jobs (missing status field or other issues)", errors)).
					Emit()
			}

			if dryRun {
				ulog.Info("").PrettyOnly().Pretty("").Emit()
				ulog.Info("Dry run tip").
					Pretty("Tip: Run without --dry-run to actually update the files").
					Emit()
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be updated without making changes")
	cmd.Flags().StringVar(&beforeDate, "before", "", "Mark jobs created before this date as completed (format: YYYY-MM-DD, default: today)")

	return cmd
}

// updateJobStatus updates the status field in a job file's frontmatter
func updateJobStatus(jobFilePath, newStatus string) error {
	// Read the file
	content, err := os.ReadFile(jobFilePath)
	if err != nil {
		return fmt.Errorf("failed to read job file: %w", err)
	}

	contentStr := string(content)

	// Parse frontmatter to find status line
	lines := strings.Split(contentStr, "\n")
	inFrontmatter := false
	statusLineIdx := -1

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
			break
		}
	}

	if statusLineIdx == -1 {
		return fmt.Errorf("no status field found in frontmatter")
	}

	// Update the status line
	lines[statusLineIdx] = fmt.Sprintf("status: %s", newStatus)
	newContent := strings.Join(lines, "\n")

	// Write back to file
	if err := os.WriteFile(jobFilePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write job file: %w", err)
	}

	return nil
}
