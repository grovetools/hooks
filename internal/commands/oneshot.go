package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/spf13/cobra"
)

// OneshotStartInput defines the JSON payload for starting a job
type OneshotStartInput struct {
	JobID         string `json:"job_id"`
	PlanName      string `json:"plan_name"`
	PlanDirectory string `json:"plan_directory"`
	JobTitle      string `json:"job_title"`
	JobFilePath   string `json:"job_file_path"`
	Repository    string `json:"repository"`
	Branch        string `json:"branch"`
	Status        string `json:"status"`
}

// OneshotStopInput defines the JSON payload for stopping a job
type OneshotStopInput struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"` // "completed" or "failed"
	Error  string `json:"error,omitempty"`
}

// NewOneshotCmd creates the oneshot command with start and stop subcommands
func NewOneshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oneshot",
		Short: "Manage oneshot job lifecycle from grove-flow",
	}
	cmd.AddCommand(newOneshotStartCmd())
	cmd.AddCommand(newOneshotStopCmd())
	return cmd
}

func newOneshotStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Signal the start of a oneshot job",
		Run: func(cmd *cobra.Command, args []string) {
			// Read JSON from stdin
			input, err := io.ReadAll(os.Stdin)
			if err != nil {
				log.Printf("Error reading input: %v", err)
				os.Exit(1)
			}

			var data OneshotStartInput
			if err := json.Unmarshal(input, &data); err != nil {
				log.Printf("Error parsing JSON: %v", err)
				os.Exit(1)
			}

			// Create storage directly instead of using hook context
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				log.Printf("Error creating storage: %v", err)
				os.Exit(1)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Get current user
			user := os.Getenv("USER")
			if user == "" {
				user = "unknown"
			}

			// Get working directory
			workingDir, _ := os.Getwd()

			now := time.Now()

			// Always set status to running on start.
			// The status from grove-flow might be an internal state like "pending_user"
			// which isn't relevant for the hooks' session tracking.
			status := "running"

			session := &disk.ExtendedSession{
				Session: models.Session{
					ID:               data.JobID,
					PID:              os.Getpid(),
					Repo:             data.Repository,
					Branch:           data.Branch,
					Status:           status,
					StartedAt:        now,
					LastActivity:     now,
					User:             user,
					WorkingDirectory: workingDir,
				},
				Type:          "oneshot_job",
				PlanName:      data.PlanName,
				PlanDirectory: data.PlanDirectory,
				JobTitle:      data.JobTitle,
				JobFilePath:   data.JobFilePath,
			}

			if err := storage.EnsureSessionExists(session); err != nil {
				log.Printf("Failed to record job start: %v", err)
				os.Exit(1)
			}

			fmt.Printf("Started tracking oneshot job: %s\n", data.JobID)
		},
	}
}

func newOneshotStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Signal the end of a oneshot job",
		Run: func(cmd *cobra.Command, args []string) {
			// Read JSON from stdin
			input, err := io.ReadAll(os.Stdin)
			if err != nil {
				log.Printf("Error reading input: %v", err)
				os.Exit(1)
			}

			var data OneshotStopInput
			if err := json.Unmarshal(input, &data); err != nil {
				log.Printf("Error parsing JSON: %v", err)
				os.Exit(1)
			}

			// Create storage directly instead of using hook context
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				log.Printf("Error creating storage: %v", err)
				os.Exit(1)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Update the session status with error if present
			if err := storage.(*disk.SQLiteStore).UpdateSessionStatusWithError(data.JobID, data.Status, data.Error); err != nil {
				log.Printf("Failed to update job status: %v", err)
				os.Exit(1)
			}

			// If there's an error, log it as a notification
			if data.Error != "" && data.Status == "failed" {
				notification := &models.ClaudeNotification{
					Type:    "job_failed",
					Message: fmt.Sprintf("Oneshot Job Failed: %s", data.Error),
					Level:   "error",
				}
				if err := storage.LogNotification(data.JobID, notification); err != nil {
					log.Printf("Failed to log job error notification: %v", err)
				}
			}

			fmt.Printf("Updated oneshot job %s status to: %s\n", data.JobID, data.Status)
		},
	}
}
