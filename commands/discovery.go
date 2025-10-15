package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coreconfig "github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/process"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-flow/pkg/orchestration"
	"github.com/mattsolo1/grove-hooks/internal/config"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/mattsolo1/grove-hooks/internal/utils"
	"github.com/mattsolo1/grove-notifications"
	"github.com/sirupsen/logrus"
)

// Cache for flow jobs discovery to avoid expensive flow plan list calls
var (
	flowJobsCacheTTL  = 1 * time.Minute // Cache for 1 minute
	flowJobsCachePath = utils.ExpandPath("~/.grove/hooks/flow_jobs_cache.json")
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
	WorktreeRootPath    string    `json:"worktree_root_path,omitempty"`
	User                string    `json:"user"`
	StartedAt           time.Time `json:"started_at"`
	TranscriptPath      string    `json:"transcript_path,omitempty"`
	ProjectName         string    `json:"project_name,omitempty"`
	IsWorktree          bool      `json:"is_worktree,omitempty"`
	ParentEcosystemPath string    `json:"parent_ecosystem_path,omitempty"`
	Type                string    `json:"type,omitempty"`
	JobTitle            string    `json:"job_title,omitempty"`
	PlanName            string    `json:"plan_name,omitempty"`
	JobFilePath         string    `json:"job_file_path,omitempty"`
}

