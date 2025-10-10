package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/process"
)

// Cache for flow jobs discovery to avoid expensive flow plan list calls
var (
	flowJobsCacheTTL  = 1 * time.Minute // Cache for 1 minute
	flowJobsCachePath = expandPath("~/.grove/hooks/flow_jobs_cache.json")
	// Background refresh disabled by default (CLI commands exit too quickly).
	// Can be enabled for long-running commands like `browse` TUI.
	flowJobsBackgroundRefresh = false
	flowJobsRefreshStarted    bool
)

// EnableBackgroundRefresh enables periodic cache updates in the background
func EnableBackgroundRefresh() {
	flowJobsBackgroundRefresh = true
}

// GetCachedFlowJobs returns cached flow jobs without triggering a refresh.
// Returns empty slice if cache doesn't exist or is expired.
func GetCachedFlowJobs() ([]*models.Session, error) {
	if cacheData, err := os.ReadFile(flowJobsCachePath); err == nil {
		var cached flowJobsCacheData
		if err := json.Unmarshal(cacheData, &cached); err == nil {
			// Return cached data even if slightly expired - better to show stale data
			// than wait 4 seconds. Background refresh will update it.
			return cached.Sessions, nil
		}
	}
	return []*models.Session{}, nil
}

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

// flowJobsCacheData wraps the sessions with a timestamp for file-based caching
type flowJobsCacheData struct {
	Timestamp time.Time          `json:"timestamp"`
	Sessions  []*models.Session `json:"sessions"`
}

// startBackgroundRefresh starts a goroutine that periodically refreshes the flow jobs cache
func startBackgroundRefresh() {
	if flowJobsRefreshStarted || !flowJobsBackgroundRefresh {
		return
	}
	flowJobsRefreshStarted = true

	go func() {
		// Initial delay to not block startup
		time.Sleep(100 * time.Millisecond)

		// Refresh immediately on startup
		refreshFlowJobsCache()

		// Then refresh every 30 seconds
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			refreshFlowJobsCache()
		}
	}()
}

// refreshFlowJobsCache fetches fresh data and updates the cache file
func refreshFlowJobsCache() {
	cmd := exec.Command("flow", "plan", "list", "--json", "--all-workspaces", "--include-finished", "--verbose")
	output, err := cmd.Output()
	if err != nil {
		return // Silently fail for background refresh
	}

	type Job struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Status    string    `json:"status"`
		Type      string    `json:"type"`
		Worktree  string    `json:"worktree,omitempty"`
		Filename  string    `json:"filename,omitempty"`
		FilePath  string    `json:"file_path,omitempty"`
		StartTime time.Time `json:"start_time,omitempty"`
		UpdatedAt time.Time `json:"updated_at,omitempty"`
	}
	type PlanSummary struct {
		Title         string `json:"title"`
		Path          string `json:"path"`
		Jobs          []*Job `json:"jobs,omitempty"`
		WorkspaceName string `json:"workspace_name,omitempty"`
	}

	var planSummaries []PlanSummary
	if err := json.Unmarshal(output, &planSummaries); err != nil {
		return
	}

	var sessions []*models.Session
	seenJobs := make(map[string]bool)

	for _, plan := range planSummaries {
		for _, job := range plan.Jobs {
			if job.Status != "running" && job.Status != "interrupted" && job.Status != "pending_user" {
				continue
			}

			if job.FilePath != "" {
				if seenJobs[job.FilePath] {
					continue
				}
				seenJobs[job.FilePath] = true
			}

			displayStatus := job.Status
			if displayStatus == "pending_user" {
				displayStatus = "running"
			}

			startTime := job.StartTime
			if startTime.IsZero() {
				startTime = job.UpdatedAt
			}

			session := &models.Session{
				ID:               job.ID,
				Type:             "job",
				Status:           displayStatus,
				Repo:             plan.WorkspaceName,
				Branch:           job.Worktree,
				WorkingDirectory: plan.Path,
				User:             os.Getenv("USER"),
				StartedAt:        startTime,
				LastActivity:     startTime,
				PlanName:         plan.Title,
				JobTitle:         job.Title,
				JobFilePath:      job.FilePath,
			}
			sessions = append(sessions, session)
		}
	}

	// Update cache file
	cacheData := flowJobsCacheData{
		Timestamp: time.Now(),
		Sessions:  sessions,
	}
	if jsonData, err := json.Marshal(cacheData); err == nil {
		os.MkdirAll(filepath.Dir(flowJobsCachePath), 0755)
		os.WriteFile(flowJobsCachePath, jsonData, 0644)
	}
}

