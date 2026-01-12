package hooks

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/sirupsen/logrus"
)

var planLog = logging.NewLogger("grove-hooks.plan-preservation")

// PlanPreservationConfig holds configuration for auto-saving plans
type PlanPreservationConfig struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	TargetPlanDir string `yaml:"target_plan_dir" json:"target_plan_dir"` // Optional: override auto-detection
	JobType       string `yaml:"job_type" json:"job_type"`               // Default: "file"
	TitlePrefix   string `yaml:"title_prefix" json:"title_prefix"`       // Optional prefix for title
	KebabCase     bool   `yaml:"kebab_case" json:"kebab_case"`           // Use kebab-case titles (default: true)
	AddDependsOn  string `yaml:"add_depends_on" json:"add_depends_on"`   // Optional: job to depend on
	NotifyOnSave  bool   `yaml:"notify_on_save" json:"notify_on_save"`   // Send notification when plan is saved
}

// DefaultPlanPreservationConfig returns the default configuration
func DefaultPlanPreservationConfig() *PlanPreservationConfig {
	return &PlanPreservationConfig{
		Enabled:      true,
		JobType:      "file",
		TitlePrefix:  "claude-plan",
		KebabCase:    true,
		NotifyOnSave: true,
	}
}

// HandleExitPlanMode processes ExitPlanMode tool events and saves plans to grove-flow
func HandleExitPlanMode(ctx *HookContext, data PostToolUseInput) error {
	// Extract plan content from tool input
	planContent, ok := extractPlanContent(data.ToolInput)
	if !ok || planContent == "" {
		planLog.Debug("No plan content found in ExitPlanMode tool input")
		return nil
	}

	// Get working directory from environment or session
	workingDir := getWorkingDirFromEnv()
	if workingDir == "" {
		planLog.Debug("No working directory available, skipping plan preservation")
		return nil
	}

	// Check if plan preservation is enabled for this directory
	preservationConfig := loadPlanPreservationConfig(workingDir)
	if !preservationConfig.Enabled {
		planLog.WithField("working_dir", workingDir).Debug("Plan preservation disabled for this directory")
		return nil
	}

	// Find the active flow plan for this directory
	planDir, err := findActivePlanDir(workingDir, preservationConfig)
	if err != nil {
		planLog.WithError(err).WithField("working_dir", workingDir).Debug("No active flow plan found")
		return nil // Not an error - just means no plan is active
	}

	// Extract title from plan content
	title := extractPlanTitle(planContent, preservationConfig)

	// Generate a unique job filename
	jobFilename := generateJobFilename(planDir)

	// Save the plan as a new job
	if err := savePlanAsJob(planDir, jobFilename, title, planContent, preservationConfig); err != nil {
		planLog.WithError(err).WithFields(logrus.Fields{
			"plan_dir": planDir,
			"title":    title,
		}).Error("Failed to save plan as job")
		return err
	}

	planLog.WithFields(logrus.Fields{
		"plan_dir":     planDir,
		"job_filename": jobFilename,
		"title":        title,
	}).Info("Successfully saved Claude plan to grove-flow")

	// Send notification if enabled
	if preservationConfig.NotifyOnSave {
		sendPlanSavedNotification(ctx, planDir, jobFilename, title)
	}

	return nil
}