// DiscoverLiveClaudeSessions scans ~/.grove/hooks/sessions/ directory and returns live sessions
// A session is considered live if its PID is still alive
func DiscoverLiveClaudeSessions(storage interfaces.SessionStorer) ([]*models.Session, error) {
	groveSessionsDir := utils.ExpandPath("~/.grove/hooks/sessions")

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

		// Use directory name to locate the session files
		dirName := entry.Name()
		sessionDir := filepath.Join(groveSessionsDir, dirName)
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

		// Use the session ID from metadata, not the directory name
		// This ensures consistency with flow jobs and database entries
		sessionID := metadata.SessionID

		// Determine status and last activity based on liveness
		status := "running"
		var endedAt *time.Time
		lastActivity := metadata.StartedAt // Default to StartedAt

		if !isAlive {
			// Process is dead. Check if this is a flow job that should be auto-completed.
			if (metadata.Type == "interactive_agent" || metadata.Type == "agent") && metadata.JobFilePath != "" {
				// This is a dead flow job. Delegate to 'flow plan complete' in the background
				// to avoid blocking the session list command.
				// Note: We keep the session directory so that flow plan complete can read the metadata
				// to close the tmux window. The directory will be cleaned up after completion.
				go func(jobPath, sessDir string) {
					cmd := exec.Command("flow", "plan", "complete", jobPath)
					// We can log errors for debugging but don't need to block on them.
					// This is a best-effort, self-healing mechanism.
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
					// After completion, wait a bit then clean up the session directory
					// This gives time for any retries or manual completions to read the metadata
					time.Sleep(10 * time.Second)
					os.RemoveAll(sessDir)
				}(metadata.JobFilePath, sessionDir)
			} else {
				// This is a standard Claude session that died unexpectedly. Clean up its directory.
				go os.RemoveAll(sessionDir)
			}

			// For the current view, show the session as interrupted. It will be updated on the next refresh.
			status = "interrupted"
			now := time.Now()
			endedAt = &now
			lastActivity = now
		} else {
			// Process is alive, enrich with DB data if available
			dbSessionData, err := storage.GetSession(sessionID)
			if err == nil {
				var dbStatus string
				var dbLastActivity time.Time

				// Extract status and last activity from either ExtendedSession or Session
				if extSession, ok := dbSessionData.(*disk.ExtendedSession); ok {
					dbStatus = extSession.Status
					dbLastActivity = extSession.LastActivity
				} else if session, ok := dbSessionData.(*models.Session); ok {
					dbStatus = session.Status
					dbLastActivity = session.LastActivity
				}

				if dbStatus == "idle" {
					status = "idle" // Override default "running" status
				}

				if !dbLastActivity.IsZero() {
					lastActivity = dbLastActivity // Use DB value
				}
			}
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
			LastActivity:     lastActivity, // Use the determined timestamp
			EndedAt:          endedAt,
			IsTest:           false,
			Type:             metadata.Type,
			JobTitle:         metadata.JobTitle,
			PlanName:         metadata.PlanName,
			JobFilePath:      metadata.JobFilePath,
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

// refreshFlowJobsCache fetches fresh data and updates the cache file using NotebookLocator
func refreshFlowJobsCache() {
	// Initialize workspace provider and NotebookLocator
	logger := logrus.New()
	logger.SetOutput(io.Discard) // Suppress discoverer's debug output
	discoveryService := workspace.NewDiscoveryService(logger)
	discoveryResult, err := discoveryService.DiscoverAll()
	if err != nil {
		return // Silently fail for background refresh
	}
	provider := workspace.NewProvider(discoveryResult)

	coreCfg, err := coreconfig.LoadDefault()
	if err != nil {
		coreCfg = &coreconfig.Config{}
	}
	locator := workspace.NewNotebookLocator(coreCfg)

	// Scan for all plan and chat directories
	planDirs, _ := locator.ScanForAllPlans(provider)
	chatDirs, _ := locator.ScanForAllChats(provider)

	allScanDirs := append(planDirs, chatDirs...)

	var sessions []*models.Session
	seenJobs := make(map[string]bool)

	// Walk each directory and load jobs
	for _, scannedDir := range allScanDirs {
		ownerNode := scannedDir.Owner
		filepath.Walk(scannedDir.Path, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}

			// Skip spec.md and README.md
			if info.Name() == "spec.md" || info.Name() == "README.md" {
				return nil
			}

			job, loadErr := orchestration.LoadJob(path)
			if loadErr != nil {
				// This is expected for non-job markdown files, so we skip them silently.
				return nil
			}

			// Only process valid jobs (plan or chat)
			if job.Type != "chat" && job.Type != "oneshot" && job.Type != "agent" && job.Type != "interactive_agent" && job.Type != "headless_agent" && job.Type != "shell" {
				return nil
			}

			// Deduplicate by job file path (more reliable than ID which can clash across plans)
			if seenJobs[path] {
				return nil
			}
			seenJobs[path] = true

			// Start with the owner of the plan directory as the default context.
			effectiveOwnerNode := ownerNode

			// If the job's frontmatter specifies a worktree, resolve it to a more specific node.
			if job.Worktree != "" {
				// The ownerNode is the "base project" (e.g., main grove-core).
				// The job.Worktree is the name of the ecosystem worktree (e.g., "test444").
				resolvedNode := provider.FindByWorktree(ownerNode, job.Worktree)
				if resolvedNode != nil {
					// We found the correct workspace node! Use this as the effective owner.
					effectiveOwnerNode = resolvedNode
				}
				// If resolvedNode is nil, we gracefully fall back to the base project node.
			}

			// Now, determine repo name and worktree from the correctly resolved effectiveOwnerNode.
			repoName := effectiveOwnerNode.Name
			worktreeName := ""
			if effectiveOwnerNode.IsWorktree() {
				if effectiveOwnerNode.ParentProjectPath != "" {
					repoName = filepath.Base(effectiveOwnerNode.ParentProjectPath)
				}
				worktreeName = effectiveOwnerNode.Name
			}

			session := &models.Session{
				ID:               job.ID,
				Type:             string(job.Type),
				Status:           string(job.Status),
				Repo:             repoName,
				Branch:           worktreeName, // Branch and Worktree are synonymous here
				WorkingDirectory: effectiveOwnerNode.Path, // CRITICAL: This path is used for TUI grouping.
				StartedAt:        job.StartTime,
				LastActivity:     job.UpdatedAt,
				PlanName:         filepath.Base(filepath.Dir(path)),
				JobTitle:         job.Title,
				JobFilePath:      path,
			}
			sessions = append(sessions, session)
			return nil
		})
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

// DiscoverFlowJobs discovers all flow jobs by directly scanning plan directories using NotebookLocator.
// This eliminates the subprocess call to `flow plan list` for better performance and reliability.
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

	// Initialize workspace provider and NotebookLocator
	logger := logrus.New()
	logger.SetOutput(io.Discard) // Suppress discoverer's debug output
	discoveryService := workspace.NewDiscoveryService(logger)
	discoveryResult, err := discoveryService.DiscoverAll()
	if err != nil {
		// Can't discover workspaces, so can't find jobs.
		return []*models.Session{}, nil
	}
	provider := workspace.NewProvider(discoveryResult)

	coreCfg, err := coreconfig.LoadDefault()
	if err != nil {
		coreCfg = &coreconfig.Config{} // Proceed with defaults
	}
	locator := workspace.NewNotebookLocator(coreCfg)

	// Scan for all plan and chat directories
	planDirs, _ := locator.ScanForAllPlans(provider)
	chatDirs, _ := locator.ScanForAllChats(provider)

	allScanDirs := append(planDirs, chatDirs...)

	var sessions []*models.Session
	seenJobs := make(map[string]bool)

	// Walk each directory and load jobs
	for _, scannedDir := range allScanDirs {
		ownerNode := scannedDir.Owner
		filepath.Walk(scannedDir.Path, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}

			// Skip spec.md and README.md
			if info.Name() == "spec.md" || info.Name() == "README.md" {
				return nil
			}

			job, loadErr := orchestration.LoadJob(path)
			if loadErr != nil {
				// This is expected for non-job markdown files, so we skip them silently.
				return nil
			}

			// Only process valid jobs (plan or chat)
			if job.Type != "chat" && job.Type != "oneshot" && job.Type != "agent" && job.Type != "interactive_agent" && job.Type != "headless_agent" && job.Type != "shell" {
				return nil
			}

			// Deduplicate by job file path (more reliable than ID which can clash across plans)
			if seenJobs[path] {
				return nil
			}
			seenJobs[path] = true

			// Start with the owner of the plan directory as the default context.
			effectiveOwnerNode := ownerNode

			// If the job's frontmatter specifies a worktree, resolve it to a more specific node.
			if job.Worktree != "" {
				// The ownerNode is the "base project" (e.g., main grove-core).
				// The job.Worktree is the name of the ecosystem worktree (e.g., "test444").
				resolvedNode := provider.FindByWorktree(ownerNode, job.Worktree)
				if resolvedNode != nil {
					// We found the correct workspace node! Use this as the effective owner.
					effectiveOwnerNode = resolvedNode
				}
				// If resolvedNode is nil, we gracefully fall back to the base project node.
			}

			// Now, determine repo name and worktree from the correctly resolved effectiveOwnerNode.
			repoName := effectiveOwnerNode.Name
			worktreeName := ""
			if effectiveOwnerNode.IsWorktree() {
				if effectiveOwnerNode.ParentProjectPath != "" {
					repoName = filepath.Base(effectiveOwnerNode.ParentProjectPath)
				}
				worktreeName = effectiveOwnerNode.Name
			}

			session := &models.Session{
				ID:               job.ID,
				Type:             string(job.Type),
				Status:           string(job.Status),
				Repo:             repoName,
				Branch:           worktreeName, // Branch and Worktree are synonymous here
				WorkingDirectory: effectiveOwnerNode.Path, // CRITICAL: This path is used for TUI grouping.
				StartedAt:        job.StartTime,
				LastActivity:     job.UpdatedAt,
				PlanName:         filepath.Base(filepath.Dir(path)),
				JobTitle:         job.Title,
				JobFilePath:      path,
			}
			sessions = append(sessions, session)
			return nil
		})
	}

	// Update file cache
	cacheData := flowJobsCacheData{
		Timestamp: time.Now(),
		Sessions:  sessions,
	}
	if jsonData, err := json.Marshal(cacheData); err == nil {
		os.MkdirAll(filepath.Dir(flowJobsCachePath), 0755)
		os.WriteFile(flowJobsCachePath, jsonData, 0644)
	}

	for _, session := range sessions {
		if session.Type != "" && session.Type != "claude_session" {
			updateSessionStatusFromFilesystem(session)
		}
	}

	return sessions, nil
}

