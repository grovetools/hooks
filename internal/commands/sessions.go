package commands

import (
	"encoding/json"
	"fmt"
	"os"
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
	
	return cmd
}

func newSessionsListCmd() *cobra.Command {
	var (
		statusFilter string
		jsonOutput   bool
		limit        int
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
			
			// Apply limit
			if limit > 0 && len(sessions) > limit {
				sessions = sessions[:limit]
			}
			
			// Output results
			if jsonOutput {
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(sessions)
			}
			
			// Table output
			if len(sessions) == 0 {
				fmt.Println("No sessions found")
				return nil
			}
			
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SESSION ID\tSTATUS\tREPO\tBRANCH\tUSER\tSTARTED\tDURATION")
			
			for _, s := range sessions {
				duration := "running"
				if s.EndedAt != nil {
					duration = s.EndedAt.Sub(s.StartedAt).Round(time.Second).String()
				} else if s.Status == "idle" {
					duration = "idle"
				}
				
				started := s.StartedAt.Format("2006-01-02 15:04:05")
				
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					truncate(s.ID, 12),
					s.Status,
					s.Repo,
					s.Branch,
					s.User,
					started,
					duration,
				)
			}
			
			return w.Flush()
		},
	}
	
	cmd.Flags().StringVarP(&statusFilter, "status", "s", "", "Filter by status (running, idle, completed, failed)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "Limit number of results")
	
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
			session, err := storage.GetSession(sessionID)
			if err != nil {
				return fmt.Errorf("failed to get session: %w", err)
			}
			
			// Output results
			if jsonOutput {
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(session)
			}
			
			// Detailed text output
			fmt.Printf("Session ID: %s\n", session.ID)
			fmt.Printf("Status: %s\n", session.Status)
			fmt.Printf("Repository: %s\n", session.Repo)
			fmt.Printf("Branch: %s\n", session.Branch)
			fmt.Printf("User: %s\n", session.User)
			fmt.Printf("Working Directory: %s\n", session.WorkingDirectory)
			fmt.Printf("PID: %d\n", session.PID)
			fmt.Printf("Started: %s\n", session.StartedAt.Format(time.RFC3339))
			
			if session.EndedAt != nil {
				fmt.Printf("Ended: %s\n", session.EndedAt.Format(time.RFC3339))
				fmt.Printf("Duration: %s\n", session.EndedAt.Sub(session.StartedAt).Round(time.Second))
			}
			
			if session.TmuxKey != "" {
				fmt.Printf("Tmux Key: %s\n", session.TmuxKey)
			}
			
			if session.ToolStats != nil {
				fmt.Printf("\nTool Statistics:\n")
				fmt.Printf("  Total Calls: %d\n", session.ToolStats.TotalCalls)
				fmt.Printf("  Bash Commands: %d\n", session.ToolStats.BashCommands)
				fmt.Printf("  File Modifications: %d\n", session.ToolStats.FileModifications)
				fmt.Printf("  File Reads: %d\n", session.ToolStats.FileReads)
				fmt.Printf("  Search Operations: %d\n", session.ToolStats.SearchOperations)
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