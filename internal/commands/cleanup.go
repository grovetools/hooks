package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/process"
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
			cleanedSessions, err := CleanupDeadSessionsWithThreshold(storage, time.Duration(inactivityMinutes)*time.Minute)
			if err != nil {
				return fmt.Errorf("cleanup failed: %w", err)
			}

			// Clean up stale grove-flow jobs
			cleanedJobs, err := CleanupStaleFlowJobs()
			if err != nil {
				return fmt.Errorf("flow job cleanup failed: %w", err)
			}

			// Output summary
			totalCleaned := cleanedSessions + cleanedJobs
			if totalCleaned > 0 {
				fmt.Printf("Cleaned up %d dead session(s) and %d stale job(s)\n", cleanedSessions, cleanedJobs)
			} else {
				fmt.Println("No dead sessions or stale jobs found")
			}

			return nil
		},
	}

	cmd.Flags().BoolP("verbose", "v", false, "Show verbose output")
	cmd.Flags().IntVar(&inactivityMinutes, "inactive-minutes", 30, "Minutes of inactivity before marking session as completed")

	return cmd
}

// isProcessAlive is now a wrapper around the grove-core utility.
func isProcessAlive(pid int) bool {
	return process.IsProcessAlive(pid)
}

// CleanupDeadSessions checks all running/idle sessions and marks dead ones as completed
// Uses default 30 minute inactivity threshold
func CleanupDeadSessions(storage interfaces.SessionStorer) (int, error) {
	return CleanupDeadSessionsWithThreshold(storage, 30*time.Minute)
}

// CleanupDeadSessionsWithThreshold checks all running/idle sessions and marks inactive ones as completed
// Returns the number of sessions cleaned up
func CleanupDeadSessionsWithThreshold(storage interfaces.SessionStorer, inactivityThreshold time.Duration) (int, error) {
	cleaned := 0

	// 1. Clean up stale interactive Claude session directories
	claudeSessionsDir := expandPath("~/.claude/sessions")
	if _, err := os.Stat(claudeSessionsDir); err == nil {
		entries, err := os.ReadDir(claudeSessionsDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}

				sessionID := entry.Name()
				sessionDir := filepath.Join(claudeSessionsDir, sessionID)
				pidFile := filepath.Join(sessionDir, "pid.lock")

				// Read PID from lock file
				if content, err := os.ReadFile(pidFile); err == nil {
					var pid int
					if _, err := fmt.Sscanf(string(content), "%d", &pid); err == nil {
						if !process.IsProcessAlive(pid) {
							// Process is dead, this is a stale session
							if os.Getenv("GROVE_DEBUG") != "" {
								fmt.Printf("Found stale interactive session: %s (PID: %d)\n", sessionID, pid)
							}

							// Remove the directory
							if err := os.RemoveAll(sessionDir); err == nil {
								cleaned++
								if os.Getenv("GROVE_DEBUG") != "" {
									fmt.Printf("Cleaned up stale session directory: %s\n", sessionDir)
								}
							} else if os.Getenv("GROVE_DEBUG") != "" {
								fmt.Fprintf(os.Stderr, "Warning: failed to remove session directory %s: %v\n", sessionDir, err)
							}
						}
					}
				}
			}
		}
	}

	// 2. Clean up old database entries (for backwards compatibility during transition)
	sessions, err := storage.GetAllSessions()
	if err != nil {
		return cleaned, fmt.Errorf("failed to get sessions: %w", err)
	}

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
					fmt.Printf("Cleaned up DB session %s (PID %d was dead)\n", session.ID, session.PID)
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

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(os.Getenv("HOME"), path[2:])
	}
	return path
}

// CleanupStaleFlowJobs discovers grove-flow jobs with stale lock files and updates their status
// Returns the number of jobs cleaned up
// Note: This function is maintained for backwards compatibility but the actual
// grove-flow job cleanup logic is handled by grove-flow itself now
func CleanupStaleFlowJobs() (int, error) {
	// This functionality has been moved to grove-flow Phase 1-3
	// and is handled by the PID-based lock file mechanism
	// We keep this function for backwards compatibility but it's essentially a no-op now
	return 0, nil
}
