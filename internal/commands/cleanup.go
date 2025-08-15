package commands

import (
	"fmt"
	"os"
	"syscall"

	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/spf13/cobra"
)

func NewCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up sessions with dead processes",
		Long:  `Check all running and idle sessions and mark those with dead processes as completed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Run cleanup
			cleaned, err := CleanupDeadSessions(storage)
			if err != nil {
				return fmt.Errorf("cleanup failed: %w", err)
			}

			// Output summary
			if cleaned > 0 {
				fmt.Printf("Cleaned up %d dead session(s)\n", cleaned)
			} else {
				fmt.Println("No dead sessions found")
			}

			return nil
		},
	}

	cmd.Flags().BoolP("verbose", "v", false, "Show verbose output")
	
	return cmd
}

// isProcessAlive checks if a process with the given PID is still running
func isProcessAlive(pid int) bool {
	// PID 0 is invalid
	if pid <= 0 {
		return false
	}

	// Try to send signal 0 to the process
	// This doesn't actually send a signal but checks if we can
	err := syscall.Kill(pid, 0)
	
	// If no error, process exists
	if err == nil {
		return true
	}
	
	// If error is ESRCH (no such process), process doesn't exist
	if err == syscall.ESRCH {
		return false
	}
	
	// For other errors (like EPERM - permission denied), 
	// assume process exists but we can't access it
	return true
}

// CleanupDeadSessions checks all running/idle sessions and marks dead ones as completed
// Returns the number of sessions cleaned up
func CleanupDeadSessions(storage interfaces.SessionStorer) (int, error) {
	// Get all sessions
	sessions, err := storage.GetAllSessions()
	if err != nil {
		return 0, fmt.Errorf("failed to get sessions: %w", err)
	}

	cleaned := 0
	for _, session := range sessions {
		// Only check running or idle sessions
		if session.Status != "running" && session.Status != "idle" {
			continue
		}

		// Check if process is still alive
		if !isProcessAlive(session.PID) {
			// Mark session as completed
			if err := storage.UpdateSessionStatus(session.ID, "completed"); err != nil {
				// Log error but continue
				fmt.Fprintf(os.Stderr, "Warning: failed to update session %s: %v\n", session.ID, err)
				continue
			}
			cleaned++
			
			// Debug logging
			if os.Getenv("GROVE_DEBUG") != "" {
				fmt.Printf("Cleaned up session %s (PID %d was dead)\n", session.ID, session.PID)
			}
		}
	}

	return cleaned, nil
}