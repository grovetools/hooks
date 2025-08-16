package commands

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/spf13/cobra"
)

func NewCleanupCmd() *cobra.Command {
	var inactivityMinutes int
	
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up inactive sessions",
		Long:  `Check all running and idle sessions and mark those that have been inactive for too long as completed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Run cleanup with custom threshold
			cleaned, err := CleanupDeadSessionsWithThreshold(storage, time.Duration(inactivityMinutes)*time.Minute)
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
	cmd.Flags().IntVar(&inactivityMinutes, "inactive-minutes", 30, "Minutes of inactivity before marking session as completed")
	
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
	
	// Debug logging
	if os.Getenv("GROVE_DEBUG") != "" {
		fmt.Printf("Checking PID %d: err=%v\n", pid, err)
	}
	
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
// Uses default 30 minute inactivity threshold
func CleanupDeadSessions(storage interfaces.SessionStorer) (int, error) {
	return CleanupDeadSessionsWithThreshold(storage, 30*time.Minute)
}

// CleanupDeadSessionsWithThreshold checks all running/idle sessions and marks inactive ones as completed
// Returns the number of sessions cleaned up
func CleanupDeadSessionsWithThreshold(storage interfaces.SessionStorer, inactivityThreshold time.Duration) (int, error) {
	// Get all sessions
	sessions, err := storage.GetAllSessions()
	if err != nil {
		return 0, fmt.Errorf("failed to get sessions: %w", err)
	}

	cleaned := 0
	now := time.Now()
	
	for _, session := range sessions {
		// Skip oneshot jobs - they are managed by grove-flow
		if session.Type == "oneshot_job" {
			continue
		}
		
		// For running/idle sessions, check if still active
		if session.Status == "running" || session.Status == "idle" {
			// First check if process is dead (quick check)
			if session.PID > 0 && !isProcessAlive(session.PID) {
				// Mark session as completed
				if err := storage.UpdateSessionStatus(session.ID, "completed"); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to update session %s: %v\n", session.ID, err)
					continue
				}
				cleaned++
				
				if os.Getenv("GROVE_DEBUG") != "" {
					fmt.Printf("Cleaned up session %s (PID %d was dead)\n", session.ID, session.PID)
				}
				continue
			}
			
			// Then check if session has been inactive for too long
			timeSinceActivity := now.Sub(session.LastActivity)
			if timeSinceActivity > inactivityThreshold {
				// Mark session as completed due to inactivity
				if err := storage.UpdateSessionStatus(session.ID, "completed"); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to update session %s: %v\n", session.ID, err)
					continue
				}
				cleaned++
				
				if os.Getenv("GROVE_DEBUG") != "" {
					fmt.Printf("Cleaned up inactive session %s (inactive for %v)\n", session.ID, timeSinceActivity)
				}
			}
		}
	}

	return cleaned, nil
}