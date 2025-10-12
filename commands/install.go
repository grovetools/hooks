package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/mattsolo1/grove-core/logging"
)

type ClaudeSettings map[string]interface{}

type HookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func NewInstallCmd() *cobra.Command {
	var targetDir string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install grove-hooks configuration in a repository",
		Long: `Install grove-hooks configuration in a repository by creating or updating .claude/settings.local.json

This command will:
- Create .claude directory if it doesn't exist
- Create or update settings.local.json with grove-hooks configuration
- Preserve existing settings when updating`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(targetDir)
		},
	}

	cmd.Flags().StringVarP(&targetDir, "directory", "d", ".", "Target directory for installation")

	return cmd
}

func runInstall(targetDir string) error {
	// Initialize loggers
	log := logging.NewLogger("grove-hooks")
	prettyLog := logging.NewPrettyLogger()
	
	// Resolve target directory
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("failed to resolve directory: %w", err)
	}

	// Check if target directory exists
	if _, err := os.Stat(absDir); os.IsNotExist(err) {
		return fmt.Errorf("target directory does not exist: %s", absDir)
	}

	// Create .claude directory if it doesn't exist
	claudeDir := filepath.Join(absDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}

	// Path to settings file
	settingsPath := filepath.Join(claudeDir, "settings.local.json")

	// Check if settings file already exists
	var settings ClaudeSettings
	if _, err := os.Stat(settingsPath); err == nil {
		// File exists, read and parse it
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			return fmt.Errorf("failed to read existing settings: %w", err)
		}

		// Handle empty or invalid JSON files
		if len(data) == 0 || string(data) == "{}" {
			settings = make(ClaudeSettings)
			log.Infof("Found empty settings file, creating new configuration at %s", settingsPath)
			prettyLog.InfoPretty(fmt.Sprintf("Found empty settings file, creating new configuration at %s", settingsPath))
		} else {
			if err := json.Unmarshal(data, &settings); err != nil {
				// If parsing fails, offer to backup and create new
				backupPath := settingsPath + ".backup"
				log.Warnf("Failed to parse existing settings (%v)", err)
				prettyLog.WarnPretty(fmt.Sprintf("Failed to parse existing settings (%v)", err))
				log.Infof("Backing up to %s and creating new configuration", backupPath)
				prettyLog.InfoPretty(fmt.Sprintf("Backing up to %s and creating new configuration", backupPath))

				// Backup the corrupted file
				if err := os.WriteFile(backupPath, data, 0o644); err != nil {
					return fmt.Errorf("failed to backup corrupted settings: %w", err)
				}

				settings = make(ClaudeSettings)
			} else {
				log.Infof("Updating existing settings at %s", settingsPath)
				prettyLog.InfoPretty(fmt.Sprintf("Updating existing settings at %s", settingsPath))
			}
		}
	} else {
		// File doesn't exist, create new settings
		settings = make(ClaudeSettings)
		log.Infof("Creating new settings at %s", settingsPath)
		prettyLog.InfoPretty(fmt.Sprintf("Creating new settings at %s", settingsPath))
	}

	// Define default hooks configuration
	defaultHooks := map[string][]HookEntry{
		"PreToolUse": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{
						Type:    "command",
						Command: "grove-hooks pretooluse",
					},
				},
			},
		},
		"PostToolUse": {
			{
				Matcher: "(Edit|Write|MultiEdit|Bash|Read)",
				Hooks: []Hook{
					{
						Type:    "command",
						Command: "grove-hooks posttooluse",
					},
				},
			},
		},
		"Notification": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{
						Type:    "command",
						Command: "grove-hooks notification",
					},
				},
			},
		},
		"Stop": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{
						Type:    "command",
						Command: "grove-hooks stop",
					},
				},
			},
		},
		"SubagentStop": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{
						Type:    "command",
						Command: "grove-hooks subagentstop",
					},
				},
			},
		},
	}

	// Update hooks (this will overwrite existing grove-hooks configurations)
	settings["hooks"] = defaultHooks

	// Marshal settings with indentation
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Write settings file
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write settings: %w", err)
	}

	log.Info("Grove hooks configuration installed successfully")
	prettyLog.Success("Grove hooks configuration installed successfully!")
	log.Infof("Settings file: %s", settingsPath)
	prettyLog.Success(fmt.Sprintf("Settings file: %s", settingsPath))
	prettyLog.Blank()
	prettyLog.InfoPretty("The following hooks have been configured:")
	prettyLog.InfoPretty("  - PreToolUse: Runs before any tool use")
	prettyLog.InfoPretty("  - PostToolUse: Runs after Edit, Write, MultiEdit, Bash, or Read tools")
	prettyLog.InfoPretty("  - Notification: Runs on notifications")
	prettyLog.InfoPretty("  - Stop: Runs when conversation stops")
	prettyLog.InfoPretty("  - SubagentStop: Runs when subagent stops")

	return nil
}