// HandlePlanEdit processes Edit tool events on Claude plan files and syncs to grove-flow
// This captures incremental edits to plans, not just the final ExitPlanMode event
func HandlePlanEdit(ctx *HookContext, data PostToolUseInput) error {
	// Extract file path from Edit tool input
	filePath, ok := extractFilePath(data.ToolInput)
	if !ok || filePath == "" {
		return nil // Not an error, just no file path
	}

	// Check if this is a Claude plan file
	if !isClaudePlanFile(filePath) {
		return nil // Not a plan file, skip
	}

	planLog.WithField("file_path", filePath).Debug("Detected edit to Claude plan file")

	// Read the updated file content
	planContent, err := os.ReadFile(filePath)
	if err != nil {
		planLog.WithError(err).WithField("file_path", filePath).Debug("Failed to read edited plan file")
		return nil // Best effort - don't fail if we can't read
	}

	if len(planContent) == 0 {
		planLog.Debug("Plan file is empty, skipping sync")
		return nil
	}

	// Get working directory from environment or session
	workingDir := getWorkingDirFromEnv()
	if workingDir == "" {
		planLog.Debug("No working directory available, skipping plan sync")
		return nil
	}

	// Check if plan preservation is enabled for this directory
	preservationConfig := loadPlanPreservationConfig(workingDir)
	if !preservationConfig.Enabled {
		planLog.WithField("working_dir", workingDir).Debug("Plan preservation disabled for this directory")
		return nil
	}

	// Find the active flow plan for this directory
	planDir, err := findActivePlanDir(workingDir, preservationConfig)
	if err != nil {
		planLog.WithError(err).WithField("working_dir", workingDir).Debug("No active flow plan found")
		return nil // Not an error - just means no plan is active
	}

	// Extract title from plan content
	title := extractPlanTitle(string(planContent), preservationConfig)
	rawTitle := extractRawPlanTitle(string(planContent))

	// Check if we already have a job for this plan file (by raw title without prefix)
	// If so, update it instead of creating a new one
	existingJob := findExistingPlanJob(planDir, rawTitle)
	if existingJob != "" {
		// Update existing job
		if err := updatePlanJob(existingJob, string(planContent)); err != nil {
			planLog.WithError(err).WithFields(logrus.Fields{
				"plan_dir": planDir,
				"job_file": existingJob,
			}).Error("Failed to update existing plan job")
			return err
		}
		planLog.WithFields(logrus.Fields{
			"plan_dir": planDir,
			"job_file": existingJob,
			"title":    title,
		}).Info("Updated existing plan job from Edit")
	} else {
		// Create new job
		jobFilename := generateJobFilename(planDir)
		if err := savePlanAsJob(planDir, jobFilename, title, string(planContent), preservationConfig); err != nil {
			planLog.WithError(err).WithFields(logrus.Fields{
				"plan_dir": planDir,
				"title":    title,
			}).Error("Failed to save plan as job")
			return err
		}
		planLog.WithFields(logrus.Fields{
			"plan_dir":     planDir,
			"job_filename": jobFilename,
			"title":        title,
		}).Info("Created new plan job from Edit")
	}

	// Send notification if enabled
	if preservationConfig.NotifyOnSave {
		sendPlanSavedNotification(ctx, planDir, "", title)
	}

	return nil
}

// extractFilePath extracts the file_path from Edit tool input
func extractFilePath(toolInput any) (string, bool) {
	if inputMap, ok := toolInput.(map[string]any); ok {
		if fp, exists := inputMap["file_path"]; exists {
			if fpStr, ok := fp.(string); ok {
				return fpStr, true
			}
		}
	}
	if inputMap, ok := toolInput.(map[string]interface{}); ok {
		if fp, exists := inputMap["file_path"]; exists {
			if fpStr, ok := fp.(string); ok {
				return fpStr, true
			}
		}
	}
	return "", false
}

// isClaudePlanFile checks if the given path is a Claude plan file
func isClaudePlanFile(filePath string) bool {
	// Expand ~ to home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	claudePlansDir := filepath.Join(homeDir, ".claude", "plans")

	// Check if filePath starts with the Claude plans directory
	return strings.HasPrefix(filePath, claudePlansDir)
}

// findExistingPlanJob looks for an existing job file with matching title
// It matches on the raw title (from # heading) without the claude-plan prefix
func findExistingPlanJob(planDir, rawTitle string) string {
	entries, err := os.ReadDir(planDir)
	if err != nil {
		return ""
	}

	// Convert raw title to kebab-case for matching (without prefix)
	kebabRawTitle := toKebabCase("", rawTitle)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(planDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		contentStr := string(content)
		lines := strings.Split(contentStr, "\n")

		// First, check the # heading in the body (most reliable)
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "# ") {
				fileTitle := strings.TrimPrefix(line, "# ")
				if toKebabCase("", fileTitle) == kebabRawTitle {
					return filePath
				}
				break // Only check first heading
			}
		}

		// Also check frontmatter title field (handles existing jobs)
		if strings.HasPrefix(contentStr, "---") {
			// Parse frontmatter for title
			parts := strings.SplitN(contentStr, "---", 3)
			if len(parts) >= 3 {
				frontmatter := parts[1]
				for _, line := range strings.Split(frontmatter, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "title:") {
						fmTitle := strings.TrimSpace(strings.TrimPrefix(line, "title:"))
						// Remove quotes if present
						fmTitle = strings.Trim(fmTitle, "\"'")
						// Check if frontmatter title contains the raw title (handles prefixed titles)
						if strings.Contains(toKebabCase("", fmTitle), kebabRawTitle) {
							return filePath
						}
						break
					}
				}
			}
		}
	}

	return ""
}

