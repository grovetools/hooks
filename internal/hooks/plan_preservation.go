package hooks

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/workspace"
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

	// Try to find the Claude plan file by content matching
	// This gives us a stable identifier for deduplication
	sourceFile := ""
	if claudeFilename := findClaudePlanFileByContent(planContent); claudeFilename != "" {
		sourceFile = "claude://plans/" + claudeFilename
	}

	// Extract title from plan content
	title := extractPlanTitle(planContent, preservationConfig)
	rawTitle := extractRawPlanTitle(planContent)

	planLog.WithFields(logrus.Fields{
		"source_file": sourceFile,
		"title":       title,
		"raw_title":   rawTitle,
	}).Debug("Processing ExitPlanMode")

	// Find existing job by source_file (primary) or title (fallback)
	existingJob := findExistingPlanJob(planDir, sourceFile, rawTitle)

	if existingJob != "" {
		// Update existing job
		if err := updatePlanJob(existingJob, planContent, sourceFile); err != nil {
			planLog.WithError(err).WithFields(logrus.Fields{
				"plan_dir": planDir,
				"job_file": existingJob,
			}).Error("Failed to update existing plan job")
			return err
		}
		planLog.WithFields(logrus.Fields{
			"plan_dir":    planDir,
			"job_file":    existingJob,
			"title":       title,
			"source_file": sourceFile,
		}).Info("Updated existing plan job from ExitPlanMode")
	} else {
		// Create new job
		if err := savePlanAsJob(planDir, title, planContent, sourceFile, preservationConfig); err != nil {
			planLog.WithError(err).WithFields(logrus.Fields{
				"plan_dir": planDir,
				"title":    title,
			}).Error("Failed to save plan as job")
			return err
		}
		planLog.WithFields(logrus.Fields{
			"plan_dir":    planDir,
			"title":       title,
			"source_file": sourceFile,
		}).Info("Successfully saved Claude plan to grove-flow")
	}

	// Send notification if enabled
	if preservationConfig.NotifyOnSave {
		sendPlanSavedNotification(ctx, planDir, title)
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

	// Extract the Claude plan filename and format as URI for stable identification
	sourceFile := "claude://plans/" + filepath.Base(filePath)

	planLog.WithFields(logrus.Fields{
		"file_path":   filePath,
		"source_file": sourceFile,
	}).Debug("Detected edit to Claude plan file")

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

	// Find existing job by source_file (primary) or title (fallback)
	existingJob := findExistingPlanJob(planDir, sourceFile, rawTitle)

	if existingJob != "" {
		// Update existing job, preserving source_file in frontmatter
		if err := updatePlanJob(existingJob, string(planContent), sourceFile); err != nil {
			planLog.WithError(err).WithFields(logrus.Fields{
				"plan_dir": planDir,
				"job_file": existingJob,
			}).Error("Failed to update existing plan job")
			return err
		}
		planLog.WithFields(logrus.Fields{
			"plan_dir":    planDir,
			"job_file":    existingJob,
			"title":       title,
			"source_file": sourceFile,
		}).Info("Updated existing plan job from Edit")
	} else {
		// Create new job with source_file
		if err := savePlanAsJob(planDir, title, string(planContent), sourceFile, preservationConfig); err != nil {
			planLog.WithError(err).WithFields(logrus.Fields{
				"plan_dir": planDir,
				"title":    title,
			}).Error("Failed to save plan as job")
			return err
		}
		planLog.WithFields(logrus.Fields{
			"plan_dir":    planDir,
			"title":       title,
			"source_file": sourceFile,
		}).Info("Created new plan job from Edit")
	}

	// Send notification if enabled
	if preservationConfig.NotifyOnSave {
		sendPlanSavedNotification(ctx, planDir, title)
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

// findClaudePlanFileByContent finds a Claude plan file by matching its content
// Returns the filename (e.g., "sharded-fluttering-pizza.md") or empty string if not found
func findClaudePlanFileByContent(planContent string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		planLog.WithError(err).Debug("Failed to get home directory")
		return ""
	}

	plansDir := filepath.Join(homeDir, ".claude", "plans")
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		planLog.WithError(err).WithField("plans_dir", plansDir).Debug("Failed to read Claude plans directory")
		return ""
	}

	// Normalize content for comparison (trim whitespace)
	normalizedContent := strings.TrimSpace(planContent)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(plansDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		if strings.TrimSpace(string(content)) == normalizedContent {
			planLog.WithField("matched_file", entry.Name()).Debug("Found Claude plan file by content match")
			return entry.Name()
		}
	}

	planLog.Debug("No Claude plan file found matching content")
	return ""
}

