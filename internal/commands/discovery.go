package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/process"
)

// SessionMetadata represents the metadata.json structure for file-based sessions
type SessionMetadata struct {
	SessionID           string    `json:"session_id"`
	PID                 int       `json:"pid"`
	Repo                string    `json:"repo,omitempty"`
	Branch              string    `json:"branch,omitempty"`
	TmuxKey             string    `json:"tmux_key,omitempty"`
	WorkingDirectory    string    `json:"working_directory"`
	User                string    `json:"user"`
	StartedAt           time.Time `json:"started_at"`
	TranscriptPath      string    `json:"transcript_path,omitempty"`
	ProjectName         string    `json:"project_name,omitempty"`
	IsWorktree          bool      `json:"is_worktree,omitempty"`
	ParentEcosystemPath string    `json:"parent_ecosystem_path,omitempty"`
}

// DiscoverLiveClaudeSessions scans ~/.grove/hooks/sessions/ directory and returns live sessions
// A session is considered live if its PID is still alive
func DiscoverLiveClaudeSessions() ([]*models.Session, error) {
	groveSessionsDir := expandPath("~/.grove/hooks/sessions")

	// Check if directory exists
	if _, err := os.Stat(groveSessionsDir); os.IsNotExist(err) {
		return []*models.Session{}, nil
	}

	entries, err := os.ReadDir(groveSessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var sessions []*models.Session

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		sessionDir := filepath.Join(groveSessionsDir, sessionID)
		pidFile := filepath.Join(sessionDir, "pid.lock")
		metadataFile := filepath.Join(sessionDir, "metadata.json")

		// Read PID
		pidContent, err := os.ReadFile(pidFile)
		if err != nil {
			// Skip if we can't read PID file
			continue
		}

		var pid int
		if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err != nil {
			// Skip if PID is invalid
			continue
		}

		// Check if process is alive
		isAlive := process.IsProcessAlive(pid)

		// Read metadata
		metadataContent, err := os.ReadFile(metadataFile)
		if err != nil {
			// Skip if we can't read metadata
			continue
		}

		var metadata SessionMetadata
		if err := json.Unmarshal(metadataContent, &metadata); err != nil {
			// Skip if metadata is invalid
			continue
		}

		// Determine status based on liveness
		status := "running"
		var endedAt *time.Time
		if !isAlive {
			status = "interrupted"
			now := time.Now()
			endedAt = &now
		}

		// Create session object
		session := &models.Session{
			ID:               sessionID,
			PID:              pid,
			Repo:             metadata.Repo,
			Branch:           metadata.Branch,
			TmuxKey:          metadata.TmuxKey,
			WorkingDirectory: metadata.WorkingDirectory,
			User:             metadata.User,
			Status:           status,
			StartedAt:        metadata.StartedAt,
			LastActivity:     metadata.StartedAt, // Use started time as last activity
			EndedAt:          endedAt,
			IsTest:           false,
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// DiscoverLiveFlowJobs scans for grove-flow plans and returns running jobs as sessions
// This allows unified display of both Claude sessions and grove-flow jobs
func DiscoverLiveFlowJobs() ([]*models.Session, error) {
	var sessions []*models.Session

	// Common locations to search for plans
	planDirs := []string{
		expandPath("~/Documents/nb/repos"),
		expandPath("~/Code/nb/repos"),
		expandPath("~/Code"),
	}

	for _, baseDir := range planDirs {
		if _, err := os.Stat(baseDir); os.IsNotExist(err) {
			continue
		}

		// Walk the directory tree looking for plan directories
		err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}

			// Look for "plans" directories
			if !info.IsDir() || info.Name() != "plans" {
				return nil
			}

			// Check subdirectories of the plans directory
			planEntries, err := os.ReadDir(path)
			if err != nil {
				return nil
			}

			for _, planEntry := range planEntries {
				if !planEntry.IsDir() {
					continue
				}

				planDir := filepath.Join(path, planEntry.Name())
				planSessions, err := discoverJobsInPlan(planDir)
				if err != nil {
					// Skip this plan if there's an error
					continue
				}

				sessions = append(sessions, planSessions...)
			}

			return filepath.SkipDir // Don't descend into plans directories
		})

		if err != nil {
			// Continue with other base directories
			continue
		}
	}

	return sessions, nil
}

// discoverJobsInPlan scans a single plan directory for running jobs
func discoverJobsInPlan(planDir string) ([]*models.Session, error) {
	var sessions []*models.Session

	entries, err := os.ReadDir(planDir)
	if err != nil {
		return nil, err
	}

	// Extract plan name and context from the directory path
	planName := filepath.Base(planDir)

	// Try to determine repo/branch from path
	// Path is typically: .../repos/REPO/main/plans/PLAN or .../REPO/.grove-worktrees/PLAN
	pathParts := strings.Split(planDir, string(filepath.Separator))
	var repo, branch string
	for i, part := range pathParts {
		if part == "repos" && i+2 < len(pathParts) {
			repo = pathParts[i+1]
			branch = pathParts[i+2]
			break
		} else if part == ".grove-worktrees" && i > 0 {
			repo = pathParts[i-1]
			branch = planName
			break
		}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()

		// Skip non-.md files
		if !strings.HasSuffix(filename, ".md") {
			continue
		}

		// Skip spec.md and other non-job files
		if filename == "spec.md" || filename == "README.md" {
			continue
		}

		filePath := filepath.Join(planDir, filename)

		// Read the frontmatter to check status
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		// Parse frontmatter to get status
		status := parseJobStatus(string(content))
		if status != "running" {
			continue
		}

		// Check for lock file
		lockFile := filePath + ".lock"
		pidContent, err := os.ReadFile(lockFile)
		if err != nil {
			// No lock file, but status is running - mark as interrupted
			status = "interrupted"
		} else {
			// Check if PID is alive
			var pid int
			if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err == nil {
				if !process.IsProcessAlive(pid) {
					status = "interrupted"
				}
			} else {
				status = "interrupted"
			}
		}

		// Parse job metadata
		jobID, jobTitle, startedAt := parseJobMetadata(string(content))
		if jobID == "" {
			jobID = strings.TrimSuffix(filename, ".md")
		}

		// Create session object for this job
		session := &models.Session{
			ID:               jobID,
			Type:             "oneshot_job",
			Status:           status,
			Repo:             repo,
			Branch:           branch,
			WorkingDirectory: planDir,
			User:             os.Getenv("USER"),
			StartedAt:        startedAt,
			LastActivity:     startedAt,
			PlanName:         planName,
			JobTitle:         jobTitle,
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// parseJobStatus extracts the status from job frontmatter
func parseJobStatus(content string) string {
	// Look for status: line in frontmatter
	lines := strings.Split(content, "\n")
	inFrontmatter := false

	for _, line := range lines {
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
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}

	return "pending"
}

// parseJobMetadata extracts ID, title, and started_at from frontmatter
func parseJobMetadata(content string) (id, title string, startedAt time.Time) {
	lines := strings.Split(content, "\n")
	inFrontmatter := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			} else {
				break
			}
		}

		if !inFrontmatter {
			continue
		}

		if strings.HasPrefix(trimmed, "id:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				id = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(trimmed, "title:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				title = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(trimmed, "updated_at:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				timeStr := strings.Trim(strings.TrimSpace(parts[1]), `"`)
				if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
					startedAt = t
				}
			}
		}
	}

	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	return
}
