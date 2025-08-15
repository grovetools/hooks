package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattsolo1/grove-tend/pkg/assert"
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// LocalStorageScenario tests the local SQLite storage functionality
func LocalStorageScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "local-storage",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),
			harness.NewStep("Create a new session via pretooluse hook", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Create a unique session ID
				sessionID := fmt.Sprintf("test-session-%d", time.Now().Unix())
				ctx.Set("session_id", sessionID)

				// Pretooluse will create the session if it doesn't exist
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"transcript_path": "/tmp/test-transcript.log",
					"hook_event_name": "pretooluse",
					"tool_name": "Bash",
					"tool_input": {
						"command": "echo 'Hello from test'"
					}
				}`, sessionID)

				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "pretooluse should succeed"); err != nil {
					return err
				}

				return assert.Contains(result.Stdout, `"approved":true`, "Tool should be approved")
			}),
			harness.NewStep("Verify session was created using sessions list", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list", "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions list should succeed"); err != nil {
					return err
				}

				// Parse JSON output
				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse sessions JSON: %w", err)
				}

				if err := assert.True(len(sessions) > 0, "Should have at least one session"); err != nil {
					return err
				}

				// Find our test session
				sessionID := ctx.GetString("session_id")
				found := false
				for _, session := range sessions {
					if session["id"] == sessionID {
						found = true
						if err := assert.Equal("running", session["status"], "Session should be running"); err != nil {
							return err
						}
						break
					}
				}

				return assert.True(found, fmt.Sprintf("Session %s should be in the list", sessionID))
			}),
			harness.NewStep("Get specific session details", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")
				cmd := command.New(hooksBinary, "sessions", "get", sessionID)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions get should succeed"); err != nil {
					return err
				}

				// Verify output contains expected fields
				checks := []string{
					fmt.Sprintf("Session ID: %s", sessionID),
					"Status: running",
					"Repository:",
					"Branch:",
					"User:",
					"Working Directory:",
					"Started:",
				}

				for _, check := range checks {
					if err := assert.Contains(result.Stdout, check, fmt.Sprintf("Output should contain '%s'", check)); err != nil {
						return err
					}
				}

				return nil
			}),
			harness.NewStep("Complete a tool execution via posttooluse", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"transcript_path": "/tmp/test-transcript.log",
					"hook_event_name": "posttooluse",
					"tool_name": "Bash",
					"tool_input": {
						"command": "echo 'Hello from test'"
					},
					"tool_response": "Hello from test",
					"tool_duration_ms": 100,
					"tool_error": null
				}`, sessionID)

				cmd := command.New(hooksBinary, "posttooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return assert.Equal(0, result.ExitCode, "posttooluse should succeed")
			}),
			harness.NewStep("Send a notification", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"transcript_path": "/tmp/test-transcript.log",
					"hook_event_name": "notification",
					"type": "info",
					"message": "Test notification from e2e test",
					"level": "info"
				}`, sessionID)

				cmd := command.New(hooksBinary, "notification").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return assert.Equal(0, result.ExitCode, "notification should succeed")
			}),
			harness.NewStep("Stop the session", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"transcript_path": "/tmp/test-transcript.log",
					"hook_event_name": "stop",
					"exit_reason": "completed",
					"duration_ms": 5000
				}`, sessionID)

				cmd := command.New(hooksBinary, "stop").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return assert.Equal(0, result.ExitCode, "stop should succeed")
			}),
			harness.NewStep("Verify session status is completed", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")
				cmd := command.New(hooksBinary, "sessions", "get", sessionID, "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions get should succeed"); err != nil {
					return err
				}

				// Parse JSON output
				var session map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &session); err != nil {
					return fmt.Errorf("failed to parse session JSON: %w", err)
				}

				return assert.Equal("completed", session["status"], "Session should be completed")
			}),
			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// SessionQueriesScenario tests various session query capabilities
func SessionQueriesScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "session-queries",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),
			harness.NewStep("Create multiple test sessions", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Create 3 sessions with different statuses
				sessions := []struct {
					id     string
					status string
				}{
					{fmt.Sprintf("running-%d", time.Now().Unix()), "running"},
					{fmt.Sprintf("idle-%d", time.Now().Unix()), "idle"},
					{fmt.Sprintf("completed-%d", time.Now().Unix()), "completed"},
				}

				ctx.Set("test_sessions", sessions)

				for _, sess := range sessions {
					// Create session
					jsonInput := fmt.Sprintf(`{
						"session_id": "%s",
						"transcript_path": "/tmp/test-%s.log",
						"hook_event_name": "pretooluse",
						"tool_name": "test",
						"tool_input": {}
					}`, sess.id, sess.id)

					cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
					result := cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("failed to create session %s: %s", sess.id, result.Stderr)
					}

					// Update status if needed
					if sess.status != "running" {
						stopInput := fmt.Sprintf(`{
							"session_id": "%s",
							"transcript_path": "/tmp/test-%s.log",
							"hook_event_name": "stop",
							"exit_reason": "%s",
							"duration_ms": 1000
						}`, sess.id, sess.id, sess.status)

						cmd = command.New(hooksBinary, "stop").Stdin(strings.NewReader(stopInput))
						result = cmd.Run()
						if result.ExitCode != 0 {
							return fmt.Errorf("failed to stop session %s: %s", sess.id, result.Stderr)
						}
					}
				}

				return nil
			}),
			harness.NewStep("Query running sessions only", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list", "--status", "running", "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions list should succeed"); err != nil {
					return err
				}

				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse sessions JSON: %w", err)
				}

				// Should only have running sessions
				for _, session := range sessions {
					if err := assert.Equal("running", session["status"], "All sessions should be running"); err != nil {
						return err
					}
				}

				return nil
			}),
			harness.NewStep("Query with limit", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list", "--limit", "2", "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions list should succeed"); err != nil {
					return err
				}

				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse sessions JSON: %w", err)
				}

				return assert.True(len(sessions) <= 2, "Should return at most 2 sessions")
			}),
			harness.NewStep("Test table output format", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions list should succeed"); err != nil {
					return err
				}

				// Check for table headers (check for presence of key header words)
				requiredHeaders := []string{"SESSION ID", "TYPE", "STATUS", "CONTEXT", "USER", "STARTED", "DURATION", "IN STATE"}
				for _, header := range requiredHeaders {
					if err := assert.Contains(result.Stdout, header, fmt.Sprintf("Should contain header '%s'", header)); err != nil {
						return err
					}
				}
				return nil
			}),
			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// SessionBrowseScenario tests the interactive browse command
func SessionBrowseScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "session-browse",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),
			harness.NewStep("Create test sessions for browsing", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Create a few test sessions
				for i := 1; i <= 3; i++ {
					sessionID := fmt.Sprintf("browse-test-%d-%d", i, time.Now().Unix())
					jsonInput := fmt.Sprintf(`{
						"session_id": "%s",
						"transcript_path": "/tmp/browse-test-%d.log",
						"hook_event_name": "pretooluse",
						"tool_name": "Test%d",
						"tool_input": {"test": %d}
					}`, sessionID, i, i, i)

					cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
					result := cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("failed to create test session %d: %s", i, result.Stderr)
					}
				}
				return nil
			}),
			harness.NewStep("Verify browse command exists", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Check that browse command is available
				cmd := command.New(hooksBinary, "sessions", "browse", "--help")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "browse help should succeed"); err != nil {
					return err
				}

				// Check for expected help text
				checks := []string{
					"interactive terminal UI",
					"search, and filter",
					"Aliases:",
					"browse, b",
				}

				for _, check := range checks {
					if err := assert.Contains(result.Stdout, check, fmt.Sprintf("Help should contain '%s'", check)); err != nil {
						return err
					}
				}

				return nil
			}),
			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// OfflineOperationScenario tests that hooks work without network/API access
func OfflineOperationScenario() *harness.Scenario {
	return &harness.Scenario{
		Name: "offline-operation",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),
			harness.NewStep("Ensure no API is running", func(ctx *harness.Context) error {
				// The test is already configured with a non-existent API URL
				// via CANOPY_API_URL=http://test-not-running:8888
				return nil
			}),
			harness.NewStep("Complete full session lifecycle offline", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := fmt.Sprintf("offline-test-%d", time.Now().Unix())

				// 1. Start session
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"transcript_path": "/tmp/offline-test.log",
					"hook_event_name": "pretooluse",
					"tool_name": "Bash",
					"tool_input": {"command": "ls -la"}
				}`, sessionID)

				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				if err := assert.Equal(0, result.ExitCode, "pretooluse should work offline"); err != nil {
					ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
					return err
				}

				// 2. Complete tool
				jsonInput = fmt.Sprintf(`{
					"session_id": "%s",
					"transcript_path": "/tmp/offline-test.log",
					"hook_event_name": "posttooluse",
					"tool_name": "Bash",
					"tool_input": {"command": "ls -la"},
					"tool_response": "file1.txt\nfile2.txt",
					"tool_duration_ms": 50,
					"tool_error": null
				}`, sessionID)

				cmd = command.New(hooksBinary, "posttooluse").Stdin(strings.NewReader(jsonInput))
				result = cmd.Run()
				if err := assert.Equal(0, result.ExitCode, "posttooluse should work offline"); err != nil {
					ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
					return err
				}

				// 3. Stop session
				jsonInput = fmt.Sprintf(`{
					"session_id": "%s",
					"transcript_path": "/tmp/offline-test.log",
					"hook_event_name": "stop",
					"exit_reason": "completed",
					"duration_ms": 10000
				}`, sessionID)

				cmd = command.New(hooksBinary, "stop").Stdin(strings.NewReader(jsonInput))
				result = cmd.Run()
				if err := assert.Equal(0, result.ExitCode, "stop should work offline"); err != nil {
					ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
					return err
				}

				// 4. Verify data was stored locally
				cmd = command.New(hooksBinary, "sessions", "get", sessionID)
				result = cmd.Run()
				if err := assert.Equal(0, result.ExitCode, "should be able to query offline session"); err != nil {
					ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
					return err
				}

				return assert.Contains(result.Stdout, "Status: completed", "Session should be completed")
			}),
			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}