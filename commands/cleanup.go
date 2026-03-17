package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/process"
	coresessions "github.com/grovetools/core/pkg/sessions"
	"github.com/grovetools/core/util/delegation"
	"github.com/spf13/cobra"
)

func NewCleanupCmd() *cobra.Command {
	var inactivityMinutes int

	ulog := grovelogging.NewUnifiedLogger("grove-hooks.cleanup")

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up inactive sessions",
		Long:  `Check all running and idle sessions and mark those that have been inactive for too long as completed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Phase 3: Check if daemon is running
			// If the daemon is active, it owns session lifecycle management and cleanup.
			client := daemon.New()
			defer client.Close()

			if client.IsRunning() {
				ulog.Info("Daemon Active").
					Pretty("The Grove Daemon is running and managing session lifecycle automatically.").
					Emit()
				return nil
			}

			// Fallback: Daemon not running, perform manual cleanup
			ulog.Info("Local Cleanup").
				Pretty("Daemon not active. Performing manual cleanup...").
				Emit()

			// Run cleanup with custom threshold (filesystem-only, no SQLite)
			cleanedSessions, err := CleanupDeadSessionsWithThreshold(time.Duration(inactivityMinutes) * time.Minute)
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
				ulog.Success("Cleanup completed").
					Field("cleaned_sessions", cleanedSessions).
					Field("cleaned_jobs", cleanedJobs).
					Pretty(fmt.Sprintf("Cleaned up %d dead session(s) and %d stale job(s)", cleanedSessions, cleanedJobs)).
					Emit()
			} else {
				ulog.Info("No cleanup needed").
					Pretty("No dead sessions or stale jobs found").
					Emit()
			}

			return nil
		},
	}

	cmd.Flags().BoolP("verbose", "v", false, "Show verbose output")
	cmd.Flags().IntVar(&inactivityMinutes, "inactive-minutes", 30, "Minutes of inactivity before marking session as completed")

	return cmd
}

// CleanupDeadSessions checks filesystem session directories for dead processes.
func CleanupDeadSessions() (int, error) {
	return CleanupDeadSessionsWithThreshold(30 * time.Minute)
}

// CleanupDeadSessionsWithThreshold checks filesystem session directories for dead processes.
// The daemon is authoritative for live state; this only handles filesystem PID cleanup.
func CleanupDeadSessionsWithThreshold(inactivityThreshold time.Duration) (int, error) {
	cleaned := 0

	// Clean up stale interactive Claude session directories
	groveSessionsDir := filepath.Join(paths.StateDir(), "hooks", "sessions")
	if _, err := os.Stat(groveSessionsDir); err == nil {
		entries, err := os.ReadDir(groveSessionsDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}

				sessionID := entry.Name()
				sessionDir := filepath.Join(groveSessionsDir, sessionID)
				pidFile := filepath.Join(sessionDir, "pid.lock")

				// Read PID from lock file
				if content, err := os.ReadFile(pidFile); err == nil {
					var pid int
					if _, err := fmt.Sscanf(string(content), "%d", &pid); err == nil {
						if !process.IsProcessAlive(pid) {
							if os.Getenv("GROVE_DEBUG") != "" {
								fmt.Printf("Found stale interactive session: %s (PID: %d)\n", sessionID, pid)
							}

							// Check if this is a flow job by reading metadata
							metadataFile := filepath.Join(sessionDir, "metadata.json")
							if metadataContent, err := os.ReadFile(metadataFile); err == nil {
								var metadata coresessions.SessionMetadata
								if err := json.Unmarshal(metadataContent, &metadata); err == nil {
									if (metadata.Type == "interactive_agent" || metadata.Type == "isolated_agent" || metadata.Type == "agent") && metadata.JobFilePath != "" {
										if os.Getenv("GROVE_DEBUG") != "" {
											fmt.Printf("Triggering auto-completion for dead flow job: %s\n", metadata.JobFilePath)
										}
										go func(jobPath string) {
											cmd := delegation.Command("flow", "plan", "complete", jobPath)
											if os.Getenv("GROVE_DEBUG") != "" {
												output, err := cmd.CombinedOutput()
												if err != nil {
													fmt.Fprintf(os.Stderr, "Debug: Auto-completion of dead flow job %s failed: %v\nOutput: %s\n", jobPath, err, string(output))
												} else {
													fmt.Fprintf(os.Stderr, "Debug: Auto-completed dead flow job %s\n", jobPath)
												}
											} else {
												cmd.Run()
											}
											time.Sleep(10 * time.Second)
										}(metadata.JobFilePath)
										cleaned++
										continue
									}
								}
							}

							// Not a flow job — preserve as historical record
							cleaned++
							if os.Getenv("GROVE_DEBUG") != "" {
								fmt.Printf("Marked stale session as historical: %s\n", sessionDir)
							}
						}
					}
				}
			}
		}
	}

	return cleaned, nil
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
