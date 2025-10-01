package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
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
		Short: "List all sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Clean up dead sessions first
			_, _ = CleanupDeadSessions(storage)

			// Get all sessions
			sessions, err := storage.GetAllSessions()
			if err != nil {
				return fmt.Errorf("failed to get sessions: %w", err)
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
					if sessionType == "" {
						sessionType = "claude"
					}
					// Normalize job type names
					if sessionType == "oneshot_job" && typeFilter == "job" {
						filtered = append(filtered, s)
					} else if sessionType == typeFilter {
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			// Hide completed sessions if requested
			if hideCompleted {
				var filtered []*models.Session
				for _, s := range sessions {
					if s.Status != "completed" && s.Status != "failed" && s.Status != "error" {
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			// Sort sessions: running first, then idle, then others by started_at desc
			sort.Slice(sessions, func(i, j int) bool {
				// Define status priority: running=1, idle=2, others=3
				iPriority := 3
				if sessions[i].Status == "running" {
					iPriority = 1
				} else if sessions[i].Status == "idle" {
					iPriority = 2
				}

				jPriority := 3
				if sessions[j].Status == "running" {
					jPriority = 1
				} else if sessions[j].Status == "idle" {
					jPriority = 2
				}

				// Sort by priority first
				if iPriority != jPriority {
					return iPriority < jPriority
				}

				// Within same status group, sort by most recent first
				return sessions[i].StartedAt.After(sessions[j].StartedAt)
			})

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

				// Format context based on session type
				context := ""
				sessionType := s.Type
				if sessionType == "" {
					sessionType = "claude"
				}
				if s.Type == "oneshot_job" {
					sessionType = "job"
					// For jobs, show repo/branch like Claude sessions
					if s.Repo != "" && s.Branch != "" {
						context = fmt.Sprintf("%s/%s", s.Repo, s.Branch)
						// Optionally append title if it fits
						if s.JobTitle != "" && len(context)+len(s.JobTitle)+3 <= 30 {
							context = fmt.Sprintf("%s (%s)", context, s.JobTitle)
						}
					} else if s.Repo != "" {
						context = s.Repo
						if s.JobTitle != "" && len(context)+len(s.JobTitle)+3 <= 30 {
							context = fmt.Sprintf("%s (%s)", context, s.JobTitle)
						}
					} else if s.PlanName != "" {
						// Show plan name when no repo info
						context = s.PlanName
						if s.JobTitle != "" && len(context)+len(s.JobTitle)+3 <= 30 {
							context = fmt.Sprintf("%s (%s)", context, s.JobTitle)
						}
					} else if s.JobTitle != "" {
						// Fallback to title alone
						context = s.JobTitle
					} else {
						context = "oneshot"
					}
				} else {
					// Claude session
					if s.Repo != "" && s.Branch != "" {
						context = fmt.Sprintf("%s/%s", s.Repo, s.Branch)
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

			// Detailed text output
			fmt.Printf("Session ID: %s\n", baseSession.ID)
			fmt.Printf("Type: %s\n", sessionType)
			fmt.Printf("Status: %s\n", baseSession.Status)

			if sessionType == "oneshot_job" {
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
