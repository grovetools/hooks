package api

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "syscall"
    "time"
    
    "github.com/mattsolo1/grove-core/pkg/models"
    "github.com/mattsolo1/grove-tmux/pkg/tmux"
    "gopkg.in/yaml.v3"
)

// HookBlockingError represents an error that should block the hook operation
type HookBlockingError struct {
    Message string
}

func (e *HookBlockingError) Error() string {
    return e.Message
}

// BaseHookInput contains fields common to all hooks
type BaseHookInput struct {
    SessionID      string `json:"session_id"`
    TranscriptPath string `json:"transcript_path,omitempty"`
    HookEventName  string `json:"hook_event_name"`
    // Current transcript position (if available)
    CurrentUUID    string `json:"current_uuid,omitempty"`
    ParentUUID     string `json:"parent_uuid,omitempty"`
}

// HookContext provides common functionality for all hooks
type HookContext struct {
    Input        BaseHookInput
    RawInput     []byte
    APIClient    *APIClient
    StartTime    time.Time
}

// Config represents the server configuration (copied from api_client.go)
type Config struct {
    Server struct {
        Port int `yaml:"port"`
    } `yaml:"server"`
    ToolSummarization struct {
        Enabled       bool     `yaml:"enabled"`
        LLMCommand    string   `yaml:"llm_command"`
        MaxOutputSize int      `yaml:"max_output_size"`
        ToolWhitelist []string `yaml:"tool_whitelist"`
    } `yaml:"tool_summarization"`
}

// APIClient handles communication with the canopy API
type APIClient struct {
    baseURL string
    client  *http.Client
}

func NewHookContext() (*HookContext, error) {
    // Read stdin
    inputData, err := io.ReadAll(os.Stdin)
    if err != nil {
        return nil, err
    }
    
    // Parse base input
    var baseInput BaseHookInput
    if err := json.Unmarshal(inputData, &baseInput); err != nil {
        return nil, err
    }
    
    return &HookContext{
        Input:       baseInput,
        RawInput:    inputData,
        APIClient:   NewAPIClient(),
        StartTime:   time.Now(),
    }, nil
}

func (hc *HookContext) LogEvent(eventType models.EventType, data map[string]any) error {
    event := &models.Event{
        SessionID:      hc.Input.SessionID,
        Type:           eventType,
        Timestamp:      time.Now(),
        TranscriptPath: hc.Input.TranscriptPath,
        TranscriptUUID: hc.Input.CurrentUUID,
        ParentUUID:     hc.Input.ParentUUID,
        Metadata: models.EventMetadata{
            Version: "1.0",
            Source:  hc.Input.HookEventName,
        },
    }
    if err := event.SetDataFromMap(data); err != nil {
        return err
    }
    
    // For now, just log to stdout in debug mode
    if os.Getenv("GROVE_DEBUG") != "" {
        log.Printf("Event: %s - %v", eventType, data)
    }
    
    // TODO: Send event to API or event store
    return nil
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
    if strings.HasPrefix(path, "~/") {
        home, err := os.UserHomeDir()
        if err == nil {
            return filepath.Join(home, path[2:])
        }
    }
    return path
}

// loadAPIURL reads the config file to get the server port
func loadAPIURL() string {
    // Priority: CANOPY_API_URL -> TMUX_CLAUDE_API_URL -> Config File -> Default
    if url := os.Getenv("CANOPY_API_URL"); url != "" {
        return url
    }
    
    if url := os.Getenv("TMUX_CLAUDE_API_URL"); url != "" {
        return url
    }
    
    defaultAPIURL := "http://localhost:8888/api"
    
    // Try to read from config file
    configPath := expandPath("~/.config/canopy/config.yaml")
    data, err := os.ReadFile(configPath)
    if err != nil {
        return defaultAPIURL
    }
    
    var config Config
    if err := yaml.Unmarshal(data, &config); err != nil {
        return defaultAPIURL
    }
    
    if config.Server.Port > 0 {
        return fmt.Sprintf("http://localhost:%d/api", config.Server.Port)
    }
    
    return defaultAPIURL
}

// NewAPIClient creates a new API client
func NewAPIClient() *APIClient {
    baseURL := loadAPIURL()
    
    return &APIClient{
        baseURL: baseURL,
        client: &http.Client{
            Timeout: 5 * time.Second,
        },
    }
}

