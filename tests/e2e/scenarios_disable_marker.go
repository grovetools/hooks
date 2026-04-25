package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/tend/pkg/assert"
	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/harness"
)

// markerPath mirrors hooks/internal/hooks.HookMarkerPath, kept inline so the
// e2e binary doesn't depend on internal packages.
func markerPath(workingDir, hookName string) string {
	repo := strings.ToLower(filepath.Base(workingDir))
	return filepath.Join(paths.StateDir(), "hooks", "disabled", repo, strings.ToLower(hookName))
}

// HookDisableMarkerSkipsHookScenario verifies that a marker file created via
// `grove hooks disable` causes stop-async to skip the hook (exit 0 even
// though the hook would otherwise fail with exit 2).
func HookDisableMarkerSkipsHookScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "hooks-disable-marker-skips-hook",
		Description: "marker file from `grove hooks disable` makes stop-async skip a would-be-failing hook",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with failing hook", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				os.Unsetenv("XDG_STATE_HOME")
				body := `[[hooks.on_stop]]
name = "would-fail"
command = "echo SHOULD_NOT_RUN 1>&2; exit 2"
run_if = "always"
`
				return writeAsyncHookGroveToml(ctx.RootDir, body)
			}),
			harness.NewStep("Disable the hook via grove hooks disable", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				cmd := command.New(bin, "disable", "would-fail", "--repo", ctx.RootDir, "--reason", "test-skip")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				if err := assert.Equal(0, result.ExitCode, "disable should succeed"); err != nil {
					return err
				}
				if _, err := os.Stat(markerPath(ctx.RootDir, "would-fail")); err != nil {
					return fmt.Errorf("expected marker file: %w", err)
				}
				return nil
			}),
			harness.NewStep("Invoke stop-async and assert clean skip", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := "disable-marker-" + fmt.Sprint(time.Now().UnixNano())
				ctx.Set("session_id", sessionID)
				stdin := stopAsyncStdin(sessionID, ctx.RootDir)
				cmd := command.New(bin, "stop-async").Stdin(strings.NewReader(stdin))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				if err := assert.Equal(0, result.ExitCode, "disabled hook must not fail stop-async"); err != nil {
					return err
				}
				if strings.Contains(result.Stderr, "SHOULD_NOT_RUN") {
					return fmt.Errorf("hook command ran despite marker; stderr=%q", result.Stderr)
				}
				summaryPath := filepath.Join(asyncStateDir(sessionID), "would-fail.summary")
				data, err := os.ReadFile(summaryPath)
				if err != nil {
					return fmt.Errorf("expected summary file at %s: %w", summaryPath, err)
				}
				return assert.Contains(string(data), "skipped", "summary should record 'skipped'")
			}),
			harness.NewStep("Cleanup", CleanupTestDatabase),
		},
	}
}

