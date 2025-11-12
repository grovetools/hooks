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
	"sync"
	"time"

	coreconfig "github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/process"
	coresessions "github.com/mattsolo1/grove-core/pkg/sessions"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-flow/pkg/orchestration"
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
	progressiveRefreshOnce    sync.Once
	progressiveRefreshMutex   sync.Mutex
	lastActiveRefresh         time.Time
	lastFullRefresh           time.Time
)

const (
	activeSessionsRefreshInterval = 2 * time.Second
	fullRefreshInterval           = 3 * time.Second
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

// tryLoadCacheIgnoreTTL returns cached sessions without checking TTL.
// It returns nil if the cache file doesn't exist or is corrupted.
func tryLoadCacheIgnoreTTL() []*models.Session {
	if cacheData, err := os.ReadFile(flowJobsCachePath); err == nil {
		var cached flowJobsCacheData
		if err := json.Unmarshal(cacheData, &cached); err == nil {
			return cached.Sessions
		}
	}
	return nil
}

// writeCacheFile writes sessions to the cache file atomically.
func writeCacheFile(sessions []*models.Session) error {
	cacheData := flowJobsCacheData{
		Timestamp: time.Now(),
		Sessions:  sessions,
	}
	jsonData, err := json.Marshal(cacheData)
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(flowJobsCachePath), 0755)

	// Atomic write: write to temp file then rename
	tempFile, err := os.CreateTemp(filepath.Dir(flowJobsCachePath), "flow_jobs_cache.json.*")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name()) // Clean up temp file on exit

	if _, err := tempFile.Write(jsonData); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}

	return os.Rename(tempFile.Name(), flowJobsCachePath)
}

// refreshActiveSessions quickly updates only running/pending_user/idle sessions
func refreshActiveSessions(coreCfg *coreconfig.Config) {
	debugTiming := os.Getenv("GROVE_DEBUG_TIMING") != ""
	if debugTiming {
		startTime := time.Now()
		defer func() {
			fmt.Fprintf(os.Stderr, "[TIMING] Active sessions refresh: %v\n", time.Since(startTime))
		}()
	}

	// Load current cache
	cached := tryLoadCacheIgnoreTTL()
	if cached == nil {
		return
	}

	// Filter to active sessions only
	activeStates := map[string]bool{
		"running":      true,
		"pending_user": true,
		"idle":         true,
	}

	// Refresh only active sessions in parallel
	var wg sync.WaitGroup
	for _, session := range cached {
		if activeStates[session.Status] && session.Type != "claude_session" {
			wg.Add(1)
			go func(s *models.Session) {
				defer wg.Done()
				updateSessionStatusFromFilesystem(s)
			}(session)
		}
	}
	wg.Wait()

	// Update cache with refreshed active sessions
	writeCacheFile(cached)
}

// refreshAllSessions performs full directory scan and update
func refreshAllSessions(coreCfg *coreconfig.Config) {
	debugTiming := os.Getenv("GROVE_DEBUG_TIMING") != ""
	if debugTiming {
		startTime := time.Now()
		defer func() {
			fmt.Fprintf(os.Stderr, "[TIMING] Full background refresh: %v\n", time.Since(startTime))
		}()
	}
	// Perform full discovery scan
	sessions, err := doFullDiscoveryScan()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Error] Full background refresh failed: %v\n", err)
		return
	}

	// Update cache with the full fresh list
	writeCacheFile(sessions)
}

// progressiveRefreshLoop performs multi-stage refresh in background
func progressiveRefreshLoop(coreCfg *coreconfig.Config) {
	// Perform an initial active refresh immediately on startup to get quick updates
	refreshActiveSessions(coreCfg)

	// Then start the main refresh cycle
	ticker := time.NewTicker(activeSessionsRefreshInterval)
	defer ticker.Stop()

	for {
		<-ticker.C

		progressiveRefreshMutex.Lock()
		now := time.Now()

		// Stage 2: Full refresh if needed (less frequent)
		if now.Sub(lastFullRefresh) > fullRefreshInterval {
			lastFullRefresh = now
			lastActiveRefresh = now // Reset active refresh timer too
			progressiveRefreshMutex.Unlock()
			go refreshAllSessions(coreCfg)
			continue // Skip active refresh on this tick
		}

		// Stage 1: Quick refresh of active sessions
		lastActiveRefresh = now
		progressiveRefreshMutex.Unlock()
		go refreshActiveSessions(coreCfg)
	}
}