// DiscoverLiveFlowJobs calls `flow plan list` to get an accurate list of all jobs and their statuses.
func DiscoverLiveFlowJobs() ([]*models.Session, error) {
	// Start background refresh if enabled (only starts once)
	startBackgroundRefresh()

	// Try to load from file cache
	if cacheData, err := os.ReadFile(flowJobsCachePath); err == nil {
		var cached flowJobsCacheData
		if err := json.Unmarshal(cacheData, &cached); err == nil {
			if time.Since(cached.Timestamp) < flowJobsCacheTTL {
				return cached.Sessions, nil
			}
		}
	}

	// Use the `flow` command as the source of truth.
	// Note: --verbose is required to include job details in the JSON output
	cmd := exec.Command("flow", "plan", "list", "--json", "--all-workspaces", "--include-finished", "--verbose")
	output, err := cmd.Output()
	if err != nil {
		// If `flow` command fails, we can't discover jobs. Return an empty list.
		// This is not a fatal error for grove-hooks.
		if os.Getenv("GROVE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "Warning: could not execute 'flow plan list': %v\n", err)
		}
		return []*models.Session{}, nil
	}

	// Define structs to parse the JSON output from `flow plan list`.
	type Job struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Status    string    `json:"status"`
		Type      string    `json:"type"`
		Worktree  string    `json:"worktree,omitempty"`
		Filename  string    `json:"filename,omitempty"`
		FilePath  string    `json:"file_path,omitempty"`
		StartTime time.Time `json:"start_time,omitempty"`
		UpdatedAt time.Time `json:"updated_at,omitempty"`
	}
	type PlanSummary struct {
		Title         string `json:"title"`
		Path          string `json:"path"`
		Jobs          []*Job `json:"jobs,omitempty"`
		WorkspaceName string `json:"workspace_name,omitempty"`
	}

	var planSummaries []PlanSummary
	if err := json.Unmarshal(output, &planSummaries); err != nil {
		if os.Getenv("GROVE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse JSON from 'flow plan list': %v\n", err)
		}
		return []*models.Session{}, nil
	}

	var sessions []*models.Session
	// Track seen job file paths to avoid duplicates from multiple workspace discoveries
	seenJobs := make(map[string]bool)

	for _, plan := range planSummaries {
		for _, job := range plan.Jobs {
			// Skip jobs that aren't in a "live" state for the session list.
			if job.Status != "running" && job.Status != "interrupted" && job.Status != "pending_user" {
				continue
			}

			// Deduplicate jobs by file path (same job may be discovered from main workspace and worktree)
			if job.FilePath != "" {
				if seenJobs[job.FilePath] {
					continue
				}
				seenJobs[job.FilePath] = true
			}

			// For display purposes, group 'pending_user' into 'running'.
			displayStatus := job.Status
			if displayStatus == "pending_user" {
				displayStatus = "running"
			}

			// Use UpdatedAt if StartTime is zero
			startTime := job.StartTime
			if startTime.IsZero() {
				startTime = job.UpdatedAt
			}

			session := &models.Session{
				ID:               job.ID,
				Type:             "job", // Use "job" for flow jobs
				Status:           displayStatus,
				Repo:             plan.WorkspaceName,
				Branch:           job.Worktree,
				WorkingDirectory: plan.Path,
				User:             os.Getenv("USER"),
				StartedAt:        startTime,
				LastActivity:     startTime, // Use StartTime as a proxy for LastActivity.
				PlanName:         plan.Title,
				JobTitle:         job.Title,
				JobFilePath:      job.FilePath,
			}
			sessions = append(sessions, session)
		}
	}

	// Update file cache
	cacheData := flowJobsCacheData{
		Timestamp: time.Now(),
		Sessions:  sessions,
	}
	if jsonData, err := json.Marshal(cacheData); err == nil {
		// Ensure cache directory exists
		os.MkdirAll(filepath.Dir(flowJobsCachePath), 0755)
		// Write cache file (ignore errors - cache is best-effort)
		os.WriteFile(flowJobsCachePath, jsonData, 0644)
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

		// Read the frontmatter to check status and type
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		jobInfo := parseJobFrontmatter(string(content))

		// Skip jobs that are not in a potentially "live" state.
		// `pending_user` is a live state for chat jobs.
		if jobInfo.Status != "running" && jobInfo.Status != "pending_user" {
			continue
		}

		finalStatus := jobInfo.Status

		// Liveness check now depends on the job type.
		if jobInfo.Type == "chat" {
			// For chat jobs, 'running' or 'pending_user' means it's active.
			// No lock file is created for them, so we consider it live.
			// For display purposes in the list, we'll call it 'running'.
			finalStatus = "running"
		} else {
			// For all other job types (e.g., 'oneshot'), we enforce the lock file check.
			lockFile := filePath + ".lock"
			pidContent, err := os.ReadFile(lockFile)
			if err != nil {
				// No lock file for a non-chat job means it's interrupted.
				finalStatus = "interrupted"
			} else {
				// Lock file exists, now check if the PID is alive.
				var pid int
				if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err == nil {
					if !process.IsProcessAlive(pid) {
						finalStatus = "interrupted" // PID is dead.
					} else {
						finalStatus = "running" // Lock file and live PID, it's running.
					}
				} else {
					finalStatus = "interrupted" // Invalid PID in lock file.
				}
			}
		}

		// Create session object for this job
		session := &models.Session{
			ID:               jobInfo.ID,
			Type:             "oneshot_job", // The session model uses this generic type for all jobs.
			Status:           finalStatus,   // This now holds the correctly determined status.
			Repo:             repo,
			Branch:           branch,
			WorkingDirectory: planDir,
			User:             os.Getenv("USER"),
			StartedAt:        jobInfo.StartedAt,
			LastActivity:     jobInfo.StartedAt,
			PlanName:         planName,
			JobTitle:         jobInfo.Title,
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// jobInfo holds metadata parsed from a job's frontmatter.
type jobInfo struct {
	ID        string
	Title     string
	Status    string
	Type      string
	StartedAt time.Time
}

// parseJobFrontmatter extracts ID, title, status, type, and start time from frontmatter.
func parseJobFrontmatter(content string) jobInfo {
	info := jobInfo{
		Status:    "pending",
		Type:      "oneshot", // Default to oneshot if not specified
		StartedAt: time.Now(),
	}
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

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "id":
			info.ID = value
		case "title":
			info.Title = value
		case "status":
			info.Status = value
		case "type":
			info.Type = value
		case "start_time": // Grove-flow uses this field
			if t, err := time.Parse(time.RFC3339, strings.Trim(value, `"`)); err == nil {
				info.StartedAt = t
			}
		case "updated_at": // Fallback for older jobs
			if info.StartedAt.IsZero() {
				if t, err := time.Parse(time.RFC3339, strings.Trim(value, `"`)); err == nil {
					info.StartedAt = t
				}
			}
		}
	}
	if info.ID == "" {
		// Fallback if no ID is present.
		info.ID = info.Title
	}
	return info
}

