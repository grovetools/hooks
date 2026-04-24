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
	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
)

// writeAsyncHookGroveToml drops a minimal grove.toml into dir with the given
// on_stop hook body. The body must already be the TOML fragment for a single
// [[hooks.on_stop]] entry (including the header line).
func writeAsyncHookGroveToml(dir, body string) error {
	contents := "name = \"async-hook-test\"\n\n" + body
	return fs.WriteString(filepath.Join(dir, "grove.toml"), contents)
}

// stopAsyncStdin builds a StopInput payload scoped to the given working dir
// and session id. Passing cwd lets stop-async locate grove.toml without
// session metadata on disk.
func stopAsyncStdin(sessionID, cwd string) string {
	payload := map[string]any{
		"session_id":      sessionID,
		"exit_reason":     "completed",
		"hook_event_name": "Stop",
		"cwd":             cwd,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func asyncStateDir(sessionID string) string {
	return filepath.Join(paths.StateDir(), "hooks", "sessions", sessionID, "on_stop")
}

// AsyncHookPassesSilentScenario verifies that an on_stop hook exiting 0 causes
// stop-async to exit cleanly and records a "passed" summary line.
func AsyncHookPassesSilentScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "async-hook-passes-silent",
		Description: "on_stop hook that exits 0 → stop-async exits 0 and logs 'passed'",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with passing hook", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				// StateDir uses XDG_STATE_HOME → fall back to $HOME/.local/state.
				// HOME is already sandboxed by SetupTestDatabase; ensure XDG_STATE_HOME
				// is not inherited from the outer environment.
				os.Unsetenv("XDG_STATE_HOME")

				body := `[[hooks.on_stop]]
name = "always-passes"
command = "echo hello && exit 0"
run_if = "always"
`
				return writeAsyncHookGroveToml(ctx.RootDir, body)
			}),
			harness.NewStep("Invoke stop-async and assert clean exit", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := "async-pass-" + fmt.Sprint(time.Now().UnixNano())
				stdin := stopAsyncStdin(sessionID, ctx.RootDir)

				cmd := command.New(bin, "stop-async").Stdin(strings.NewReader(stdin))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "stop-async should exit 0 for a passing hook"); err != nil {
					return err
				}

				summaryPath := filepath.Join(asyncStateDir(sessionID), "always-passes.summary")
				data, err := os.ReadFile(summaryPath)
				if err != nil {
					return fmt.Errorf("expected summary file at %s: %w", summaryPath, err)
				}
				return assert.Contains(string(data), "passed", "summary should record 'passed'")
			}),
			harness.NewStep("Cleanup", func(ctx *harness.Context) error {
				return CleanupTestDatabase(ctx)
			}),
		},
	}
}

// AsyncHookExitsTwoRewakesScenario verifies that a hook exiting 2 surfaces its
// stderr on the stop-async process's stderr and causes exit 2 (which Claude
// Code consumes via asyncRewake to surface to the agent).
func AsyncHookExitsTwoRewakesScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "async-hook-exits-two-rewakes",
		Description: "on_stop hook that exits 2 → stop-async exits 2 with aggregated stderr and 'failed' summary",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with exit-2 hook", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				os.Unsetenv("XDG_STATE_HOME")

				body := `[[hooks.on_stop]]
name = "always-fails"
command = "echo BUILD_FAILED_MARKER 1>&2; exit 2"
run_if = "always"
`
				return writeAsyncHookGroveToml(ctx.RootDir, body)
			}),
			harness.NewStep("Invoke stop-async and assert exit 2 with visible stderr", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := "async-fail-" + fmt.Sprint(time.Now().UnixNano())
				stdin := stopAsyncStdin(sessionID, ctx.RootDir)

				cmd := command.New(bin, "stop-async").Stdin(strings.NewReader(stdin))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(2, result.ExitCode, "stop-async should exit 2 when a hook exits 2"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stderr, "BUILD_FAILED_MARKER", "stderr should surface the hook's stderr"); err != nil {
					return err
				}

				summaryPath := filepath.Join(asyncStateDir(sessionID), "always-fails.summary")
				data, err := os.ReadFile(summaryPath)
				if err != nil {
					return fmt.Errorf("expected summary file at %s: %w", summaryPath, err)
				}
				return assert.Contains(string(data), "failed", "summary should record 'failed'")
			}),
			harness.NewStep("Cleanup", func(ctx *harness.Context) error {
				return CleanupTestDatabase(ctx)
			}),
		},
	}
}

