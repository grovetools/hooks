package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/process"
	coresessions "github.com/mattsolo1/grove-core/pkg/sessions"
	"github.com/mattsolo1/grove-flow/pkg/orchestration"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/mattsolo1/grove-hooks/internal/utils"
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

			// Clean up zombie grove-flow jobs (interactive jobs with no live session)
			cleanedJobs, err := CleanupZombieFlowJobs(false)
			if err != nil {
				return fmt.Errorf("flow job cleanup failed: %w", err)
			}

			// Output summary
			totalCleaned := cleanedSessions + cleanedJobs
			if totalCleaned > 0 {
				ulog.Success("Cleanup completed").
					Field("cleaned_sessions", cleanedSessions).
					Field("cleaned_jobs", cleanedJobs).
					Pretty(fmt.Sprintf("Cleaned up %d dead session(s) and %d zombie job(s)", cleanedSessions, cleanedJobs)).
					Emit()
			} else {
				ulog.Info("No cleanup needed").
					Pretty("No dead sessions or zombie jobs found").
					Emit()
			}

			return nil
		},
	}

	cmd.Flags().BoolP("verbose", "v", false, "Show verbose output")
	cmd.Flags().IntVar(&inactivityMinutes, "inactive-minutes", 30, "Minutes of inactivity before marking session as completed")

	return cmd
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
	groveSessionsDir := utils.ExpandPath("~/.grove/hooks/sessions")
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
							// Process is dead, this is a stale session
							if os.Getenv("GROVE_DEBUG") != "" {
								fmt.Printf("Found stale interactive session: %s (PID: %d)\n", sessionID, pid)
							}

							// Check if this is a flow job by reading metadata
							metadataFile := filepath.Join(sessionDir, "metadata.json")
							if metadataContent, err := os.ReadFile(metadataFile); err == nil {
								var metadata coresessions.SessionMetadata
								if err := json.Unmarshal(metadataContent, &metadata); err == nil {
									// Check if it's a flow job
									if (metadata.Type == "interactive_agent" || metadata.Type == "agent") && metadata.JobFilePath != "" {
										// This is a flow job - trigger auto-completion
										if os.Getenv("GROVE_DEBUG") != "" {
											fmt.Printf("Triggering auto-completion for dead flow job: %s\n", metadata.JobFilePath)
										}
										go func(jobPath, sessDir string) {
											cmd := exec.Command("grove", "flow", "plan", "complete", jobPath)
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
											// After completion, wait a bit for completion to finish
											// Session directory is now preserved as permanent historical record
											time.Sleep(10 * time.Second)
										}(metadata.JobFilePath, sessionDir)
										cleaned++
										continue // Skip the removal below
									}
								}
							}

							// Not a flow job, or couldn't read metadata - preserve as historical record
							// Session directories are now permanent and never deleted
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
			if session.PID > 0 && !process.IsProcessAlive(session.PID) {
				// Mark session as interrupted
				if err := storage.UpdateSessionStatus(session.ID, "interrupted"); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to update session %s: %v\n", session.ID, err)
					continue
				}
				cleaned++

				if os.Getenv("GROVE_DEBUG") != "" {
					fmt.Printf("Cleaned up DB session %s (PID %d was dead)\n", session.ID, session.PID)
				}
				continue
			}

			// For sessions without PID (old sessions before PID tracking), check filesystem
			if session.PID == 0 {
				sessionDir := filepath.Join(groveSessionsDir, session.ID)
				pidFile := filepath.Join(sessionDir, "pid.lock")

				// If no pid.lock file exists, this is a zombie session
				if _, err := os.Stat(pidFile); os.IsNotExist(err) {
					// Mark session as interrupted (no filesystem tracking)
					if err := storage.UpdateSessionStatus(session.ID, "interrupted"); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to update session %s: %v\n", session.ID, err)
						continue
					}
					cleaned++

					if os.Getenv("GROVE_DEBUG") != "" {
						fmt.Printf("Cleaned up zombie session %s (no PID tracking)\n", session.ID)
					}
					continue
				}
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

// CleanupStaleFlowJobs is deprecated and has been superseded by CleanupZombieFlowJobs.
// This function is kept for backwards compatibility but does nothing.
// Use CleanupZombieFlowJobs(dryRun bool) instead for zombie job cleanup.
//
// Deprecated: Use CleanupZombieFlowJobs instead.
func CleanupStaleFlowJobs() (int, error) {
	return 0, nil
}

// updateJobStatusInFile updates a job file's status using orchestration.UpdateFrontmatter
func updateJobStatusInFile(jobFilePath, newStatus string) error {
	content, err := os.ReadFile(jobFilePath)
	if err != nil {
		return fmt.Errorf("failed to read job file: %w", err)
	}

	updatedContent, err := orchestration.UpdateFrontmatter(content, map[string]interface{}{
		"status": newStatus,
	})
	if err != nil {
		return fmt.Errorf("failed to update frontmatter: %w", err)
	}

	return os.WriteFile(jobFilePath, updatedContent, 0644)
}

// CleanupZombieFlowJobs finds interactive jobs (chat, interactive_agent) that are in a non-terminal
// state but have no corresponding live session, and marks them as 'interrupted'.
// Returns the count of jobs that were (or would be in dry-run mode) successfully updated.
func CleanupZombieFlowJobs(dryRun bool) (int, error) {
	updatedCount := 0
	ulog := grovelogging.NewUnifiedLogger("grove-hooks.cleanup-zombies")

	// 1. Discover all live interactive sessions to identify which jobs are actually running.
	storage, err := disk.NewSQLiteStore()
	if err != nil {
		return 0, fmt.Errorf("failed to create storage for zombie cleanup: %w", err)
	}
	defer storage.(*disk.SQLiteStore).Close()

	liveSessions, err := DiscoverLiveInteractiveSessions(storage)
	if err != nil {
		return 0, fmt.Errorf("failed to discover live sessions for zombie cleanup: %w", err)
	}

	// Use shared helper to build the live job paths map
	liveJobFilePaths := BuildLiveJobFilePathsMap(liveSessions)

	// 2. Discover all flow jobs from disk.
	flowJobs, err := DiscoverFlowJobs()
	if err != nil {
		return 0, fmt.Errorf("failed to discover flow jobs for zombie cleanup: %w", err)
	}

	// 3. Identify and update zombies using shared helper.
	for _, job := range flowJobs {
		if !IsZombieJob(job, liveJobFilePaths) {
			continue
		}

		// This is a zombie job.
		if dryRun {
			ulog.Info("Would mark as interrupted").
				Field("job_file", job.JobFilePath).
				Field("current_status", job.Status).
				Pretty(fmt.Sprintf("Would update: %s (status: %s)", job.JobFilePath, job.Status)).
				Emit()
			updatedCount++
		} else {
			if err := updateJobStatusInFile(job.JobFilePath, "interrupted"); err != nil {
				ulog.Warn("Failed to update zombie job file").
					Field("job_file", job.JobFilePath).
					Err(err).
					Emit()
				// Don't increment count on failure - only count successful updates
				continue
			}
			ulog.Info("Marked as interrupted").
				Field("job_file", job.JobFilePath).
				Pretty(fmt.Sprintf("Updated: %s", job.JobFilePath)).
				Emit()
			updatedCount++
		}
	}

	return updatedCount, nil
}
