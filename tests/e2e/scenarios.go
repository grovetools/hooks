package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-tend/pkg/assert"
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// HooksDirectExecutionScenario tests executing hooks via direct commands
func HooksDirectExecutionScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "hooks-direct-execution",
		Steps: []harness.Step{
			harness.NewStep("Run 'hooks pretooluse' command", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				if hooksBinary == "" {
					return fmt.Errorf("HOOKS_BINARY environment variable not set")
				}
				
				// Pretooluse expects JSON input
				jsonInput := `{"session_id":"test-session","tool_name":"test","tool_input":{}}`
				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks pretooluse should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, `"approved":true`, "Should approve the tool use")
			}),
			harness.NewStep("Run 'hooks posttooluse' command", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				// Posttooluse expects JSON input
				jsonInput := `{"session_id":"test-session","tool_name":"test","tool_output":"test output"}`
				cmd := command.New(hooksBinary, "posttooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks posttooluse should exit successfully"); err != nil {
					return err
				}
				
				// Posttooluse doesn't output anything on success
				return nil
			}),
			harness.NewStep("Run 'hooks notification' command", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				// Notification hook expects JSON input  
				jsonInput := `{"message":"Test notification","level":"info"}`
				cmd := command.New(hooksBinary, "notification").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks notification should exit successfully"); err != nil {
					return err
				}
				
				// Notification hook doesn't output anything on success
				return nil
			}),
			harness.NewStep("Run 'hooks stop' command", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				// Stop hook expects JSON input
				jsonInput := `{"session_id":"test-session","exit_reason":"completed"}`
				cmd := command.New(hooksBinary, "stop").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks stop should exit successfully"); err != nil {
					return err
				}
				
				// Stop hook doesn't output anything on success
				return nil
			}),
		},
	}
}

// HooksSymlinkExecutionScenario tests executing hooks via symlinks (as Claude would)
func HooksSymlinkExecutionScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "hooks-symlink-execution",
		Steps: []harness.Step{
			harness.NewStep("Create symlinks for hooks", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				if hooksBinary == "" {
					return fmt.Errorf("HOOKS_BINARY environment variable not set")
				}
				
				// Create symlinks in a temporary directory
				symlinkDir := ctx.NewDir("symlinks")
				ctx.Set("symlink_dir", symlinkDir)
				
				// Ensure the directory exists
				if err := os.MkdirAll(symlinkDir, 0755); err != nil {
					return fmt.Errorf("failed to create symlink directory: %w", err)
				}
				
				// Create symlinks for each hook
				hooks := []string{"pretooluse", "posttooluse", "notification", "stop"}
				for _, hook := range hooks {
					symlinkPath := filepath.Join(symlinkDir, hook)
					if err := os.Symlink(hooksBinary, symlinkPath); err != nil {
						return fmt.Errorf("failed to create symlink for %s: %w", hook, err)
					}
				}
				
				return nil
			}),
			harness.NewStep("Execute pretooluse via symlink", func(ctx *harness.Context) error {
				symlinkDir := ctx.GetString("symlink_dir")
				pretooluse := filepath.Join(symlinkDir, "pretooluse")
				
				jsonInput := `{"session_id":"test-session","tool_name":"test","tool_input":{}}`
				cmd := command.New(pretooluse).Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "pretooluse symlink should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, `"approved":true`, "Should approve tool use via symlink")
			}),
			harness.NewStep("Execute posttooluse via symlink", func(ctx *harness.Context) error {
				symlinkDir := ctx.GetString("symlink_dir")
				posttooluse := filepath.Join(symlinkDir, "posttooluse")
				
				jsonInput := `{"session_id":"test-session","tool_name":"test","tool_output":"test output"}`
				cmd := command.New(posttooluse).Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "posttooluse symlink should exit successfully"); err != nil {
					return err
				}
				
				return nil
			}),
			harness.NewStep("Execute notification via symlink", func(ctx *harness.Context) error {
				symlinkDir := ctx.GetString("symlink_dir")
				notification := filepath.Join(symlinkDir, "notification")
				
				jsonInput := `{"message":"Test notification","level":"info"}`
				cmd := command.New(notification).Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "notification symlink should exit successfully"); err != nil {
					return err
				}
				
				return nil
			}),
			harness.NewStep("Execute stop via symlink", func(ctx *harness.Context) error {
				symlinkDir := ctx.GetString("symlink_dir")
				stop := filepath.Join(symlinkDir, "stop")
				
				jsonInput := `{"session_id":"test-session","exit_reason":"completed"}`
				cmd := command.New(stop).Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "stop symlink should exit successfully"); err != nil {
					return err
				}
				
				return nil
			}),
		},
	}
}