// AsyncHookCancelPreviousScenario verifies that firing two Stop events
// back-to-back with cancel_previous=true results in the first run being
// killed and the second run landing its own PID in the lockfile.
func AsyncHookCancelPreviousScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "async-hook-cancel-previous",
		Description: "cancel_previous=true → the second stop-async kills the first hook's child",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with a slow hook", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				os.Unsetenv("XDG_STATE_HOME")

				// First invocation will run `sleep 30`; cancel_previous=true lets
				// the second invocation SIGTERM it before we assert state.
				body := `[[hooks.on_stop]]
name = "slow-hook"
command = "sleep 30"
run_if = "always"
cancel_previous = true
`
				return writeAsyncHookGroveToml(ctx.RootDir, body)
			}),
			harness.NewStep("Start first stop-async (will block in sleep)", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := "async-cancel-fixed-session"
				ctx.Set("cancel_session", sessionID)
				stdin := stopAsyncStdin(sessionID, ctx.RootDir)
				cmd := command.New(bin, "stop-async").Stdin(strings.NewReader(stdin))
				proc, err := cmd.Start()
				if err != nil {
					return fmt.Errorf("failed to start first stop-async: %w", err)
				}
				ctx.Set("cancel_first_proc", proc)

				// Wait for the first run's child PID to appear in the lockfile.
				pidPath := filepath.Join(asyncStateDir(sessionID), "slow-hook.pid")
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if b, err := os.ReadFile(pidPath); err == nil && len(strings.TrimSpace(string(b))) > 0 {
						ctx.Set("first_pid", strings.TrimSpace(string(b)))
						return nil
					}
					time.Sleep(50 * time.Millisecond)
				}
				return fmt.Errorf("first run never wrote a pid to %s", pidPath)
			}),
			harness.NewStep("Replace the slow hook with a passing one and fire second stop-async", func(ctx *harness.Context) error {
				// Replace the slow hook with one that exits immediately so the
				// second invocation completes deterministically. cancel_previous
				// still applies, so the first sleep must be SIGTERM'd first.
				body := `[[hooks.on_stop]]
name = "slow-hook"
command = "exit 0"
run_if = "always"
cancel_previous = true
`
				if err := writeAsyncHookGroveToml(ctx.RootDir, body); err != nil {
					return err
				}

				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := ctx.GetString("cancel_session")
				stdin := stopAsyncStdin(sessionID, ctx.RootDir)
				cmd := command.New(bin, "stop-async").Stdin(strings.NewReader(stdin))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "second stop-async should exit 0"); err != nil {
					return err
				}

				// Summary should contain at least one entry; the first run's
				// status ("killed" or "failed" after SIGTERM) plus the second
				// run's "passed".
				summaryPath := filepath.Join(asyncStateDir(sessionID), "slow-hook.summary")
				data, err := os.ReadFile(summaryPath)
				if err != nil {
					return fmt.Errorf("expected summary file at %s: %w", summaryPath, err)
				}
				summary := string(data)
				if !strings.Contains(summary, "passed") {
					return fmt.Errorf("summary should contain 'passed' entry for the second run; got:\n%s", summary)
				}
				return nil
			}),
			harness.NewStep("Wait for first stop-async to exit", func(ctx *harness.Context) error {
				procAny := ctx.Get("cancel_first_proc")
				if procAny == nil {
					return fmt.Errorf("first process handle missing")
				}
				proc, ok := procAny.(*command.Process)
				if !ok {
					return fmt.Errorf("unexpected first process type: %T", procAny)
				}
				// The first run's child should have been SIGTERM'd; give the
				// parent a moment to observe the signal and exit.
				result := proc.Wait(10 * time.Second)
				if result.Error != nil && strings.Contains(result.Error.Error(), "timed out") {
					return fmt.Errorf("first stop-async did not exit after cancel_previous: %v", result.Error)
				}
				return nil
			}),
			harness.NewStep("Cleanup", func(ctx *harness.Context) error {
				return CleanupTestDatabase(ctx)
			}),
		},
	}
}
