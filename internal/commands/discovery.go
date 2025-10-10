package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/process"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
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
		// Only refresh periodically; initial data is loaded by the main thread
		// This avoids race condition where both initial load and background refresh
		// try to execute 'flow plan list' simultaneously, causing cache corruption
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			refreshFlowJobsCache()
		}
	}()
}

// refreshFlowJobsCache fetches fresh data and updates the cache file
func refreshFlowJobsCache() {
	cmdArgs := []string{"plan", "list", "--json", "--include-finished", "--verbose"}

	// Conditionally add --all-workspaces unless in local discovery mode for testing
	if os.Getenv("GROVE_HOOKS_DISCOVERY_MODE") != "local" {
		cmdArgs = append(cmdArgs, "--all-workspaces")
	}

	cmd := exec.Command("flow", cmdArgs...)
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

			// Populate EndedAt for terminal states
			var endedAt *time.Time
			if job.Status == "completed" || job.Status == "failed" || job.Status == "interrupted" {
				endTime := job.UpdatedAt
				endedAt = &endTime
			}

			session := &models.Session{
				ID:               job.ID,
				Type:             job.Type, // Use specific job type (e.g. chat, interactive_agent, oneshot)
				Status:           displayStatus,
				Repo:             plan.WorkspaceName,
				Branch:           job.Worktree,
				WorkingDirectory: plan.Path,
				User:             os.Getenv("USER"),
				StartedAt:        startTime,
				LastActivity:     job.UpdatedAt,
				EndedAt:          endedAt,
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

// DiscoverFlowJobs calls `flow plan list` to get an accurate list of all jobs and their statuses.
func DiscoverFlowJobs() ([]*models.Session, error) {
	// Start background refresh if enabled (only starts once)
	startBackgroundRefresh()

	// Try to load from file cache
	if cacheData, err := os.ReadFile(flowJobsCachePath); err == nil {
		var cached flowJobsCacheData
		if err := json.Unmarshal(cacheData, &cached); err == nil {
			if time.Since(cached.Timestamp) < flowJobsCacheTTL {
				// Update real-time status for cached sessions before returning
				for _, session := range cached.Sessions {
					// Check if it's a flow job (not a claude_code session)
					if session.Type != "" && session.Type != "claude_session" {
						updateSessionStatusFromFilesystem(session)
					}
				}
				return cached.Sessions, nil
			}
		}
	}

	// Use the `flow` command as the source of truth.
	// Note: --verbose is required to include job details in the JSON output
	cmdArgs := []string{"plan", "list", "--json", "--include-finished", "--verbose"}

	// Conditionally add --all-workspaces unless in local discovery mode for testing
	if os.Getenv("GROVE_HOOKS_DISCOVERY_MODE") != "local" {
		cmdArgs = append(cmdArgs, "--all-workspaces")
	}

	cmd := exec.Command("flow", cmdArgs...)
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

			// Populate EndedAt for terminal states
			var endedAt *time.Time
			if job.Status == "completed" || job.Status == "failed" || job.Status == "interrupted" {
				endTime := job.UpdatedAt
				endedAt = &endTime
			}

			session := &models.Session{
				ID:               job.ID,
				Type:             job.Type, // Use specific job type (e.g. chat, interactive_agent, oneshot)
				Status:           displayStatus,
				Repo:             plan.WorkspaceName,
				Branch:           job.Worktree,
				WorkingDirectory: plan.Path,
				User:             os.Getenv("USER"),
				StartedAt:        startTime,
				LastActivity:     job.UpdatedAt,
				EndedAt:          endedAt,
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

	// After getting sessions from cache or command, update their status in real-time
	for _, session := range sessions {
		// Check if it's a flow job (not a claude_code session)
		if session.Type != "" && session.Type != "claude_session" {
			updateSessionStatusFromFilesystem(session)
		}
	}

	return sessions, nil
}

// GetAllSessions fetches sessions from all sources, merges them, and sorts them.
func GetAllSessions(storage interfaces.SessionStorer, hideCompleted bool) ([]*models.Session, error) {
	// Discover live Claude sessions from filesystem (fast - just reads local files)
	liveClaudeSessions, err := DiscoverLiveClaudeSessions()
	if err != nil {
		// Log error but continue
		if os.Getenv("GROVE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "Warning: failed to discover live Claude sessions: %v\n", err)
		}
		liveClaudeSessions = []*models.Session{}
	}

	// Discover flow jobs (now fast, as it uses cache + filesystem checks)
	flowJobs, err := DiscoverFlowJobs()
	if err != nil {
		if os.Getenv("GROVE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "Warning: failed to discover flow jobs: %v\n", err)
		}
		flowJobs = []*models.Session{}
	}

	// Get archived sessions from database
	dbSessions, err := storage.GetAllSessions()
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}

	// Merge all sources, prioritizing live sessions
	seenIDs := make(map[string]bool)
	sessions := make([]*models.Session, 0, len(liveClaudeSessions)+len(flowJobs)+len(dbSessions))

	// Add live Claude sessions first
	for _, session := range liveClaudeSessions {
		sessions = append(sessions, session)
		seenIDs[session.ID] = true
	}

	// Add flow jobs (includes both live and completed)
	for _, session := range flowJobs {
		if !seenIDs[session.ID] {
			sessions = append(sessions, session)
			seenIDs[session.ID] = true
		}
	}

	// Add DB sessions that aren't already in live/flow sessions
	for _, session := range dbSessions {
		if !seenIDs[session.ID] {
			sessions = append(sessions, session)
		}
	}

	// Filter by hideCompleted if requested
	if hideCompleted {
		var filtered []*models.Session
		for _, s := range sessions {
			if s.Status != "completed" && s.Status != "failed" && s.Status != "error" {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	// Sort sessions: running first, then idle, then others by started_at desc
	sort.Slice(sessions, func(i, j int) bool {
		iPriority := 3
		if sessions[i].Status == "running" {
			iPriority = 1
		} else if sessions[i].Status == "idle" {
			iPriority = 2
		}

		jPriority := 3
		if sessions[j].Status == "running" {
			jPriority = 1
		} else if sessions[j].Status == "idle" {
			jPriority = 2
		}

		if iPriority != jPriority {
			return iPriority < jPriority
		}

		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

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

		// Check lock file for all job types
		lockFile := filePath + ".lock"
		pidContent, err := os.ReadFile(lockFile)
		if err != nil {
			// For interactive_agent jobs, lock file may not exist immediately after start
			if jobInfo.Type == "interactive_agent" {
				finalStatus = "running"
			} else {
				// No lock file means the job is interrupted
				finalStatus = "interrupted"
			}
		} else {
			// Lock file exists, check if PID is alive
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

		// Skip lines with leading whitespace (nested YAML structures)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Strip quotes if present
		value = strings.Trim(value, `"`)

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
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				info.StartedAt = t
			}
		case "updated_at": // Fallback for older jobs
			if info.StartedAt.IsZero() {
				if t, err := time.Parse(time.RFC3339, value); err == nil {
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

// getRealtimeJobStatus checks the filesystem to determine the true, current status of a job.
func getRealtimeJobStatus(jobFilePath string) (string, error) {
	content, err := os.ReadFile(jobFilePath)
	if err != nil {
		return "unknown", fmt.Errorf("failed to read job file %s: %w", jobFilePath, err)
	}
	jobInfo := parseJobFrontmatter(string(content))

	// Terminal states are the source of truth from frontmatter
	terminalStates := map[string]bool{
		"completed":   true,
		"failed":      true,
		"interrupted": true,
		"error":       true,
	}
	if terminalStates[jobInfo.Status] {
		return jobInfo.Status, nil
	}

	// For non-terminal states, we must verify liveness
	if jobInfo.Status == "running" || jobInfo.Status == "pending_user" {
		// Chat and interactive_agent jobs don't use lock files; 'running' in frontmatter is sufficient.
		if jobInfo.Type == "chat" || jobInfo.Type == "interactive_agent" {
			return "running", nil
		}

		// For other job types (oneshot, headless_agent, etc.), check the lock file and PID
		lockFile := jobFilePath + ".lock"
		pidContent, err := os.ReadFile(lockFile)
		if err != nil {
			// No lock file for a running job means it's interrupted.
			return "interrupted", nil
		}

		var pid int
		if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err != nil {
			// Invalid PID in lock file.
			return "interrupted", nil
		}

		if !process.IsProcessAlive(pid) {
			// PID is dead.
			return "interrupted", nil
		}

		// Lock file exists and PID is alive.
		return "running", nil
	}

	// Fallback to the status in the frontmatter.
	return jobInfo.Status, nil
}

// updateSessionStatusFromFilesystem refreshes a job session's status based on its file path.
func updateSessionStatusFromFilesystem(session *models.Session) {
	if session.JobFilePath == "" {
		return // Not a job session with a file path.
	}
	if _, err := os.Stat(session.JobFilePath); os.IsNotExist(err) {
		// If the file is gone, the job is likely gone too. Mark as interrupted.
		session.Status = "interrupted"
		return
	}

	status, err := getRealtimeJobStatus(session.JobFilePath)
	if err != nil {
		if os.Getenv("GROVE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "Warning: could not get realtime status for %s: %v\n", session.JobFilePath, err)
		}
		// Don't change status if we can't determine it
		return
	}
	session.Status = status
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

		// Skip interactive_agent jobs - they may not have lock files immediately after start
		if jobInfo.Type == "interactive_agent" {
			continue
		}

		// For other jobs, check for lock file
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
