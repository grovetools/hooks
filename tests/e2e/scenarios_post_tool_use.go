package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/tend/pkg/assert"
	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
)

// writePostToolUseGroveToml writes a grove.toml with one post_tool_use entry
// matching the given `if` rule and emitting the given additional_context.
func writePostToolUseGroveToml(dir, ifRule, additionalContext string) error {
	contents := fmt.Sprintf(`name = "post-tool-use-test"

[[hooks.post_tool_use]]
name = "test-reminder"
if = %q
additional_context = %q
`, ifRule, additionalContext)
	return fs.WriteString(filepath.Join(dir, "grove.toml"), contents)
}

// postToolUseStdin builds a PostToolUse JSON payload pointing the handler at
// cwd so it can locate the test grove.toml.
func postToolUseStdin(sessionID, cwd, toolName string, toolInput map[string]any) string {
	payload := map[string]any{
		"session_id":      sessionID,
		"hook_event_name": "PostToolUse",
		"tool_name":       toolName,
		"tool_input":      toolInput,
		"tool_response":   map[string]any{},
		"cwd":             cwd,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// PostToolUseReminderFiresScenario verifies that a matching post_tool_use
// entry causes posttooluse to emit the additionalContext JSON to stdout.
func PostToolUseReminderFiresScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "post-tool-use-reminder-fires",
		Description: "matching post_tool_use entry → posttooluse emits additionalContext JSON",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with echo-matching reminder", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				os.Unsetenv("XDG_STATE_HOME")
				return writePostToolUseGroveToml(ctx.RootDir, "Bash(echo *)", "remember to echo carefully")
			}),
			harness.NewStep("Invoke posttooluse with matching Bash echo and assert JSON", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := "ptu-fires-" + fmt.Sprint(time.Now().UnixNano())
				stdin := postToolUseStdin(sessionID, ctx.RootDir, "Bash", map[string]any{"command": "echo hello"})

				cmd := command.New(bin, "posttooluse").Stdin(strings.NewReader(stdin))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "posttooluse should exit 0"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, `"hookEventName":"PostToolUse"`, "stdout should contain hook event"); err != nil {
					return err
				}
				return assert.Contains(result.Stdout, "remember to echo carefully", "stdout should contain additionalContext")
			}),
			harness.NewStep("Cleanup", func(ctx *harness.Context) error {
				return CleanupTestDatabase(ctx)
			}),
		},
	}
}

// PostToolUseReminderSkipsOnMissScenario verifies that a non-matching tool
// call produces no stdout reminder JSON.
func PostToolUseReminderSkipsOnMissScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "post-tool-use-reminder-skips-on-miss",
		Description: "non-matching tool call → posttooluse emits no reminder JSON",
		Steps: []harness.Step{
			harness.NewStep("Setup sandbox and grove.toml with echo-only reminder", func(ctx *harness.Context) error {
				if err := SetupTestDatabase(ctx); err != nil {
					return err
				}
				os.Unsetenv("XDG_STATE_HOME")
				return writePostToolUseGroveToml(ctx.RootDir, "Bash(echo *)", "remember to echo carefully")
			}),
			harness.NewStep("Invoke posttooluse with non-matching Bash ls and assert no reminder", func(ctx *harness.Context) error {
				bin, err := FindProjectBinary()
				if err != nil {
					return err
				}
				sessionID := "ptu-miss-" + fmt.Sprint(time.Now().UnixNano())
				stdin := postToolUseStdin(sessionID, ctx.RootDir, "Bash", map[string]any{"command": "ls"})

				cmd := command.New(bin, "posttooluse").Stdin(strings.NewReader(stdin))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "posttooluse should exit 0"); err != nil {
					return err
				}
				if strings.Contains(result.Stdout, "hookEventName") {
					return fmt.Errorf("expected no reminder JSON on stdout, got: %s", result.Stdout)
				}
				return nil
			}),
			harness.NewStep("Cleanup", func(ctx *harness.Context) error {
				return CleanupTestDatabase(ctx)
			}),
		},
	}
}