// startProgressiveRefreshLoop starts a goroutine that performs progressive refreshing
func startProgressiveRefreshLoop(coreCfg *coreconfig.Config) {
	if flowJobsRefreshStarted || !flowJobsBackgroundRefresh {
		return
	}
	flowJobsRefreshStarted = true
	go progressiveRefreshLoop(coreCfg)
}

// DiscoverLiveInteractiveSessions scans ~/.grove/hooks/sessions/ directory and returns live sessions
// from all interactive providers (Claude, Codex, etc.)
// A session is considered live if its PID is still alive
func DiscoverLiveInteractiveSessions(storage interfaces.SessionStorer) ([]*models.Session, error) {
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

		var metadata coresessions.SessionMetadata
		if err := json.Unmarshal(metadataContent, &metadata); err != nil{
			// Skip if metadata is invalid
			continue
		}

		// Use the session ID from metadata, not the directory name
		// This ensures consistency with flow jobs and database entries
		sessionID := metadata.SessionID
		claudeSessionID := metadata.ClaudeSessionID
		if claudeSessionID == "" {
			// Backwards compatibility: if the field doesn't exist, use the directory name
			claudeSessionID = dirName
		}

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
					cmd := exec.Command("grove", "flow", "plan", "complete", jobPath)
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
			// For interactive_agent sessions, check both the flow job ID and Claude UUID
			dbSessionData, err := storage.GetSession(sessionID)
			if err != nil && claudeSessionID != "" && claudeSessionID != sessionID {
				// Try the Claude UUID if the flow job ID lookup failed
				dbSessionData, err = storage.GetSession(claudeSessionID)
			}

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
			ClaudeSessionID:  claudeSessionID,
			PID:              pid,
			Repo:             metadata.Repo,
			Branch:           metadata.Branch,
			WorkingDirectory: metadata.WorkingDirectory,
			User:             metadata.User,
			Status:           status,
			StartedAt:        metadata.StartedAt,
			LastActivity:     lastActivity, // Use the determined timestamp
			EndedAt:          endedAt,
			IsTest:           false,
			JobTitle:         metadata.JobTitle,
			PlanName:         metadata.PlanName,
			JobFilePath:      metadata.JobFilePath,
			Provider:         metadata.Provider,
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

// doFullDiscoveryScan performs a complete directory walk and job parsing
func doFullDiscoveryScan() ([]*models.Session, error) {
	// Load configuration
	coreCfg, err := coreconfig.LoadDefault()
	if err != nil {
		coreCfg = &coreconfig.Config{} // Proceed with defaults on error
	}

	// Initialize workspace provider and NotebookLocator
	debugTiming := os.Getenv("GROVE_DEBUG_TIMING") != ""
	startTime := time.Now()

	logger := logrus.New()
	logger.SetOutput(io.Discard) // Suppress discoverer's debug output
	discoveryService := workspace.NewDiscoveryService(logger)
	discoveryResult, err := discoveryService.DiscoverAll()
	if err != nil {
		return nil, fmt.Errorf("workspace discovery failed for full scan: %w", err)
	}
	if debugTiming {
		fmt.Fprintf(os.Stderr, "[TIMING] Workspace discovery: %v\n", time.Since(startTime))
	}
	provider := workspace.NewProvider(discoveryResult)

	locator := workspace.NewNotebookLocator(coreCfg)

	// Scan for all plan and chat directories
	scanStart := time.Now()
	planDirs, _ := locator.ScanForAllPlans(provider)
	chatDirs, _ := locator.ScanForAllChats(provider)
	if debugTiming {
		fmt.Fprintf(os.Stderr, "[TIMING] Directory scanning: %v\n", time.Since(scanStart))
	}

	allScanDirs := append(planDirs, chatDirs...)

	// Walk each directory and load jobs in parallel
	walkStart := time.Now()

	// Build generic note groups map once and reuse it
	genericNoteGroups := make(map[string]bool)
	for _, noteType := range coreconfig.DefaultNoteTypes {
		genericNoteGroups[noteType] = true
	}

	// Collect all job file paths first
	jobWorkChan := make(chan jobFileWork, 500)
	var collectWg sync.WaitGroup
	collectWg.Add(1)

	go func() {
		defer collectWg.Done()
		for _, scannedDir := range allScanDirs {
			ownerNode := scannedDir.Owner
			filepath.Walk(scannedDir.Path, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}

				// Skip archive directories entirely
				if info.IsDir() {
					name := info.Name()
					if name == "archive" || name == ".archive" || strings.HasPrefix(name, "archive-") || strings.HasPrefix(name, ".archive-") {
						return filepath.SkipDir
					}
					return nil
				}

				if !strings.HasSuffix(info.Name(), ".md") {
					return nil
				}
				// Skip spec.md and README.md
				if info.Name() == "spec.md" || info.Name() == "README.md" {
					return nil
				}
				jobWorkChan <- jobFileWork{path: path, ownerNode: ownerNode}
				return nil
			})
		}
		close(jobWorkChan)
	}()

	// Process jobs in parallel with worker pool and incremental caching
	const numWorkers = 20
	sessionsChan := make(chan *models.Session, 500)
	var processWg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		processWg.Add(1)
		go func() {
			defer processWg.Done()
			for work := range jobWorkChan {
				if session := processJobFile(work, provider, genericNoteGroups); session != nil {
					sessionsChan <- session
				}
			}
		}()
	}

	// Close sessions channel when all workers are done
	go func() {
		processWg.Wait()
		close(sessionsChan)
	}()

	// Collect results and deduplicate
	sessions := []*models.Session{}
	seenJobs := make(map[string]bool)
	for session := range sessionsChan {
		if !seenJobs[session.JobFilePath] {
			seenJobs[session.JobFilePath] = true
			sessions = append(sessions, session)
		}
	}

	// Wait for collection to finish
	collectWg.Wait()
	if debugTiming {
		fmt.Fprintf(os.Stderr, "[TIMING] Job walking/loading: %v\n", time.Since(walkStart))
	}

	// Update statuses for non-terminal jobs
	updateStart := time.Now()
	terminalStates := map[string]bool{
		"completed": true, "failed": true, "error": true, "abandoned": true,
	}
	for _, session := range sessions {
		if session.Type != "" && session.Type != "claude_session" && !terminalStates[session.Status] {
			updateSessionStatusFromFilesystem(session)
		}
	}
	if debugTiming {
		fmt.Fprintf(os.Stderr, "[TIMING] Status updates: %v\n", time.Since(updateStart))
		fmt.Fprintf(os.Stderr, "[TIMING] TOTAL doFullDiscoveryScan: %v\n", time.Since(startTime))
	}

	return sessions, nil
}