// doRequest performs an HTTP request
func (c *APIClient) doRequest(method, path string, payload any) ([]byte, error) {
    var body []byte
    var err error
    
    if payload != nil {
        body, err = json.Marshal(payload)
        if err != nil {
            return nil, fmt.Errorf("failed to marshal payload: %w", err)
        }
    }
    
    req, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }
    
    req.Header.Set("Content-Type", "application/json")
    
    // Add Authorization header if token is available
    if token := os.Getenv("CANOPY_API_SECRET_TOKEN"); token != "" {
        req.Header.Set("Authorization", "Bearer "+token)
    }
    
    resp, err := c.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()
    
    var respBody bytes.Buffer
    if _, err := respBody.ReadFrom(resp.Body); err != nil {
        return nil, fmt.Errorf("failed to read response: %w", err)
    }
    
    if resp.StatusCode >= 400 {
        return nil, fmt.Errorf("API error: %s (status %d): %s", path, resp.StatusCode, respBody.String())
    }
    
    return respBody.Bytes(), nil
}

// LoadConfig loads the application configuration
func (hc *HookContext) LoadConfig() (map[string]interface{}, error) {
	configPath := expandPath("~/.config/canopy/config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	
	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	
	return config, nil
}

// GetSession gets session details
func (c *APIClient) GetSession(sessionID string) (*Session, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/claude-sessions/%s", sessionID), nil)
	if err != nil {
		return nil, err
	}
	
	var session Session
	if err := json.Unmarshal(resp, &session); err != nil {
		return nil, fmt.Errorf("failed to parse session: %w", err)
	}
	
	return &session, nil
}

// Session represents a Claude session
type Session struct {
	ID               string `json:"id"`
	Repo             string `json:"repo"`
	Branch           string `json:"branch"`
	Status           string `json:"status"`
	WorkingDirectory string `json:"working_directory"`
}

// Common API methods that all hooks might need

// LogToolUsage logs tool usage before execution
func (c *APIClient) LogToolUsage(sessionID, toolName string, params map[string]any, approved bool, blockedReason string) (string, error) {
    payload := map[string]any{
        "timestamp":      time.Now().Format(time.RFC3339),
        "tool_name":      toolName,
        "parameters":     params,
        "approved":       approved,
        "blocked_reason": blockedReason,
    }
    
    resp, err := c.doRequest("POST", fmt.Sprintf("/claude-sessions/%s/tools", sessionID), payload)
    if err != nil {
        return "", err
    }
    
    var result map[string]string
    if err := json.Unmarshal(resp, &result); err != nil {
        return "", err
    }
    
    return result["tool_id"], nil
}

// UpdateToolExecution updates tool completion status
func (c *APIClient) UpdateToolExecution(sessionID, toolID string, durationMs int64, success bool, resultSummary map[string]any, errorMsg string) error {
    payload := map[string]any{
        "completed_at": time.Now().Format(time.RFC3339),
        "duration_ms":  durationMs,
        "success":      success,
        "error":        errorMsg,
    }
    
    if resultSummary != nil {
        payload["result_summary"] = resultSummary
    }
    
    _, err := c.doRequest("PUT", fmt.Sprintf("/claude-sessions/%s/tools/%s", sessionID, toolID), payload)
    return err
}

// LogNotification logs a Claude notification
func (c *APIClient) LogNotification(sessionID, notificationType, level, message string, systemNotificationSent bool) error {
    payload := map[string]any{
        "timestamp":                time.Now().Format(time.RFC3339),
        "type":                     notificationType,
        "level":                    level,
        "message":                  message,
        "system_notification_sent": systemNotificationSent,
    }
    
    _, err := c.doRequest("POST", fmt.Sprintf("/claude-sessions/%s/notifications", sessionID), payload)
    return err
}

// UpdateSession updates a session's status
func (c *APIClient) UpdateSession(sessionID string, status string) error {
    payload := map[string]any{
        "status":        status,
        "last_activity": time.Now().Format(time.RFC3339),
    }
    
    _, err := c.doRequest("PUT", fmt.Sprintf("/claude-sessions/%s", sessionID), payload)
    return err
}

// CompleteSession marks a session as complete
func (c *APIClient) CompleteSession(sessionID string, durationSeconds int, exitStatus string, summary map[string]any) error {
    payload := map[string]any{
        "ended_at":         time.Now().Format(time.RFC3339),
        "duration_seconds": durationSeconds,
        "exit_status":      exitStatus,
        "session_summary":  summary,
    }
    
    _, err := c.doRequest("PUT", fmt.Sprintf("/claude-sessions/%s/complete", sessionID), payload)
    return err
}

// LogSubagent logs subagent execution
func (c *APIClient) LogSubagent(sessionID, subagentID, taskDescription, taskType string, durationSeconds int, status string, result map[string]any) error {
    payload := map[string]any{
        "subagent_id":      subagentID,
        "parent_session_id": sessionID,
        "task_description":  taskDescription,
        "task_type":         taskType,
        "completed_at":      time.Now().Format(time.RFC3339),
        "duration_seconds":  durationSeconds,
        "status":            status,
        "result":            result,
    }
    
    _, err := c.doRequest("POST", fmt.Sprintf("/claude-sessions/%s/subagents", sessionID), payload)
    return err
}

// EnsureSessionExists creates a session if it doesn't exist or updates idle sessions to running
func (c *APIClient) EnsureSessionExists(sessionID string, transcriptPath string) error {
    // First check if session exists
    resp, err := c.doRequest("GET", fmt.Sprintf("/claude-sessions/%s", sessionID), nil)
    if err == nil {
        // Session exists - check if it's idle and needs to be set back to running
        var session map[string]any
        if err := json.Unmarshal(resp, &session); err == nil {
            if status, ok := session["status"].(string); ok && status == "idle" {
                // Update idle session back to running
                updatePayload := map[string]any{
                    "status": "running",
                    "last_activity": time.Now().Format(time.RFC3339),
                }
                _, updateErr := c.doRequest("PUT", fmt.Sprintf("/claude-sessions/%s", sessionID), updatePayload)
                if updateErr != nil {
                    return fmt.Errorf("failed to update idle session to running: %w", updateErr)
                }
            }
        }
        return nil
    }
    
    // Extract working directory from transcript path
    // Try to get it from environment first
    workingDir := os.Getenv("PWD")
    if workingDir == "" {
        // Fallback to current directory
        workingDir, _ = os.Getwd()
    }
    if workingDir == "" {
        workingDir = "."
    }
    
    // Get git info
    repo := "unknown"
    branch := "unknown"
    
    // Try to get git info from the working directory
    cmd := exec.Command("git", "-C", workingDir, "rev-parse", "--show-toplevel")
    if out, err := cmd.Output(); err == nil {
        repoPath := strings.TrimSpace(string(out))
        repo = filepath.Base(repoPath)
    } else {
        // If git fails, use the directory name as repo name
        repo = filepath.Base(workingDir)
        if repo == "" || repo == "." || repo == "/" {
            repo = "no-repo"
        }
    }
    
    cmd = exec.Command("git", "-C", workingDir, "rev-parse", "--abbrev-ref", "HEAD")
    if out, err := cmd.Output(); err == nil {
        branch = strings.TrimSpace(string(out))
    } else {
        // Default branch name when not in git
        branch = "no-branch"
    }
    
    // Get current user
    user := os.Getenv("USER")
    if user == "" {
        user = "unknown"
    }
    
    // Detect tmux key using tmux manager
    tmuxKey := ""
    configDir := expandPath("~/.config/canopy")
    sessionsFile := filepath.Join(configDir, "tmux-sessions.yaml")
    tmuxMgr := tmux.NewManager(configDir, sessionsFile)
    if tmuxMgr != nil {
        tmuxKey = tmuxMgr.DetectTmuxKeyForPath(workingDir)
    }
    
    // Create session
    payload := map[string]any{
        "session_id":        sessionID,
        "type":              "claude",
        "source":            "production",
        "pid":               os.Getpid(),
        "repo":              repo,
        "branch":            branch,
        "tmux_key":          tmuxKey,
        "working_directory": workingDir,
        "user":              user,
        "started_at":        time.Now().Format(time.RFC3339),
        "status":            "running",
    }
    
    _, err = c.doRequest("POST", "/claude-sessions", payload)
    if err != nil {
        return err
    }
    
    // Trigger SDK monitoring for this CLI session
    sdkPayload := map[string]any{
        "session_id":      sessionID,
        "transcript_path": transcriptPath,
    }
    
    _, err = c.doRequest("POST", "/sdk/sessions", sdkPayload)
    if err != nil {
        // Log error but don't fail - session creation is more important
        // SDK monitoring is a nice-to-have feature
        fmt.Fprintf(os.Stderr, "Failed to enable SDK monitoring for session %s: %v\n", sessionID, err)
    }
    
    return nil
}

// LoadRepoHookConfig loads .canopy.yaml from the specified directory
func LoadRepoHookConfig(workingDir string) (*models.RepoHookConfig, error) {
    configPath := filepath.Join(workingDir, ".canopy.yaml")
    
    // Check if file exists
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        return nil, nil // No config file found, not an error
    }
    
    // Read and parse the file
    data, err := os.ReadFile(configPath)
    if err != nil {
        return nil, fmt.Errorf("failed to read .canopy.yaml: %w", err)
    }
    
    var config models.RepoHookConfig
    if err := yaml.Unmarshal(data, &config); err != nil {
        return nil, fmt.Errorf("failed to parse .canopy.yaml: %w", err)
    }
    
    return &config, nil
}