// updatePlanJob updates an existing plan job file with new content
func updatePlanJob(jobFilePath, newContent string) error {
	// For now, just overwrite the file content
	// In the future, we might want to preserve frontmatter or other metadata
	return os.WriteFile(jobFilePath, []byte(newContent), 0644)
}

// extractPlanContent extracts the plan content from tool input
func extractPlanContent(toolInput any) (string, bool) {
	// Handle map[string]any (most common case)
	if inputMap, ok := toolInput.(map[string]any); ok {
		if plan, exists := inputMap["plan"]; exists {
			if planStr, ok := plan.(string); ok {
				return planStr, true
			}
		}
	}

	// Handle map[string]interface{}
	if inputMap, ok := toolInput.(map[string]interface{}); ok {
		if plan, exists := inputMap["plan"]; exists {
			if planStr, ok := plan.(string); ok {
				return planStr, true
			}
		}
	}

	return "", false
}

// getWorkingDirFromEnv gets the working directory from environment
func getWorkingDirFromEnv() string {
	// Try PWD first
	if pwd := os.Getenv("PWD"); pwd != "" {
		return pwd
	}
	// Fall back to os.Getwd()
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// loadPlanPreservationConfig loads configuration from grove.yml and environment
func loadPlanPreservationConfig(workingDir string) *PlanPreservationConfig {
	cfg := DefaultPlanPreservationConfig()

	// Try to load from grove.yml
	groveCfg, err := config.LoadFrom(workingDir)
	if err == nil && groveCfg != nil {
		// Check for hooks.plan_preservation configuration
		var hooksConfig struct {
			PlanPreservation *PlanPreservationConfig `yaml:"plan_preservation"`
		}
		if err := groveCfg.UnmarshalExtension("hooks", &hooksConfig); err == nil && hooksConfig.PlanPreservation != nil {
			// Merge with defaults (only override non-zero values)
			if hooksConfig.PlanPreservation.JobType != "" {
				cfg.JobType = hooksConfig.PlanPreservation.JobType
			}
			if hooksConfig.PlanPreservation.TitlePrefix != "" {
				cfg.TitlePrefix = hooksConfig.PlanPreservation.TitlePrefix
			}
			if hooksConfig.PlanPreservation.TargetPlanDir != "" {
				cfg.TargetPlanDir = hooksConfig.PlanPreservation.TargetPlanDir
			}
			if hooksConfig.PlanPreservation.AddDependsOn != "" {
				cfg.AddDependsOn = hooksConfig.PlanPreservation.AddDependsOn
			}
			// Booleans need explicit handling since false is a valid value
			cfg.Enabled = hooksConfig.PlanPreservation.Enabled
			cfg.KebabCase = hooksConfig.PlanPreservation.KebabCase
			cfg.NotifyOnSave = hooksConfig.PlanPreservation.NotifyOnSave
		}
	}

	// Environment variable overrides (highest priority)
	if os.Getenv("GROVE_HOOKS_DISABLE_PLAN_PRESERVATION") == "true" {
		cfg.Enabled = false
	}
	if os.Getenv("GROVE_HOOKS_ENABLE_PLAN_PRESERVATION") == "true" {
		cfg.Enabled = true
	}
	if targetDir := os.Getenv("GROVE_HOOKS_TARGET_PLAN_DIR"); targetDir != "" {
		cfg.TargetPlanDir = targetDir
	}

	return cfg
}

// findActivePlanDir finds the active grove-flow plan directory for the working directory
func findActivePlanDir(workingDir string, preservationConfig *PlanPreservationConfig) (string, error) {
	// If explicitly configured, use that
	if preservationConfig.TargetPlanDir != "" {
		if _, err := os.Stat(preservationConfig.TargetPlanDir); err == nil {
			return preservationConfig.TargetPlanDir, nil
		}
		return "", fmt.Errorf("configured target_plan_dir does not exist: %s", preservationConfig.TargetPlanDir)
	}

	// Try to get the current active plan from flow CLI
	cmd := exec.Command("flow", "plan", "current")
	cmd.Dir = workingDir
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("no active flow plan: %w", err)
	}

	// Parse output to get plan name
	// Expected format: "Active job: <name>"
	outputStr := strings.TrimSpace(string(output))
	if !strings.HasPrefix(outputStr, "Active job:") {
		return "", fmt.Errorf("unexpected flow plan current output: %s", outputStr)
	}

	planName := strings.TrimSpace(strings.TrimPrefix(outputStr, "Active job:"))
	if planName == "" {
		return "", fmt.Errorf("empty plan name from flow plan current")
	}

	// Use grove-core's NotebookLocator to find the plans directory
	planDir, err := resolvePlanDirUsingNotebookLocator(workingDir, planName)
	if err != nil {
		// Fall back to legacy resolution if NotebookLocator fails
		planLog.WithError(err).Debug("NotebookLocator failed, trying legacy resolution")
		return resolvePlanNameToPathLegacy(workingDir, planName)
	}

	return planDir, nil
}