// markInterruptedJobsInPlan scans a single plan directory and marks jobs with status: running as interrupted
// if their lock file is missing or PID is dead. Returns the number of jobs updated.
func markInterruptedJobsInPlan(planDir string, dryRun bool) (int, error) {
	updated := 0

	entries, err := os.ReadDir(planDir)
	if err != nil {
		return 0, err
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

		// Read the file
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		contentStr := string(content)

		// Parse frontmatter to check status and type
		jobInfo := parseJobFrontmatter(contentStr)
		if jobInfo.Status != "running" {
			continue
		}

		// Skip chat jobs - they don't use lock files, so running status is valid
		if jobInfo.Type == "chat" {
			continue
		}

		// For non-chat jobs, check for lock file
		lockFile := filePath + ".lock"
		shouldMark := false

		if _, err := os.Stat(lockFile); os.IsNotExist(err) {
			// No lock file - mark as interrupted
			shouldMark = true
		} else {
			// Lock file exists - check if PID is alive
			pidContent, err := os.ReadFile(lockFile)
			if err != nil {
				shouldMark = true
			} else {
				var pid int
				if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err == nil {
					if !process.IsProcessAlive(pid) {
						shouldMark = true
					}
				} else {
					shouldMark = true
				}
			}
		}

		if shouldMark {
			if dryRun {
				fmt.Printf("Would update: %s\n", filePath)
			} else {
				// Update the frontmatter
				newContent := strings.Replace(contentStr, "status: running", "status: interrupted", 1)
				if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
					return updated, fmt.Errorf("failed to update %s: %w", filePath, err)
				}
				fmt.Printf("Updated: %s\n", filePath)
			}
			updated++
		}
	}

	return updated, nil
}