// ExecuteRepoHookCommands executes on_stop commands from .canopy.yaml
func (hc *HookContext) ExecuteRepoHookCommands(workingDir string) error {
    config, err := LoadRepoHookConfig(workingDir)
    if err != nil {
        return fmt.Errorf("failed to load repo hook config: %w", err)
    }
    
    if config == nil || len(config.Hooks.OnStop) == 0 {
        // No commands to execute
        return nil
    }
    
    log.Printf("Found %d on_stop commands in .canopy.yaml", len(config.Hooks.OnStop))
    
    for i, hookCmd := range config.Hooks.OnStop {
        log.Printf("Executing hook command %d: %s", i+1, hookCmd.Name)
        
        // Check run_if condition
        if hookCmd.RunIf == "changes" {
            hasChanges, err := hasGitChanges(workingDir)
            if err != nil {
                log.Printf("Failed to check git changes for command '%s': %v", hookCmd.Name, err)
                continue
            }
            if !hasChanges {
                log.Printf("Skipping command '%s' - no git changes detected", hookCmd.Name)
                continue
            }
        }
        
        // Execute the command
        if err := executeHookCommand(workingDir, hookCmd); err != nil {
            log.Printf("Hook command '%s' failed: %v", hookCmd.Name, err)
            
            // Check if this is a blocking error (exit code 2)
            if blockingErr, ok := err.(*HookBlockingError); ok {
                log.Printf("Hook command '%s' returned blocking error, stopping session", hookCmd.Name)
                
                // Log event for blocking command
                eventData := map[string]any{
                    "hook_command": hookCmd.Name,
                    "command":      hookCmd.Command,
                    "success":      false,
                    "error":        blockingErr.Message,
                    "blocking":     true,
                }
                if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
                    log.Printf("Failed to log hook command blocking failure: %v", logErr)
                }
                
                // Return the blocking error to prevent session stop
                return blockingErr
            }
            
            // Log event for non-blocking failed command
            eventData := map[string]any{
                "hook_command": hookCmd.Name,
                "command":      hookCmd.Command,
                "success":      false,
                "error":        err.Error(),
                "blocking":     false,
            }
            if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
                log.Printf("Failed to log hook command failure: %v", logErr)
            }
            
            // Continue with other commands for non-blocking errors
        } else {
            log.Printf("Hook command '%s' completed successfully", hookCmd.Name)
            
            // Log event for successful command
            eventData := map[string]any{
                "hook_command": hookCmd.Name,
                "command":      hookCmd.Command,
                "success":      true,
                "blocking":     false,
            }
            if logErr := hc.LogEvent(models.EventStop, eventData); logErr != nil {
                log.Printf("Failed to log hook command success: %v", logErr)
            }
        }
    }
    
    return nil
}