// resolvePlanDirUsingNotebookLocator uses grove-core's workspace and config APIs
// to properly resolve the plan directory path
func resolvePlanDirUsingNotebookLocator(workingDir, planName string) (string, error) {
	// Get the workspace node for this directory
	node, err := workspace.GetProjectByPath(workingDir)
	if err != nil {
		return "", fmt.Errorf("failed to get project for path %s: %w", workingDir, err)
	}

	// Load the grove config
	cfg, err := config.LoadDefault()
	if err != nil {
		// Config might not exist, use default notebook locator
		cfg = &config.Config{}
	}

	// Create notebook locator
	locator := workspace.NewNotebookLocator(cfg)

	// Get the plans directory for this workspace
	plansBaseDir, err := locator.GetPlansDir(node)
	if err != nil {
		return "", fmt.Errorf("failed to get plans directory: %w", err)
	}

	// The plans directory contains subdirectories for each plan
	// Append the plan name to get the specific plan directory
	planDir := filepath.Join(plansBaseDir, planName)

	// Verify it exists
	if _, err := os.Stat(planDir); err != nil {
		return "", fmt.Errorf("plan directory does not exist: %s", planDir)
	}

	return planDir, nil
}

// resolvePlanNameToPathLegacy is the fallback path resolution when NotebookLocator fails
func resolvePlanNameToPathLegacy(workingDir, planName string) (string, error) {
	// Determine the repo name - handle worktrees specially
	repoName := filepath.Base(workingDir)

	// Check if this is a worktree path (contains .grove-worktrees)
	if strings.Contains(workingDir, ".grove-worktrees") {
		// Extract repo name from path like /path/to/repo/.grove-worktrees/branch-name
		parts := strings.Split(workingDir, ".grove-worktrees")
		if len(parts) > 0 {
			repoName = filepath.Base(strings.TrimSuffix(parts[0], "/"))
		}
	}

	// Check common locations for the plan
	possiblePaths := []string{
		// Notebooks workspace pattern with repo name
		os.ExpandEnv(fmt.Sprintf("$HOME/notebooks/nb/workspaces/%s/plans/%s", repoName, planName)),
		// Notebooks workspace pattern with plan name as workspace (for matching plan names)
		os.ExpandEnv(fmt.Sprintf("$HOME/notebooks/nb/workspaces/%s/plans/%s", planName, planName)),
		// Direct plan directory in working dir
		filepath.Join(workingDir, "plans", planName),
		// Parent directory plans
		filepath.Join(filepath.Dir(workingDir), "plans", planName),
		// For worktrees, check the main repo's plans directory
		filepath.Join(strings.Split(workingDir, ".grove-worktrees")[0], "plans", planName),
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("could not find plan directory for: %s", planName)
}

// extractRawPlanTitle extracts just the raw title from plan content without any formatting
func extractRawPlanTitle(planContent string) string {
	lines := strings.Split(planContent, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			rawTitle := strings.TrimPrefix(line, "# ")
			// Remove "Plan:" prefix if already present
			rawTitle = strings.TrimPrefix(rawTitle, "Plan:")
			return strings.TrimSpace(rawTitle)
		}
	}

	return ""
}

