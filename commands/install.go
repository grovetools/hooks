package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	grovelogging "github.com/mattsolo1/grove-core/logging"
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
	ctx := context.Background()
	ulog := grovelogging.NewUnifiedLogger("grove-hooks.install")

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
			ulog.Info("Found empty settings file, creating new configuration").
				Field("settings_path", settingsPath).
				Pretty(fmt.Sprintf("Found empty settings file, creating new configuration at %s", settingsPath)).
				Log(ctx)
		} else {
			if err := json.Unmarshal(data, &settings); err != nil {
				// If parsing fails, offer to backup and create new
				backupPath := settingsPath + ".backup"
				ulog.Warn("Failed to parse existing settings").
					Field("settings_path", settingsPath).
					Err(err).
					Pretty(fmt.Sprintf("Failed to parse existing settings (%v)", err)).
					Log(ctx)
				ulog.Info("Backing up and creating new configuration").
					Field("backup_path", backupPath).
					Pretty(fmt.Sprintf("Backing up to %s and creating new configuration", backupPath)).
					Log(ctx)

				// Backup the corrupted file
				if err := os.WriteFile(backupPath, data, 0o644); err != nil {
					return fmt.Errorf("failed to backup corrupted settings: %w", err)
				}

				settings = make(ClaudeSettings)
			} else {
				ulog.Info("Updating existing settings").
					Field("settings_path", settingsPath).
					Pretty(fmt.Sprintf("Updating existing settings at %s", settingsPath)).
					Log(ctx)
			}
		}
	} else {
		// File doesn't exist, create new settings
		settings = make(ClaudeSettings)
		ulog.Info("Creating new settings").
			Field("settings_path", settingsPath).
			Pretty(fmt.Sprintf("Creating new settings at %s", settingsPath)).
			Log(ctx)
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

	ulog.Success("Grove hooks configuration installed successfully").
		Field("settings_file", settingsPath).
		Pretty("Grove hooks configuration installed successfully").
		Log(ctx)
	ulog.Success("Settings file location").
		Field("path", settingsPath).
		Pretty(fmt.Sprintf("Settings file: %s", settingsPath)).
		Log(ctx)
	ulog.Info("").PrettyOnly().Pretty("").Log(ctx)
	ulog.Info("Hooks configured").
		Pretty("The following hooks have been configured:\n  - PreToolUse: Runs before any tool use\n  - PostToolUse: Runs after Edit, Write, MultiEdit, Bash, or Read tools\n  - Notification: Runs on notifications\n  - Stop: Runs when conversation stops\n  - SubagentStop: Runs when subagent stops").
		Log(ctx)

	return nil
}
