package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/tend/pkg/assert"
	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/harness"
)

// PIDBasedSessionTracking tests the PID-based session directory creation
// This tests filesystem-based session state management
func PIDBasedSessionTracking() *harness.Scenario {
	return &harness.Scenario{
		Name:        "pid-based-session-tracking",
		Description: "Tests PID-based session directory creation and tracking",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),

			harness.NewStep("Create session directories", func(ctx *harness.Context) error {
				// With HOME set, ~/.local/state/grove/hooks/sessions becomes HOME/hooks/sessions
				sandboxedHome := ctx.GetString("sandboxed_home")
				groveSessionsDir := filepath.Join(sandboxedHome, ".local", "state", "grove", "hooks", "sessions")
				testSessionDir := filepath.Join(groveSessionsDir, "test-session-phase4")

				// Store paths for cleanup
				ctx.Set("grove_sessions_dir", groveSessionsDir)
				ctx.Set("test_session_dir", testSessionDir)

				// Clean up any existing test session
				os.RemoveAll(testSessionDir)

				// Create a marker file in the test root directory to show test info
				markerFile := filepath.Join(ctx.RootDir, "session-tracking-info.txt")
				info := fmt.Sprintf(`PID-based Session Tracking Test

This test uses HOME to redirect all artifacts to the temp directory.

Session directory structure (with HOME=%s):
  ~/.local/state/grove/hooks/sessions/ becomes:
  %s/.local/state/grove/hooks/sessions/
    └── <session-id>/
        ├── pid.lock       (contains the process PID)
        └── metadata.json  (contains session metadata)

Test session ID: test-session-phase4
Test session path: %s

All artifacts are in this temp directory for easy inspection.
`, sandboxedHome, sandboxedHome, testSessionDir)
				os.WriteFile(markerFile, []byte(info), 0644)

				return nil
			}),

			harness.NewStep("Trigger session creation via pretooluse hook", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := "test-session-phase4"
				ctx.Set("session_id", sessionID)

				// Pretooluse should trigger EnsureSessionExists
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"tool_name": "bash",
					"tool_input": {"command": "echo test"}
				}`, sessionID)

				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return assert.Equal(0, result.ExitCode, "pretooluse should succeed")
			}),

			harness.NewStep("Verify session directory was created", func(ctx *harness.Context) error {
				testSessionDir := ctx.GetString("test_session_dir")

				// Check session directory exists
				if _, err := os.Stat(testSessionDir); os.IsNotExist(err) {
					return fmt.Errorf("session directory should exist: %s", testSessionDir)
				}

				return nil
			}),

			harness.NewStep("Verify pid.lock file exists and contains valid PID", func(ctx *harness.Context) error {
				testSessionDir := ctx.GetString("test_session_dir")
				pidFile := filepath.Join(testSessionDir, "pid.lock")

				// Check pid.lock exists
				if _, err := os.Stat(pidFile); os.IsNotExist(err) {
					return fmt.Errorf("pid.lock file should exist: %s", pidFile)
				}

				// Read PID
				pidContent, err := os.ReadFile(pidFile)
				if err != nil {
					return fmt.Errorf("failed to read pid.lock: %w", err)
				}

				var pid int
				if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err != nil {
					return fmt.Errorf("pid.lock should contain valid PID, got: %s", pidContent)
				}

				if pid <= 0 {
					return fmt.Errorf("PID should be positive, got: %d", pid)
				}

				ctx.ShowCommandOutput("Info", fmt.Sprintf("Session tracked with PID: %d", pid), "")
				return nil
			}),

			harness.NewStep("Verify metadata.json exists and has correct structure", func(ctx *harness.Context) error {
				testSessionDir := ctx.GetString("test_session_dir")
				metadataFile := filepath.Join(testSessionDir, "metadata.json")

				// Check metadata.json exists
				if _, err := os.Stat(metadataFile); os.IsNotExist(err) {
					return fmt.Errorf("metadata.json file should exist: %s", metadataFile)
				}

				// Read and parse metadata
				metadataContent, err := os.ReadFile(metadataFile)
				if err != nil {
					return fmt.Errorf("failed to read metadata.json: %w", err)
				}

				var metadata map[string]interface{}
				if err := json.Unmarshal(metadataContent, &metadata); err != nil {
					return fmt.Errorf("failed to parse metadata.json: %w", err)
				}

				// Verify required fields
				requiredFields := []string{"session_id", "pid", "working_directory", "user", "started_at"}
				for _, field := range requiredFields {
					if _, ok := metadata[field]; !ok {
						return fmt.Errorf("metadata.json missing required field: %s", field)
					}
				}

				ctx.ShowCommandOutput("Success", "metadata.json has correct structure", "")
				ctx.ShowCommandOutput("Info", "Session directory created at", testSessionDir)
				ctx.ShowCommandOutput("Info", "All artifacts in", filepath.Join(ctx.RootDir, "hooks"))
				return nil
			}),

			harness.NewStep("Test idempotent session creation", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")

				// Call pretooluse again with same session ID
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"tool_name": "bash",
					"tool_input": {"command": "echo test2"}
				}`, sessionID)

				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()

				if err := assert.Equal(0, result.ExitCode, "second pretooluse should succeed"); err != nil {
					return err
				}

				// Verify session directory still exists (not recreated)
				testSessionDir := ctx.GetString("test_session_dir")
				if _, err := os.Stat(testSessionDir); os.IsNotExist(err) {
					return fmt.Errorf("session directory should still exist")
				}

				return nil
			}),

			harness.NewStep("Show artifact locations", func(ctx *harness.Context) error {
				ctx.ShowCommandOutput("Info", "Test database at", filepath.Join(ctx.RootDir, "test.db"))
				ctx.ShowCommandOutput("Info", "Session artifacts in", filepath.Join(ctx.RootDir, "hooks/sessions"))
				return nil
			}),

			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// SessionCleanupOnStop tests that session directories are preserved on completion for later cleanup
