package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/tend/pkg/assert"
	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/harness"
)

// InstallCommandScenario tests the grove-hooks install command
func InstallCommandScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "hooks-install-command",
		Steps: []harness.Step{
			harness.NewStep("Install hooks in fresh directory", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Create a fresh directory for testing
				testDir := ctx.NewDir("install-test-fresh")

				// Ensure the directory exists
				if err := os.MkdirAll(testDir, 0755); err != nil {
					return fmt.Errorf("failed to create test directory: %w", err)
				}

				// Run install command
				cmd := command.New(hooksBinary, "install", "-d", testDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "install command should exit successfully"); err != nil {
					return err
				}

				// Verify output messages (user-facing output goes to stdout)
				if err := assert.Contains(result.Stdout, "Creating new settings", "Should indicate creating new settings"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "Grove hooks configuration installed successfully", "Should show success message"); err != nil {
					return err
				}

				// Verify .claude directory was created
				claudeDir := filepath.Join(testDir, ".claude")
				if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
					return fmt.Errorf(".claude directory should exist")
				}

				// Verify settings.local.json was created
				settingsPath := filepath.Join(claudeDir, "settings.local.json")
				if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
					return fmt.Errorf("settings.local.json should exist")
				}

				// Read and verify the settings content
				data, err := os.ReadFile(settingsPath)
				if err != nil {
					return fmt.Errorf("failed to read settings file: %w", err)
				}

				var settings map[string]interface{}
				if err := json.Unmarshal(data, &settings); err != nil {
					return fmt.Errorf("failed to parse settings JSON: %w", err)
				}

				// Verify hooks section exists
				hooks, ok := settings["hooks"].(map[string]interface{})
				if !ok {
					return fmt.Errorf("settings should contain 'hooks' section")
				}

				// Verify all expected hooks are present
				expectedHooks := []string{"PreToolUse", "PostToolUse", "Notification", "Stop", "SubagentStop"}
				for _, hookName := range expectedHooks {
					if _, ok := hooks[hookName]; !ok {
						return fmt.Errorf("missing expected hook: %s", hookName)
					}
				}

				ctx.Set("test_dir", testDir)
				return nil
			}),
			harness.NewStep("Update existing settings", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Create a directory with existing settings
				testDir := ctx.NewDir("install-test-existing")
				claudeDir := filepath.Join(testDir, ".claude")
				if err := os.MkdirAll(claudeDir, 0755); err != nil {
					return fmt.Errorf("failed to create .claude directory: %w", err)
				}

				// Create existing settings with custom content
				existingSettings := map[string]interface{}{
					"enabledMcpjsonServers": []string{"custom-server"},
					"customSetting":         "should-be-preserved",
					"hooks": map[string]interface{}{
						"CustomHook": []interface{}{
							map[string]interface{}{
								"matcher": ".*",
								"hooks": []interface{}{
									map[string]interface{}{
										"type":    "command",
										"command": "custom-command",
									},
								},
							},
						},
					},
				}

				data, err := json.MarshalIndent(existingSettings, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal existing settings: %w", err)
				}

				settingsPath := filepath.Join(claudeDir, "settings.local.json")
				if err := os.WriteFile(settingsPath, data, 0644); err != nil {
					return fmt.Errorf("failed to write existing settings: %w", err)
				}

				// Run install command
				cmd := command.New(hooksBinary, "install", "-d", testDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "install command should exit successfully"); err != nil {
					return err
				}

				// Verify output indicates updating (user-facing output goes to stdout)
				if err := assert.Contains(result.Stdout, "Updating existing settings", "Should indicate updating existing settings"); err != nil {
					return err
				}

				// Read updated settings
				data, err = os.ReadFile(settingsPath)
				if err != nil {
					return fmt.Errorf("failed to read updated settings: %w", err)
				}

				var updatedSettings map[string]interface{}
				if err := json.Unmarshal(data, &updatedSettings); err != nil {
					return fmt.Errorf("failed to parse updated settings: %w", err)
				}

				// Verify custom settings were preserved
				if mcp, ok := updatedSettings["enabledMcpjsonServers"].([]interface{}); ok {
					if len(mcp) > 0 && mcp[0] == "custom-server" {
						// Good, custom MCP server was preserved
					} else {
						return fmt.Errorf("enabledMcpjsonServers should be preserved")
					}
				}

				if custom, ok := updatedSettings["customSetting"].(string); !ok || custom != "should-be-preserved" {
					return fmt.Errorf("customSetting should be preserved")
				}

				// Verify grove-hooks were added
				hooks, ok := updatedSettings["hooks"].(map[string]interface{})
				if !ok {
					return fmt.Errorf("hooks section should exist")
				}

				// Verify all grove hooks are present
				expectedHooks := []string{"PreToolUse", "PostToolUse", "Notification", "Stop", "SubagentStop"}
				for _, hookName := range expectedHooks {
					if _, ok := hooks[hookName]; !ok {
						return fmt.Errorf("missing expected hook after update: %s", hookName)
					}
				}

				// Note: CustomHook will be overwritten, which is expected behavior
				// as grove-hooks takes ownership of the hooks section

				return nil
			}),
			harness.NewStep("Install in non-existent directory", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Try to install in a non-existent directory
				nonExistentDir := filepath.Join(ctx.NewDir("temp"), "non-existent-dir")

				cmd := command.New(hooksBinary, "install", "-d", nonExistentDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				// Should fail with non-zero exit code
				if err := assert.NotEqual(0, result.ExitCode, "install should fail for non-existent directory"); err != nil {
					return err
				}

				// Should have error message
				if err := assert.Contains(result.Stderr, "target directory does not exist", "Should show directory not found error"); err != nil {
					return err
				}

				return nil
			}),
			harness.NewStep("Verify hook commands in installed settings", func(ctx *harness.Context) error {
				testDir := ctx.GetString("test_dir")
				settingsPath := filepath.Join(testDir, ".claude", "settings.local.json")

				data, err := os.ReadFile(settingsPath)
				if err != nil {
					return fmt.Errorf("failed to read settings: %w", err)
				}

				// Verify specific hook configurations
				var settings map[string]interface{}
				if err := json.Unmarshal(data, &settings); err != nil {
					return fmt.Errorf("failed to parse settings: %w", err)
				}

				hooks := settings["hooks"].(map[string]interface{})

				// Check PreToolUse configuration
				preToolUse := hooks["PreToolUse"].([]interface{})
				if len(preToolUse) != 1 {
					return fmt.Errorf("PreToolUse should have exactly one entry")
				}
				entry := preToolUse[0].(map[string]interface{})
				if entry["matcher"] != ".*" {
					return fmt.Errorf("PreToolUse matcher should be '.*'")
				}
				hooksList := entry["hooks"].([]interface{})
				if len(hooksList) != 1 {
					return fmt.Errorf("PreToolUse should have exactly one hook")
				}
				hook := hooksList[0].(map[string]interface{})
				if hook["command"] != "grove-hooks pretooluse" {
					return fmt.Errorf("PreToolUse command should be 'grove-hooks pretooluse'")
				}

				// Check PostToolUse configuration
				postToolUse := hooks["PostToolUse"].([]interface{})
				if len(postToolUse) != 1 {
					return fmt.Errorf("PostToolUse should have exactly one entry")
				}
				entry = postToolUse[0].(map[string]interface{})
				if entry["matcher"] != "(Edit|Write|MultiEdit|Bash|Read)" {
					return fmt.Errorf("PostToolUse matcher should match specific tools")
				}

				return nil
			}),
		},
	}
}