// jobFileWork represents a job file to be processed
type jobFileWork struct {
	path      string
	ownerNode *workspace.WorkspaceNode
}

// cachedJobEntry holds a parsed job with its mtime for incremental updates
type cachedJobEntry struct {
	mtime   time.Time
	session *models.Session
}

// jobMemoryCache provides in-memory caching of parsed jobs with mtime tracking
type jobMemoryCache struct {
	entries map[string]*cachedJobEntry
	mu      sync.RWMutex
}

var globalJobCache = &jobMemoryCache{
	entries: make(map[string]*cachedJobEntry),
}

// getOrParse returns cached job if file hasn't changed, otherwise parses and caches
func (c *jobMemoryCache) getOrParse(path string, ownerNode *workspace.WorkspaceNode, provider *workspace.Provider, genericNoteGroups map[string]bool) *models.Session {
	// Get current file mtime
	stat, err := os.Stat(path)
	if err != nil {
		return nil
	}
	currentMtime := stat.ModTime()

	// Check cache (read lock)
	c.mu.RLock()
	cached, exists := c.entries[path]
	c.mu.RUnlock()

	// If cached and mtime matches, return cached session
	if exists && cached.mtime.Equal(currentMtime) {
		return cached.session
	}

	// File changed or not cached - parse it
	session := parseJobFile(path, ownerNode, provider, genericNoteGroups)
	if session == nil {
		return nil
	}

	// Update cache (write lock)
	c.mu.Lock()
	c.entries[path] = &cachedJobEntry{
		mtime:   currentMtime,
		session: session,
	}
	c.mu.Unlock()

	return session
}

