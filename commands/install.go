package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
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
	var global bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install grove-hooks configuration for Claude Code",
		Long: `Install grove-hooks configuration for Claude Code.

Can install locally (default) to .claude/settings.local.json or globally
to ~/.claude/settings.json.

This command merges configuration non-destructively, preserving any other
hooks you may have defined.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(targetDir, global)
		},
	}

	cmd.Flags().StringVarP(&targetDir, "directory", "d", ".", "Target directory for local installation")
	cmd.Flags().BoolVarP(&global, "global", "g", false, "Install hooks globally to ~/.claude/settings.json")

	return cmd
}

func runInstall(targetDir string, global bool) error {
	var settingsPath string

	if global {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		claudeDir := filepath.Join(homeDir, ".claude")
		if err := os.MkdirAll(claudeDir, 0o755); err != nil {
			return fmt.Errorf("failed to create .claude directory: %w", err)
		}
		settingsPath = filepath.Join(claudeDir, "settings.json")
	} else {
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

		settingsPath = filepath.Join(claudeDir, "settings.local.json")
	}

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
			fmt.Printf("Found empty settings file, initializing at %s\n", settingsPath)
		} else {
			if err := json.Unmarshal(data, &settings); err != nil {
				// If parsing fails, offer to backup and create new
				backupPath := settingsPath + ".backup"
				fmt.Printf("Failed to parse existing settings (%v)\n", err)
				fmt.Printf("Backing up to %s and creating new configuration\n", backupPath)

				// Backup the corrupted file
				if err := os.WriteFile(backupPath, data, 0o644); err != nil {
					return fmt.Errorf("failed to backup corrupted settings: %w", err)
				}

				settings = make(ClaudeSettings)
			} else {
				fmt.Printf("Updating existing settings at %s\n", settingsPath)
			}
		}
	} else {
		// File doesn't exist, create new settings
		settings = make(ClaudeSettings)
		fmt.Printf("Creating new settings at %s\n", settingsPath)
	}

	// Define hooks configuration using delegated command format
	newHooks := map[string][]HookEntry{
		"PreToolUse": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{Type: "command", Command: "grove hooks pretooluse"},
				},
			},
		},
		"PostToolUse": {
			{
				Matcher: "(Edit|Write|MultiEdit|Bash|Read)",
				Hooks: []Hook{
					{Type: "command", Command: "grove hooks posttooluse"},
				},
			},
		},
		"Notification": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{Type: "command", Command: "grove hooks notification"},
				},
			},
		},
		"Stop": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{Type: "command", Command: "grove hooks stop"},
				},
			},
		},
		"SubagentStop": {
			{
				Matcher: ".*",
				Hooks: []Hook{
					{Type: "command", Command: "grove hooks subagent-stop"},
				},
			},
		},
	}

	// Merge hooks into settings (preserves user's custom hooks)
	mergeHooks(settings, newHooks)

	// Marshal settings with indentation
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Write settings file
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write settings: %w", err)
	}

	fmt.Println("Grove hooks configuration installed successfully")
	fmt.Printf("Settings file: %s\n", settingsPath)
	fmt.Println()
	fmt.Println("The following hooks have been configured:")
	fmt.Println("  - PreToolUse: Runs before any tool use")
	fmt.Println("  - PostToolUse: Runs after Edit, Write, MultiEdit, Bash, or Read tools")
	fmt.Println("  - Notification: Runs on notifications")
	fmt.Println("  - Stop: Runs when conversation stops")
	fmt.Println("  - SubagentStop: Runs when subagent stops")

	return nil
}

// mergeHooks intelligently merges new hooks into existing settings,
// preserving any user-defined hooks while updating Grove-specific ones.
func mergeHooks(settings ClaudeSettings, newHooks map[string][]HookEntry) {
	// Initialize hooks map if it doesn't exist
	if _, ok := settings["hooks"]; !ok {
		settings["hooks"] = make(map[string]interface{})
	}

	existingHooksMap, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		// If hooks is malformed/not a map, reset it
		existingHooksMap = make(map[string]interface{})
		settings["hooks"] = existingHooksMap
	}

	for eventType, newEntries := range newHooks {
		// Get existing entries for this event type
		var mergedEntries []interface{}

		if existingRaw, exists := existingHooksMap[eventType]; exists {
			if existingList, ok := existingRaw.([]interface{}); ok {
				// Filter out old Grove hooks to avoid duplicates
				for _, item := range existingList {
					if entryMap, ok := item.(map[string]interface{}); ok {
						isGroveHook := false
						if hooksList, ok := entryMap["hooks"].([]interface{}); ok {
							for _, h := range hooksList {
								if hookMap, ok := h.(map[string]interface{}); ok {
									if cmd, ok := hookMap["command"].(string); ok {
										// Identify old or current grove commands
										// Match: grove-hooks, grove hooks, or standalone hooks binary
										if strings.Contains(cmd, "grove-hooks") ||
											strings.Contains(cmd, "grove hooks") ||
											strings.HasPrefix(cmd, "hooks ") {
											isGroveHook = true
											break
										}
									}
								}
							}
						}
						// Keep it if it's NOT a grove hook (preserve user custom hooks)
						if !isGroveHook {
							mergedEntries = append(mergedEntries, item)
						}
					}
				}
			}
		}

		// Append new Grove hooks
		for _, entry := range newEntries {
			// Convert HookEntry to map structure to match existing JSON structure
			hooksList := make([]map[string]string, len(entry.Hooks))
			for i, h := range entry.Hooks {
				hooksList[i] = map[string]string{
					"type":    h.Type,
					"command": h.Command,
				}
			}

			entryMap := map[string]interface{}{
				"matcher": entry.Matcher,
				"hooks":   hooksList,
			}
			mergedEntries = append(mergedEntries, entryMap)
		}

		existingHooksMap[eventType] = mergedEntries
	}
}
