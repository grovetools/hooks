package main

import (
	"fmt"
	"os"
	"path/filepath"

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
				
				cmd := command.New(hooksBinary, "pretooluse")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks pretooluse should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running pre-tool-use hook", "Should output pre-tool-use message")
			}),
			harness.NewStep("Run 'hooks posttooluse' command", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				cmd := command.New(hooksBinary, "posttooluse")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks posttooluse should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running post-tool-use hook", "Should output post-tool-use message")
			}),
			harness.NewStep("Run 'hooks init' command", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				cmd := command.New(hooksBinary, "init")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks init should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running initialization hook", "Should output init message")
			}),
			harness.NewStep("Run 'hooks complete' command", func(ctx *harness.Context) error {
				hooksBinary := os.Getenv("HOOKS_BINARY")
				cmd := command.New(hooksBinary, "complete")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "hooks complete should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running completion hook", "Should output completion message")
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
				hooks := []string{"pretooluse", "posttooluse", "init", "complete"}
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
				
				cmd := command.New(pretooluse)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "pretooluse symlink should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running pre-tool-use hook", "Should run pre-tool-use hook via symlink")
			}),
			harness.NewStep("Execute posttooluse via symlink", func(ctx *harness.Context) error {
				symlinkDir := ctx.GetString("symlink_dir")
				posttooluse := filepath.Join(symlinkDir, "posttooluse")
				
				cmd := command.New(posttooluse)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "posttooluse symlink should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running post-tool-use hook", "Should run post-tool-use hook via symlink")
			}),
			harness.NewStep("Execute init via symlink", func(ctx *harness.Context) error {
				symlinkDir := ctx.GetString("symlink_dir")
				initHook := filepath.Join(symlinkDir, "init")
				
				cmd := command.New(initHook)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "init symlink should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running initialization hook", "Should run init hook via symlink")
			}),
			harness.NewStep("Execute complete via symlink", func(ctx *harness.Context) error {
				symlinkDir := ctx.GetString("symlink_dir")
				complete := filepath.Join(symlinkDir, "complete")
				
				cmd := command.New(complete)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				
				if err := assert.Equal(0, result.ExitCode, "complete symlink should exit successfully"); err != nil {
					return err
				}
				
				return assert.Contains(result.Stdout, "Running completion hook", "Should run completion hook via symlink")
			}),
		},
	}
}