// parseJobFile does the actual job parsing (extracted from processJobFile)
func parseJobFile(path string, ownerNode *workspace.WorkspaceNode, provider *workspace.Provider, genericNoteGroups map[string]bool) *models.Session {
	job, loadErr := orchestration.LoadJob(path)
	if loadErr != nil {
		return nil
	}

	// Only process valid jobs
	if job.Type != "chat" && job.Type != "oneshot" && job.Type != "agent" && job.Type != "interactive_agent" && job.Type != "headless_agent" && job.Type != "shell" {
		return nil
	}

	// Start with the owner of the plan directory as the default context
	effectiveOwnerNode := ownerNode

	// Debug logging for the specific note
	debugThis := strings.Contains(path, "20251112-search-paths-and-nbs")
	if debugThis {
		logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			defer f.Close()
			fmt.Fprintf(f, "\n=== %s ===\n", time.Now().Format("2006-01-02 15:04:05"))
			fmt.Fprintf(f, "Job: %s\n", path)
			fmt.Fprintf(f, "Initial owner: %s (kind=%s, isWorktree=%v)\n", ownerNode.Path, ownerNode.Kind, ownerNode.IsWorktree())
		}
	}

	// If the job's frontmatter specifies a worktree, resolve it
	if job.Worktree != "" {
		resolvedNode := provider.FindByWorktree(ownerNode, job.Worktree)
		if resolvedNode != nil {
			effectiveOwnerNode = resolvedNode
			if debugThis {
				logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
				f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if f != nil {
					fmt.Fprintf(f, "Resolved via frontmatter worktree '%s': %s (kind=%s)\n", job.Worktree, resolvedNode.Path, resolvedNode.Kind)
					f.Close()
				}
			}
		}
	}

	// If this is a generic note and its owner is a worktree, re-assign to the parent
	planName := filepath.Base(filepath.Dir(path))
	if genericNoteGroups[planName] && effectiveOwnerNode.IsWorktree() {
		if debugThis {
			logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
			f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if f != nil {
				fmt.Fprintf(f, "Generic note group '%s' detected, walking parent chain...\n", planName)
				f.Close()
			}
		}

		current := effectiveOwnerNode
		for current != nil && current.IsWorktree() {
			if current.ParentProjectPath != "" {
				parentNode := provider.FindByPath(current.ParentProjectPath)
				if parentNode != nil {
					if debugThis {
						logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
						f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
						if f != nil {
							fmt.Fprintf(f, "  Found parent: %s (kind=%s, isWorktree=%v)\n", parentNode.Path, parentNode.Kind, parentNode.IsWorktree())
							f.Close()
						}
					}
					current = parentNode
				} else {
					if debugThis {
						logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
						f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
						if f != nil {
							fmt.Fprintf(f, "  Parent not found: %s\n", current.ParentProjectPath)
							f.Close()
						}
					}
					break
				}
			} else {
				if debugThis {
					logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
					f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if f != nil {
						fmt.Fprintf(f, "  No ParentProjectPath\n")
						f.Close()
					}
				}
				break
			}
		}
		if current != nil && !current.IsWorktree() {
			effectiveOwnerNode = current
			if debugThis {
				logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
				f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if f != nil {
					fmt.Fprintf(f, "Final owner after walking: %s (kind=%s)\n", current.Path, current.Kind)
					f.Close()
				}
			}
		} else if debugThis {
			logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
			f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if f != nil {
				fmt.Fprintf(f, "Still a worktree after walking, keeping original\n")
				f.Close()
			}
		}
	}

	// Determine repo name and worktree
	repoName := effectiveOwnerNode.Name
	worktreeName := ""
	if effectiveOwnerNode.IsWorktree() {
		if effectiveOwnerNode.ParentProjectPath != "" {
			repoName = filepath.Base(effectiveOwnerNode.ParentProjectPath)
		}
		if string(effectiveOwnerNode.Kind) == "EcosystemWorktreeSubProjectWorktree" {
			worktreeName = filepath.Base(effectiveOwnerNode.ParentEcosystemPath)
		} else {
			worktreeName = effectiveOwnerNode.Name
		}
	}

	if debugThis {
		logPath := utils.ExpandPath("~/.grove/hooks/ownership_debug.log")
		f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if f != nil {
			fmt.Fprintf(f, "Session created: repo=%s, branch=%s, workdir=%s\n", repoName, worktreeName, effectiveOwnerNode.Path)
			f.Close()
		}
	}

	session := &models.Session{
		ID:               job.ID,
		Type:             string(job.Type),
		Status:           string(job.Status),
		Repo:             repoName,
		Branch:           worktreeName,
		WorkingDirectory: effectiveOwnerNode.Path,
		StartedAt:        job.StartTime,
		LastActivity:     job.UpdatedAt,
		PlanName:         filepath.Base(filepath.Dir(path)),
		JobTitle:         job.Title,
		JobFilePath:      path,
	}

	if !job.EndTime.IsZero() {
		session.EndedAt = &job.EndTime
	}

	return session
}