// hasGitChanges checks if there are any git changes in the working directory
func hasGitChanges(workingDir string) (bool, error) {
    // Check for staged changes
    cmd := exec.Command("git", "diff", "--cached", "--quiet")
    cmd.Dir = workingDir
    if err := cmd.Run(); err != nil {
        if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
            return true, nil // Changes detected
        }
        return false, fmt.Errorf("git diff --cached failed: %w", err)
    }
    
    // Check for unstaged changes
    cmd = exec.Command("git", "diff", "--quiet")
    cmd.Dir = workingDir
    if err := cmd.Run(); err != nil {
        if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
            return true, nil // Changes detected
        }
        return false, fmt.Errorf("git diff failed: %w", err)
    }
    
    // Check for untracked files
    cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
    cmd.Dir = workingDir
    output, err := cmd.Output()
    if err != nil {
        return false, fmt.Errorf("git ls-files failed: %w", err)
    }
    
    return len(strings.TrimSpace(string(output))) > 0, nil
}

// executeHookCommand executes a single hook command
func executeHookCommand(workingDir string, hookCmd models.HookCommand) error {
    log.Printf("Running: %s", hookCmd.Command)
    
    cmd := exec.Command("sh", "-c", hookCmd.Command)
    cmd.Dir = workingDir
    
    // Capture stderr to handle exit code 2 blocking behavior
    var stderrBuf bytes.Buffer
    cmd.Stdout = os.Stdout
    cmd.Stderr = &stderrBuf
    
    err := cmd.Run()
    if err != nil {
        // Check if this is an exit error and get the exit code
        if exitError, ok := err.(*exec.ExitError); ok {
            if ws, ok := exitError.Sys().(syscall.WaitStatus); ok {
                exitCode := ws.ExitStatus()
                stderrOutput := strings.TrimSpace(stderrBuf.String())
                
                // Exit code 2 means blocking error - feed stderr back to Claude
                if exitCode == 2 {
                    if stderrOutput != "" {
                        return &HookBlockingError{Message: stderrOutput}
                    } else {
                        return &HookBlockingError{Message: fmt.Sprintf("Hook command '%s' failed with blocking error (exit code 2)", hookCmd.Name)}
                    }
                }
                
                // For other exit codes, include stderr in the error but don't block
                if stderrOutput != "" {
                    return fmt.Errorf("command failed with exit code %d: %s", exitCode, stderrOutput)
                }
            }
        }
        
        // For other types of errors, return as-is
        return err
    }
    
    return nil
}