func SessionCleanupOnStop() *harness.Scenario {
	return &harness.Scenario{
		Name:        "session-cleanup-on-stop",
		Description: "Tests session directory is preserved when session completes (cleanup handled separately)",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),

			harness.NewStep("Create and track a test session", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := fmt.Sprintf("cleanup-test-%d", time.Now().Unix())
				ctx.Set("session_id", sessionID)

				// Create session via pretooluse
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"tool_name": "bash",
					"tool_input": {"command": "echo test"}
				}`, sessionID)

				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()

				if err := assert.Equal(0, result.ExitCode, "pretooluse should succeed"); err != nil {
					return err
				}

				// Store session directory path (using HOME from context)
				sandboxedHome := ctx.GetString("sandboxed_home")
				testSessionDir := filepath.Join(sandboxedHome, ".local", "state", "grove", "hooks", "sessions", sessionID)
				ctx.Set("test_session_dir", testSessionDir)

				// Verify it was created
				if _, err := os.Stat(testSessionDir); os.IsNotExist(err) {
					return fmt.Errorf("session directory should exist: %s", testSessionDir)
				}

				return nil
			}),

			harness.NewStep("Stop session with 'completed' exit reason", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")

				// Call stop hook with completed status
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"exit_reason": "completed"
				}`, sessionID)

				cmd := command.New(hooksBinary, "stop").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return assert.Equal(0, result.ExitCode, "stop hook should succeed")
			}),

			harness.NewStep("Verify session directory is preserved for later cleanup", func(ctx *harness.Context) error {
				testSessionDir := ctx.GetString("test_session_dir")

				// Session directory should be PRESERVED after completion (for transcript archiving).
				// Cleanup is now handled separately by 'grove-hooks sessions cleanup' command.
				if _, err := os.Stat(testSessionDir); os.IsNotExist(err) {
					return fmt.Errorf("session directory should be preserved after completion (for transcript archiving): %s", testSessionDir)
				}

				ctx.ShowCommandOutput("Success", "Session directory preserved for later cleanup", "")
				return nil
			}),

			harness.NewStep("Verify session is marked as completed in database", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionID := ctx.GetString("session_id")

				cmd := command.New(hooksBinary, "sessions", "get", sessionID, "--json")
				result := cmd.Run()

				if result.ExitCode != 0 {
					return fmt.Errorf("sessions get should succeed")
				}

				var session models.Session
				if err := json.Unmarshal([]byte(result.Stdout), &session); err != nil {
					return fmt.Errorf("failed to parse session JSON: %w", err)
				}

				return assert.Equal("completed", session.Status, "session status should be completed")
			}),

			harness.NewStep("Test idle sessions are NOT cleaned up", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				idleSessionID := fmt.Sprintf("idle-test-%d", time.Now().Unix())

				// Create session
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"tool_name": "bash",
					"tool_input": {"command": "echo test"}
				}`, idleSessionID)

				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				cmd.Run()

				sandboxedHome := ctx.GetString("sandboxed_home")
				idleSessionDir := filepath.Join(sandboxedHome, ".local", "state", "grove", "hooks", "sessions", idleSessionID)

				// Stop with empty exit_reason (normal stop, not completion)
				jsonInput = fmt.Sprintf(`{
					"session_id": "%s",
					"exit_reason": ""
				}`, idleSessionID)

				cmd = command.New(hooksBinary, "stop").Stdin(strings.NewReader(jsonInput))
				result := cmd.Run()

				if result.ExitCode != 0 {
					return fmt.Errorf("stop hook should succeed")
				}

				// Session directory should still exist for idle sessions
				if _, err := os.Stat(idleSessionDir); os.IsNotExist(err) {
					return fmt.Errorf("idle session directory should NOT be removed: %s", idleSessionDir)
				}

				ctx.ShowCommandOutput("Success", "Idle session directory preserved", idleSessionDir)
				ctx.ShowCommandOutput("Info", "All artifacts in", filepath.Join(ctx.RootDir, "hooks"))
				return nil
			}),

			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// SessionDiscoveryService tests the discovery service that finds live sessions
func SessionDiscoveryService() *harness.Scenario {
	return &harness.Scenario{
		Name:        "session-discovery-service",
		Description: "Tests discovery service finds live sessions from filesystem",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),

			harness.NewStep("Create multiple test sessions", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				sessionIDs := []string{
					fmt.Sprintf("discovery-1-%d", time.Now().Unix()),
					fmt.Sprintf("discovery-2-%d", time.Now().Unix()+1),
					fmt.Sprintf("discovery-3-%d", time.Now().Unix()+2),
				}

				for _, sessionID := range sessionIDs {
					jsonInput := fmt.Sprintf(`{
						"session_id": "%s",
						"tool_name": "bash",
						"tool_input": {"command": "echo test"}
					}`, sessionID)

					cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
					result := cmd.Run()

					if result.ExitCode != 0 {
						return fmt.Errorf("failed to create session %s", sessionID)
					}
				}

				ctx.Set("session_ids", sessionIDs)
				return nil
			}),

			harness.NewStep("List sessions using discovery service", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// The sessions list command should use discovery service
				cmd := command.New(hooksBinary, "sessions", "list", "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.ExitCode != 0 {
					return fmt.Errorf("sessions list should succeed")
				}

				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse sessions JSON: %w", err)
				}

				// Verify all test sessions are found
				sessionIDs := ctx.Get("session_ids").([]string)
				foundCount := 0

				for _, session := range sessions {
					if id, ok := session["id"].(string); ok {
						for _, testID := range sessionIDs {
							if strings.Contains(id, "discovery-") && id == testID {
								foundCount++
								break
							}
						}
					}
				}

				if foundCount != len(sessionIDs) {
					return fmt.Errorf("expected to find %d sessions, found %d", len(sessionIDs), foundCount)
				}

				ctx.ShowCommandOutput("Success", fmt.Sprintf("Discovery service found all %d sessions", foundCount), "")
				return nil
			}),

			harness.NewStep("Test discovery with stale PID", func(ctx *harness.Context) error {
				sandboxedHome := ctx.GetString("sandboxed_home")
				staleSessionID := fmt.Sprintf("stale-%d", time.Now().Unix())
				staleSessionDir := filepath.Join(sandboxedHome, ".local", "state", "grove", "hooks", "sessions", staleSessionID)

				// Create a stale session manually with fake dead PID
				os.MkdirAll(staleSessionDir, 0755)

				// Write a PID that definitely doesn't exist (use a very high number)
				pidFile := filepath.Join(staleSessionDir, "pid.lock")
				os.WriteFile(pidFile, []byte("999999"), 0644)

				// Write minimal metadata
				metadataFile := filepath.Join(staleSessionDir, "metadata.json")
				metadata := fmt.Sprintf(`{
					"session_id": "%s",
					"pid": 999999,
					"working_directory": "/tmp",
					"user": "test",
					"started_at": "2024-01-01T00:00:00Z"
				}`, staleSessionID)
				os.WriteFile(metadataFile, []byte(metadata), 0644)

				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// List sessions
				cmd := command.New(hooksBinary, "sessions", "list", "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse JSON: %w", err)
				}

				// Find the stale session
				var staleSession map[string]interface{}
				for _, session := range sessions {
					if id, ok := session["id"].(string); ok && id == staleSessionID {
						staleSession = session
						break
					}
				}

				if staleSession == nil {
					// This might be OK - the discovery service may filter out sessions with dead PIDs
					ctx.ShowCommandOutput("Note", "Stale session was not included in list", "This is acceptable - discovery service filters dead PIDs")
					return nil
				}

				// If it is included, it should be marked as "interrupted"
				status, ok := staleSession["status"].(string)
				if !ok {
					return fmt.Errorf("session status should be a string")
				}

				if status != "interrupted" {
					return fmt.Errorf("stale session should have status 'interrupted', got: %s", status)
				}

				ctx.ShowCommandOutput("Success", "Discovery service correctly identified stale session", "")
				return nil
			}),

			harness.NewStep("Show artifact locations", func(ctx *harness.Context) error {
				ctx.ShowCommandOutput("Info", "All sessions in", filepath.Join(ctx.RootDir, "hooks/sessions"))
				ctx.ShowCommandOutput("Info", "Test database at", filepath.Join(ctx.RootDir, "test.db"))
				return nil
			}),

			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// SessionsListIntegration tests that sessions list shows live sessions
func SessionsListIntegration() *harness.Scenario {
	return &harness.Scenario{
		Name:        "sessions-list-integration",
		Description: "Tests sessions list command integration with discovery service",
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),

			harness.NewStep("Create mix of live and completed sessions", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Create 2 live sessions
				liveSessionIDs := []string{
					fmt.Sprintf("live-1-%d", time.Now().Unix()),
					fmt.Sprintf("live-2-%d", time.Now().Unix()+1),
				}

				for _, sessionID := range liveSessionIDs {
					jsonInput := fmt.Sprintf(`{
						"session_id": "%s",
						"tool_name": "bash",
						"tool_input": {"command": "echo test"}
					}`, sessionID)

					cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
					cmd.Run()
				}

				// Create 1 completed session
				completedSessionID := fmt.Sprintf("completed-%d", time.Now().Unix())
				jsonInput := fmt.Sprintf(`{
					"session_id": "%s",
					"tool_name": "bash",
					"tool_input": {"command": "echo test"}
				}`, completedSessionID)

				cmd := command.New(hooksBinary, "pretooluse").Stdin(strings.NewReader(jsonInput))
				cmd.Run()

				// Complete it
				jsonInput = fmt.Sprintf(`{
					"session_id": "%s",
					"exit_reason": "completed"
				}`, completedSessionID)

				cmd = command.New(hooksBinary, "stop").Stdin(strings.NewReader(jsonInput))
				cmd.Run()

				ctx.Set("live_session_ids", liveSessionIDs)
				ctx.Set("completed_session_id", completedSessionID)

				return nil
			}),

			harness.NewStep("Verify sessions list shows both live and completed", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list", "--json")
				result := cmd.Run()

				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse sessions JSON: %w", err)
				}

				liveSessionIDs := ctx.Get("live_session_ids").([]string)
				completedSessionID := ctx.GetString("completed_session_id")

				// Count found sessions
				foundLive := 0
				foundCompleted := false

				for _, session := range sessions {
					if id, ok := session["id"].(string); ok {
						for _, liveID := range liveSessionIDs {
							if id == liveID {
								if session["status"] == "running" {
									foundLive++
								}
								break
							}
						}

						if id == completedSessionID {
							if session["status"] == "completed" {
								foundCompleted = true
							}
						}
					}
				}

				if foundLive != len(liveSessionIDs) {
					return fmt.Errorf("expected to find %d live sessions, found %d", len(liveSessionIDs), foundLive)
				}

				if !foundCompleted {
					return fmt.Errorf("expected to find completed session")
				}

				ctx.ShowCommandOutput("Success", fmt.Sprintf("Found %d live and 1 completed session", foundLive), "")
				return nil
			}),

			harness.NewStep("Verify table output format", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				// Check for expected columns
				if err := assert.Contains(result.Stdout, "ID", "should have ID column"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "STATUS", "should have STATUS column"); err != nil {
					return err
				}

				// Check for session types - be flexible with ID truncation
				// The table truncates IDs, so just check for the prefix "live-"
				if err := assert.Contains(result.Stdout, "live-", "should show live session"); err != nil {
					return err
				}

				// Also verify running status appears
				if err := assert.Contains(result.Stdout, "running", "should show running status"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Show artifact locations", func(ctx *harness.Context) error {
				ctx.ShowCommandOutput("Info", "Live and completed sessions in", filepath.Join(ctx.RootDir, "hooks/sessions"))
				ctx.ShowCommandOutput("Info", "Test database at", filepath.Join(ctx.RootDir, "test.db"))
				return nil
			}),

			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}