// processJobFile uses incremental caching to avoid re-parsing unchanged files
func processJobFile(work jobFileWork, provider *workspace.Provider, genericNoteGroups map[string]bool) *models.Session {
	return globalJobCache.getOrParse(work.path, work.ownerNode, provider, genericNoteGroups)
}

// DiscoverFlowJobs implements a stale-while-revalidate strategy for fast TUI startup.
func DiscoverFlowJobs() ([]*models.Session, error) {
	// FAST PATH: Always return cached data immediately if available.
	if cachedSessions := tryLoadCacheIgnoreTTL(); cachedSessions != nil {
		// In TUI mode, start the progressive refresh loop in the background.
		// This will only run once per application start.
		if flowJobsBackgroundRefresh {
			// Load config for background refresh
			coreCfg, err := coreconfig.LoadDefault()
			if err != nil {
				coreCfg = &coreconfig.Config{} // Proceed with defaults
			}
			progressiveRefreshOnce.Do(func() {
				startProgressiveRefreshLoop(coreCfg)
			})
		}
		// Return the (potentially stale) cached data right away.
		return cachedSessions, nil
	}

	// COLD START: No cache available. Perform a full blocking scan.
	debugTiming := os.Getenv("GROVE_DEBUG_TIMING") != ""
	if debugTiming {
		startTime := time.Now()
		defer func() {
			fmt.Fprintf(os.Stderr, "[TIMING] Cold start full discovery: %v\n", time.Since(startTime))
		}()
	}

	sessions, err := doFullDiscoveryScan()
	if err != nil {
		return nil, err
	}
	// Write the result to cache so subsequent runs are fast.
	writeCacheFile(sessions)
	return sessions, nil
}

// GetAllSessions fetches sessions from all sources, merges them, and sorts them.
func GetAllSessions(storage interfaces.SessionStorer, hideCompleted bool) ([]*models.Session, error) {
	// Discover live interactive sessions from filesystem (fast - just reads local files)
	liveInteractiveSessions, err := DiscoverLiveInteractiveSessions(storage)
	if err != nil {
		// Log error but continue
		if os.Getenv("GROVE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "Warning: failed to discover live interactive sessions: %v\n", err)
		}
		liveInteractiveSessions = []*models.Session{}
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

	// Add/update with live interactive sessions, which provide the most current "live" status
	for _, session := range liveInteractiveSessions {
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
			existing.ClaudeSessionID = session.ClaudeSessionID
			if session.Provider != "" {
				existing.Provider = session.Provider
			}
		} else {
			// Check if this is a linked claude session for an interactive_agent job
			// Interactive agents store the Claude UUID in ClaudeSessionID field
			matched := false
			for _, existing := range sessionsMap {
				if existing.Type == "interactive_agent" && existing.ClaudeSessionID == session.ID {
					// Found the interactive_agent that manages this claude session
					if existing.Status != "completed" && existing.Status != "failed" && existing.Status != "interrupted" {
						existing.Status = session.Status
					}
					existing.PID = session.PID
					existing.LastActivity = session.LastActivity
					matched = true
					break
				}
			}
			if !matched {
				// This is a standalone claude session, not from a flow job
				sessionsMap[session.ID] = session
			}
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
	// Load config to check notification settings (placeholder for now)
	cfg := &struct {
		Ntfy struct {
			Enabled bool
			URL     string
			Topic   string
		}
		System struct {
			Levels []string
		}
	}{}

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