// HookEnableRemovesMarkerScenario verifies that `grove hooks enable` removes
// the marker file and the hook runs again.
func HookEnableRemovesMarkerScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "hooks-enable-removes-marker",
		Description: "`grove hooks enable` removes the marker file and the hook runs on the next stop-async",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with passing hook", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				os.Unsetenv("XDG_STATE_HOME")
				body := `[[hooks.on_stop]]
name = "runs-when-enabled"
command = "echo RAN_HOOK"
run_if = "always"
`
				return writeAsyncHookGroveToml(ctx.RootDir, body)
			}),
			harness.NewStep("Disable then enable the hook", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				disableCmd := command.New(bin, "disable", "runs-when-enabled", "--repo", ctx.RootDir)
				dr := disableCmd.Run()
				ctx.ShowCommandOutput(disableCmd.String(), dr.Stdout, dr.Stderr)
				if err := assert.Equal(0, dr.ExitCode, "disable should succeed"); err != nil {
					return err
				}
				if _, err := os.Stat(markerPath(ctx.RootDir, "runs-when-enabled")); err != nil {
					return fmt.Errorf("marker should exist after disable: %w", err)
				}
				enableCmd := command.New(bin, "enable", "runs-when-enabled", "--repo", ctx.RootDir)
				er := enableCmd.Run()
				ctx.ShowCommandOutput(enableCmd.String(), er.Stdout, er.Stderr)
				if err := assert.Equal(0, er.ExitCode, "enable should succeed"); err != nil {
					return err
				}
				if _, err := os.Stat(markerPath(ctx.RootDir, "runs-when-enabled")); !os.IsNotExist(err) {
					return fmt.Errorf("marker should be gone after enable; stat err=%v", err)
				}
				return nil
			}),
			harness.NewStep("Stop-async should run the hook (passed)", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := "enable-marker-" + fmt.Sprint(time.Now().UnixNano())
				stdin := stopAsyncStdin(sessionID, ctx.RootDir)
				cmd := command.New(bin, "stop-async").Stdin(strings.NewReader(stdin))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				if err := assert.Equal(0, result.ExitCode, "stop-async should exit 0"); err != nil {
					return err
				}
				summaryPath := filepath.Join(asyncStateDir(sessionID), "runs-when-enabled.summary")
				data, err := os.ReadFile(summaryPath)
				if err != nil {
					return fmt.Errorf("expected summary file at %s: %w", summaryPath, err)
				}
				return assert.Contains(string(data), "passed", "summary should record 'passed' after enable")
			}),
			harness.NewStep("Cleanup", CleanupTestDatabase),
		},
	}
}

// HookListShowsStateScenario verifies `grove hooks list --json` reports the
// disabled/enabled state for each hook.
func HookListShowsStateScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "hooks-list-shows-state",
		Description: "`grove hooks list --json` reports disabled/enabled state for each on_stop entry",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with two hooks", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				os.Unsetenv("XDG_STATE_HOME")
				body := `[[hooks.on_stop]]
name = "alpha"
command = "echo alpha"
run_if = "always"

[[hooks.on_stop]]
name = "beta"
command = "echo beta"
run_if = "always"
`
				return writeAsyncHookGroveToml(ctx.RootDir, body)
			}),
			harness.NewStep("Disable alpha, leave beta enabled", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				cmd := command.New(bin, "disable", "alpha", "--repo", ctx.RootDir, "--reason", "ci-flake")
				r := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), r.Stdout, r.Stderr)
				return assert.Equal(0, r.ExitCode, "disable should succeed")
			}),
			harness.NewStep("List as JSON and assert state", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				cmd := command.New(bin, "list", "--repo", ctx.RootDir, "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				if err := assert.Equal(0, result.ExitCode, "list should succeed"); err != nil {
					return err
				}
				var payload struct {
					Repo  string `json:"repo"`
					Hooks []struct {
						Name          string `json:"name"`
						Disabled      bool   `json:"disabled"`
						DisableReason string `json:"disable_reason"`
					} `json:"hooks"`
				}
				if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
					return fmt.Errorf("parse list JSON: %w", err)
				}
				if len(payload.Hooks) != 2 {
					return fmt.Errorf("expected 2 hooks, got %d", len(payload.Hooks))
				}
				byName := map[string]struct {
					disabled bool
					reason   string
				}{}
				for _, h := range payload.Hooks {
					byName[h.Name] = struct {
						disabled bool
						reason   string
					}{h.Disabled, h.DisableReason}
				}
				if !byName["alpha"].disabled {
					return fmt.Errorf("alpha should be disabled; got %+v", byName["alpha"])
				}
				if byName["alpha"].reason != "ci-flake" {
					return fmt.Errorf("alpha reason mismatch: got %q want %q", byName["alpha"].reason, "ci-flake")
				}
				if byName["beta"].disabled {
					return fmt.Errorf("beta should be enabled; got %+v", byName["beta"])
				}
				return nil
			}),
			harness.NewStep("Cleanup", CleanupTestDatabase),
		},
	}
}
