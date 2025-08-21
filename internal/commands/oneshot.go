package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/mattsolo1/grove-notifications"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
			
			log.Printf("Oneshot start received - JobID: %s, Status: %s, JobTitle: %s", data.JobID, data.Status, data.JobTitle)

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

			// Set status from input, default to running
			status := data.Status
			if status == "" {
				status = "running"
			}

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

			// Send notification if pending user input
			if status == "pending_user" {
				log.Printf("Job %s started with pending_user status, sending notification", data.JobID)
				sendOneshotNtfyNotification(storage, data.JobID, status)
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

			// Send notification on completion or failure
			log.Printf("Job %s stop received with status: %s", data.JobID, data.Status)
			if data.Status == "completed" || data.Status == "failed" || data.Status == "success" {
				log.Printf("Job %s %s, sending notification", data.JobID, data.Status)
				sendOneshotNtfyNotification(storage, data.JobID, data.Status)
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

// expandPath expands ~ to home directory (copied from internal/hooks/context.go)
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// sendOneshotNtfyNotification sends a ntfy notification for a oneshot job status change.
func sendOneshotNtfyNotification(storage interfaces.SessionStorer, sessionID, jobStatus string) {
	log.Printf("sendOneshotNtfyNotification called for job %s with status %s", sessionID, jobStatus)
	
	// Get notification settings from config
	configPath := expandPath("~/.config/canopy/config.yaml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		// Fail silently if config doesn't exist
		return
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(configData, &config); err != nil {
		log.Printf("Failed to parse config for ntfy: %v", err)
		return
	}

	ntfyConfig, ok := config["notifications"].(map[string]interface{})
	if !ok {
		return
	}

	ntfy, ok := ntfyConfig["ntfy"].(map[string]interface{})
	if !ok {
		return
	}

	enabled, _ := ntfy["enabled"].(bool)
	if !enabled {
		return
	}

	topic, _ := ntfy["topic"].(string)
	if topic == "" {
		return
	}

	// Get session info
	sessionData, err := storage.GetSession(sessionID)
	if err != nil {
		log.Printf("Failed to get session for ntfy notification: %v", err)
		return
	}

	// GetSession always returns *ExtendedSession from SQLiteStore
	extSession, ok := sessionData.(*disk.ExtendedSession)
	if !ok {
		log.Printf("Failed to cast session for ntfy notification: got %T", sessionData)
		return
	}
	session := &extSession.Session

	ntfyURL := "https://ntfy.sh"
	if url, ok := ntfy["url"].(string); ok && url != "" {
		ntfyURL = url
	}

	// Prepare notification message
	var title, message string
	
	// Get contextName from ExtendedSession
	contextName := extSession.JobTitle
	if contextName == "" {
		contextName = extSession.PlanName
	}
	
	// Fallback to repo name if no job title or plan name
	if contextName == "" {
		contextName = session.Repo
	}

	switch jobStatus {
	case "completed", "success":
		title = "Job Completed"
		message = fmt.Sprintf("Job '%s' finished successfully.", contextName)
	case "failed":
		title = "Job Failed"
		message = fmt.Sprintf("Job '%s' failed.", contextName)
	case "pending_user":
		title = "Action Required"
		message = fmt.Sprintf("Job '%s' is waiting for your input.", contextName)
	default:
		log.Printf("Unknown job status for notification: %s", jobStatus)
		return // Don't send notification for other states
	}

	if err := notifications.SendNtfy(ntfyURL, topic, title, message, "default", []string{"job", jobStatus}); err != nil {
		log.Printf("Failed to send ntfy notification: %v", err)
	} else {
		log.Printf("Sent ntfy notification for job %s: %s", sessionID, message)
	}
}