// findExistingPlanJob looks for an existing job file with matching source_file or title
// It does THREE passes:
// 1. Exact source_file match (most reliable - uses Claude's plan filename)
// 2. Exact frontmatter title match (for proper grove-flow jobs)
// 3. Body heading match (fallback for files without frontmatter)
func findExistingPlanJob(planDir, sourceFile, rawTitle string) string {
	entries, err := os.ReadDir(planDir)
	if err != nil {
		return ""
	}

	planLog.WithFields(logrus.Fields{
		"plan_dir":    planDir,
		"source_file": sourceFile,
		"raw_title":   rawTitle,
	}).Debug("Searching for existing plan job")

	// FIRST PASS: Look for source_file match (most reliable)
	if sourceFile != "" {
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

			// Check frontmatter source_file field
			if strings.HasPrefix(contentStr, "---") {
				parts := strings.SplitN(contentStr, "---", 3)
				if len(parts) >= 3 {
					frontmatter := parts[1]
					for _, line := range strings.Split(frontmatter, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "source_file:") {
							fmSource := strings.TrimSpace(strings.TrimPrefix(line, "source_file:"))
							fmSource = strings.Trim(fmSource, "\"'")
							if fmSource == sourceFile {
								planLog.WithFields(logrus.Fields{
									"matched_file": filePath,
									"source_file":  sourceFile,
									"pass":         "source_file",
								}).Debug("Found matching job by source_file")
								return filePath
							}
							break
						}
					}
				}
			}
		}
	}

	// Fall back to title matching if no source_file match or no source_file provided
	if rawTitle == "" {
		planLog.Debug("No existing plan job found (no source_file match and no title)")
		return ""
	}

	// Convert title to kebab-case for matching
	kebabTitle := toKebabCase("", rawTitle)
	prefixedTitle := "claude-plan-" + kebabTitle

	// SECOND PASS: Look for frontmatter title matches only (proper grove-flow jobs)
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

		// Check frontmatter title field
		if strings.HasPrefix(contentStr, "---") {
			parts := strings.SplitN(contentStr, "---", 3)
			if len(parts) >= 3 {
				frontmatter := parts[1]
				for _, line := range strings.Split(frontmatter, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "title:") {
						fmTitle := strings.TrimSpace(strings.TrimPrefix(line, "title:"))
						fmTitle = strings.Trim(fmTitle, "\"'")
						kebabFmTitle := toKebabCase("", fmTitle)
						// EXACT match on frontmatter title (with or without prefix)
						if kebabFmTitle == prefixedTitle || kebabFmTitle == kebabTitle {
							planLog.WithFields(logrus.Fields{
								"matched_file": filePath,
								"fm_title":     fmTitle,
								"pass":         "frontmatter_title",
							}).Debug("Found matching job by frontmatter title")
							return filePath
						}
						break
					}
				}
			}
		}
	}

	// THIRD PASS: Look for body heading matches (fallback for files without frontmatter)
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

		// Skip files that have frontmatter (they were already checked in second pass)
		if strings.HasPrefix(contentStr, "---") {
			continue
		}

		// Check the # heading in the body
		lines := strings.Split(contentStr, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "# ") {
				fileTitle := strings.TrimPrefix(line, "# ")
				if toKebabCase("", fileTitle) == kebabTitle {
					planLog.WithFields(logrus.Fields{
						"matched_file": filePath,
						"heading":      fileTitle,
						"pass":         "body_heading",
					}).Debug("Found matching job by body heading")
					return filePath
				}
				break // Only check first heading
			}
		}
	}

	planLog.Debug("No existing plan job found")
	return ""
}

// updatePlanJob updates an existing plan job file with new content, preserving frontmatter
// and ensuring source_file is set
func updatePlanJob(jobFilePath, newContent, sourceFile string) error {
	// Read existing file to preserve frontmatter
	existingContent, err := os.ReadFile(jobFilePath)
	if err != nil {
		// If we can't read, just write the new content
		return os.WriteFile(jobFilePath, []byte(newContent), 0644)
	}

	existingStr := string(existingContent)

	// Extract and preserve frontmatter if present
	if strings.HasPrefix(existingStr, "---") {
		parts := strings.SplitN(existingStr, "---", 3)
		if len(parts) >= 3 {
			frontmatter := parts[1]

			// Ensure source_file is in frontmatter (add if missing)
			if sourceFile != "" && !strings.Contains(frontmatter, "source_file:") {
				// Add source_file before the closing ---
				frontmatter = strings.TrimRight(frontmatter, "\n") + "\nsource_file: " + sourceFile + "\n"
			}

			// Combine preserved frontmatter with new content
			updatedContent := "---" + frontmatter + "---\n\n" + newContent
			return os.WriteFile(jobFilePath, []byte(updatedContent), 0644)
		}
	}

	// No frontmatter found, just write new content
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

// savePlanAsJob saves the plan content as a new grove-flow job using flow plan add
func savePlanAsJob(planDir, title, planContent, sourceFile string, preservationConfig *PlanPreservationConfig) error {
	// Use flow plan add to create the job with proper frontmatter
	args := []string{"plan", "add", planDir,
		"--type", preservationConfig.JobType,
		"--title", title,
	}

	// Add source-file if provided (for tracking job provenance)
	if sourceFile != "" {
		args = append(args, "--source-file", sourceFile)
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
		"plan_dir":    planDir,
		"title":       title,
		"source_file": sourceFile,
		"stdout":      stdout.String(),
	}).Debug("flow plan add command output")

	return nil
}

// sendPlanSavedNotification sends a notification when a plan is saved
func sendPlanSavedNotification(ctx *HookContext, planDir, title string) {
	// Log an event for the plan save
	eventData := map[string]any{
		"plan_dir": planDir,
		"title":    title,
		"action":   "plan_preserved",
	}

	if err := ctx.LogEvent(models.EventType("plan_preserved"), eventData); err != nil {
		log.Printf("Failed to log plan preservation event: %v", err)
	}
}