// extractPlanTitle extracts a title from the plan content with optional formatting
func extractPlanTitle(planContent string, cfg *PlanPreservationConfig) string {
	rawTitle := extractRawPlanTitle(planContent)

	// Fall back to timestamp if no title found
	if rawTitle == "" {
		rawTitle = time.Now().Format("2006-01-02-1504")
	}

	// Format the title
	if cfg.KebabCase {
		return toKebabCase(cfg.TitlePrefix, rawTitle)
	}

	// Non-kebab: use prefix with space
	if cfg.TitlePrefix != "" {
		return fmt.Sprintf("%s %s", cfg.TitlePrefix, rawTitle)
	}
	return rawTitle
}

// toKebabCase converts a title to kebab-case with optional prefix
func toKebabCase(prefix, title string) string {
	// Convert to lowercase
	result := strings.ToLower(title)

	// Replace common separators and special chars with hyphens
	replacer := strings.NewReplacer(
		" ", "-",
		"_", "-",
		":", "-",
		"/", "-",
		"\\", "-",
		".", "-",
		",", "",
		"'", "",
		"\"", "",
		"(", "",
		")", "",
		"[", "",
		"]", "",
	)
	result = replacer.Replace(result)

	// Remove consecutive hyphens
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	// Trim leading/trailing hyphens
	result = strings.Trim(result, "-")

	// Add prefix if provided
	if prefix != "" {
		return prefix + "-" + result
	}
	return result
}

// generateJobFilename generates a unique filename for the plan job
func generateJobFilename(planDir string) string {
	// Find existing jobs to determine next number
	entries, err := os.ReadDir(planDir)
	if err != nil {
		return "99-claude-plan.md"
	}

	maxNum := 0
	pattern := regexp.MustCompile(`^(\d+)-`)

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			matches := pattern.FindStringSubmatch(entry.Name())
			if len(matches) > 1 {
				var num int
				fmt.Sscanf(matches[1], "%d", &num)
				if num > maxNum {
					maxNum = num
				}
			}
		}
	}

	// Generate next number with leading zeros
	nextNum := maxNum + 1
	timestamp := time.Now().Format("150405") // HHMMSS
	return fmt.Sprintf("%02d-claude-plan-%s.md", nextNum, timestamp)
}

// savePlanAsJob saves the plan content as a new grove-flow job
func savePlanAsJob(planDir, jobFilename, title, planContent string, preservationConfig *PlanPreservationConfig) error {
	// Build the flow plan add command
	args := []string{"plan", "add", planDir,
		"--type", preservationConfig.JobType,
		"--title", title,
	}

	// Add depends_on if configured
	if preservationConfig.AddDependsOn != "" {
		args = append(args, "--depends-on", preservationConfig.AddDependsOn)
	}

	cmd := exec.Command("flow", args...)

	// Pass plan content via stdin
	cmd.Stdin = strings.NewReader(planContent)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("flow plan add failed: %w\nstdout: %s\nstderr: %s",
			err, stdout.String(), stderr.String())
	}

	planLog.WithFields(logrus.Fields{
		"plan_dir":     planDir,
		"job_filename": jobFilename,
		"stdout":       stdout.String(),
	}).Debug("flow plan add command output")

	return nil
}

// sendPlanSavedNotification sends a notification when a plan is saved
func sendPlanSavedNotification(ctx *HookContext, planDir, jobFilename, title string) {
	// Log an event for the plan save
	eventData := map[string]any{
		"plan_dir":     planDir,
		"job_filename": jobFilename,
		"title":        title,
		"action":       "plan_preserved",
	}

	if err := ctx.LogEvent(models.EventType("plan_preserved"), eventData); err != nil {
		log.Printf("Failed to log plan preservation event: %v", err)
	}
}