// GetAllSessions fetches sessions from all sources, merges them, and sorts them.
func GetAllSessions(storage interfaces.SessionStorer, hideCompleted bool) ([]*models.Session, error) {
	// Discover live Claude sessions from filesystem (fast - just reads local files)
	liveClaudeSessions, err := DiscoverLiveClaudeSessions(storage)
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
	sessionsMap := make(map[string]*models.Session)

	// Add DB sessions first as a baseline
	for _, session := range dbSessions {
		sessionsMap[session.ID] = session
	}

	// Add/update with flow jobs, which are more authoritative for job-related metadata
	for _, session := range flowJobs {
		sessionsMap[session.ID] = session
	}

	// Add/update with live Claude sessions, which provide the most current "live" status
	for _, session := range liveClaudeSessions {
		// If a session with this ID already exists (from flow jobs),
		// update its status to reflect the live process.
		if existing, ok := sessionsMap[session.ID]; ok {
			// Don't override terminal states from flow jobs
			// Flow jobs are authoritative for completed/failed/interrupted states
			if existing.Status != "completed" && existing.Status != "failed" && existing.Status != "interrupted" {
				existing.Status = session.Status
			}
			existing.PID = session.PID
			existing.LastActivity = session.LastActivity
		} else {
			// This is a standalone claude session, not from a flow job
			sessionsMap[session.ID] = session
		}
	}

	// Convert map back to slice
	sessions := make([]*models.Session, 0, len(sessionsMap))
	for _, session := range sessionsMap {
		sessions = append(sessions, session)
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

	// Sort sessions: running first, then idle/pending_user, then others by last_activity desc
	sort.Slice(sessions, func(i, j int) bool {
		iPriority := 3
		if sessions[i].Status == "running" {
			iPriority = 1
		} else if sessions[i].Status == "idle" || sessions[i].Status == "pending_user" {
			iPriority = 2
		}

		jPriority := 3
		if sessions[j].Status == "running" {
			jPriority = 1
		} else if sessions[j].Status == "idle" || sessions[j].Status == "pending_user" {
			jPriority = 2
		}

		if iPriority != jPriority {
			return iPriority < jPriority
		}

		// Sort by LastActivity (most recent first), fall back to StartedAt if LastActivity is not set
		iTime := sessions[i].LastActivity
		if iTime.IsZero() {
			iTime = sessions[i].StartedAt
		}
		jTime := sessions[j].LastActivity
		if jTime.IsZero() {
			jTime = sessions[j].StartedAt
		}
		return iTime.After(jTime)
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
		Status: "pending",
		Type:   "oneshot", // Default to oneshot if not specified
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
	nonLiveStates := map[string]bool{
		"completed":   true,
		"failed":      true,
		"interrupted": true,
		"error":       true,
		"abandoned":   true,
		"hold":        true,
		"todo":        true,
		"pending":     true,
	}
	if nonLiveStates[jobInfo.Status] {
		return jobInfo.Status, nil
	}

	// For non-terminal states, we must verify liveness
	if jobInfo.Status == "running" || jobInfo.Status == "pending_user" {
		// Chat and interactive_agent jobs don't use lock files; status in frontmatter is sufficient.
		if jobInfo.Type == "chat" || jobInfo.Type == "interactive_agent" {
			return jobInfo.Status, nil
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

// DispatchStateChangeNotifications compares old and new sessions and sends notifications for relevant state changes
func DispatchStateChangeNotifications(oldSessions, newSessions []*models.Session) {
	// Build a map of old session states for quick lookup
	oldStatusMap := make(map[string]string)
	for _, s := range oldSessions {
		oldStatusMap[s.ID] = s.Status
	}

	// Check each new session for relevant state changes
	for _, newSession := range newSessions {
		oldStatus, wasTracked := oldStatusMap[newSession.ID]
		if !wasTracked {
			// This is a new session, not a state change
			continue
		}

		// Rule: Notify when a chat job transitions from running to pending_user
		if newSession.Type == "chat" && newSession.Status == "pending_user" && oldStatus == "running" {
			sendJobReadyNotification(newSession)
		}

		// Rule: Notify when an interactive_agent job transitions from running to idle
		if newSession.Type == "interactive_agent" && newSession.Status == "idle" && oldStatus == "running" {
			sendJobReadyNotification(newSession)
		}

		// Rule: Notify when a oneshot job completes
		if newSession.Type == "oneshot" && newSession.Status == "completed" && oldStatus == "running" {
			sendJobReadyNotification(newSession)
		}

		// Future notification rules can be added here
		// For example:
		// - Job failures: oldStatus == "running" && newSession.Status == "failed"
	}
}

// sendJobReadyNotification sends a notification that a job is ready for user input
func sendJobReadyNotification(session *models.Session) {
	// Load config to check notification settings
	cfg := config.Load()

	// Build title from session info based on job type
	var title string
	if session.Type == "chat" {
		title = fmt.Sprintf("💬 Chat Ready: %s", session.JobTitle)
		if session.JobTitle == "" && session.PlanName != "" {
			title = fmt.Sprintf("💬 Chat Ready: %s", session.PlanName)
		} else if session.JobTitle == "" {
			title = "💬 Chat Ready"
		}
	} else if session.Type == "interactive_agent" {
		title = fmt.Sprintf("🤖 Agent Idle: %s", session.JobTitle)
		if session.JobTitle == "" && session.PlanName != "" {
			title = fmt.Sprintf("🤖 Agent Idle: %s", session.PlanName)
		} else if session.JobTitle == "" {
			title = "🤖 Agent Idle"
		}
	} else if session.Type == "oneshot" {
		title = fmt.Sprintf("✅ Oneshot Complete: %s", session.JobTitle)
		if session.JobTitle == "" && session.PlanName != "" {
			title = fmt.Sprintf("✅ Oneshot Complete: %s", session.PlanName)
		} else if session.JobTitle == "" {
			title = "✅ Oneshot Complete"
		}
	} else {
		title = fmt.Sprintf("Job Ready: %s", session.JobTitle)
		if session.JobTitle == "" {
			title = "Job Ready"
		}
	}

	// Build detailed message with session context
	var messageParts []string

	// Add session ID
	if session.ID != "" {
		messageParts = append(messageParts, fmt.Sprintf("ID: %s", session.ID))
	}

	// Add job type
	if session.Type != "" {
		messageParts = append(messageParts, fmt.Sprintf("Type: %s", session.Type))
	}

	// Add repository and worktree/branch
	if session.Repo != "" {
		if session.Branch != "" {
			messageParts = append(messageParts, fmt.Sprintf("Worktree: %s/%s", session.Repo, session.Branch))
		} else {
			messageParts = append(messageParts, fmt.Sprintf("Repo: %s", session.Repo))
		}
	}

	// Add plan name if different from job title
	if session.PlanName != "" && session.PlanName != session.JobTitle {
		messageParts = append(messageParts, fmt.Sprintf("Plan: %s", session.PlanName))
	}

	message := strings.Join(messageParts, "\n")

	// Send ntfy notification if configured
	if cfg.Ntfy.Enabled && cfg.Ntfy.Topic != "" {
		_ = notifications.SendNtfy(
			cfg.Ntfy.URL,
			cfg.Ntfy.Topic,
			title,
			message,
			"default",
			nil,
		)
	}

	// Also send system notification if configured
	if len(cfg.System.Levels) > 0 {
		_ = notifications.SendSystem(title, message, "info")
	}
}
