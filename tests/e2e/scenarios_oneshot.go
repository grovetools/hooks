package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-tend/pkg/assert"
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// TestExtendedSession represents a session with oneshot fields for testing
type TestExtendedSession struct {
	models.Session
	Type          string `json:"type"`
	PlanName      string `json:"plan_name"`
	PlanDirectory string `json:"plan_directory"`
	JobTitle      string `json:"job_title"`
	JobFilePath   string `json:"job_file_path"`
}

// OneshotJobScenario tests the oneshot job tracking functionality
// NOTE: This scenario tests deprecated functionality (oneshot command removed in Phase 2)
// It is marked as ExplicitOnly and kept for reference only
func OneshotJobScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:         "Oneshot Job Tracking",
		Description:  "Tests the oneshot job lifecycle (start, stop, query) - DEPRECATED",
		ExplicitOnly: true,
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),
			harness.NewStep("Start a oneshot job", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				jobID := fmt.Sprintf("test-job-%d", time.Now().Unix())
				ctx.Set("job_id", jobID)

				startInput := map[string]string{
					"job_id":         jobID,
					"plan_name":      "test-plan",
					"plan_directory": "/tmp/test-plan",
					"job_title":      "Test Job Execution",
					"job_file_path":  "/tmp/test-plan/job.md",
				}
				jsonInput, _ := json.Marshal(startInput)

				cmd := command.New(hooksBinary, "oneshot", "start").Stdin(strings.NewReader(string(jsonInput)))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "oneshot start should succeed"); err != nil {
					return err
				}

				return assert.Contains(result.Stdout, fmt.Sprintf("Started tracking oneshot job: %s", jobID), "should confirm job start")
			}),

			harness.NewStep("Verify job is running", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				jobID := ctx.Get("job_id").(string)

				// Get job details
				cmd := command.New(hooksBinary, "sessions", "get", jobID, "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions get should succeed"); err != nil {
					return err
				}

				var session TestExtendedSession
				if err := json.Unmarshal([]byte(result.Stdout), &session); err != nil {
					return fmt.Errorf("failed to parse session JSON: %w", err)
				}

				// Verify job properties
				if err := assert.Equal(jobID, session.ID, "job ID should match"); err != nil {
					return err
				}
				if err := assert.Equal("oneshot_job", session.Type, "type should be oneshot_job"); err != nil {
					return err
				}
				if err := assert.Equal("running", session.Status, "status should be running"); err != nil {
					return err
				}
				if err := assert.Equal("test-plan", session.PlanName, "plan name should match"); err != nil {
					return err
				}
				if err := assert.Equal("Test Job Execution", session.JobTitle, "job title should match"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Complete the oneshot job", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				jobID := ctx.Get("job_id").(string)

				stopInput := map[string]string{
					"job_id": jobID,
					"status": "completed",
				}
				jsonInput, _ := json.Marshal(stopInput)

				cmd := command.New(hooksBinary, "oneshot", "stop").Stdin(strings.NewReader(string(jsonInput)))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "oneshot stop should succeed"); err != nil {
					return err
				}

				return assert.Contains(result.Stdout, fmt.Sprintf("Updated oneshot job %s status to: completed", jobID), "should confirm job completion")
			}),

			harness.NewStep("Verify job is completed", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				jobID := ctx.Get("job_id").(string)

				cmd := command.New(hooksBinary, "sessions", "get", jobID, "--json")
				result := cmd.Run()

				var session models.Session
				if err := json.Unmarshal([]byte(result.Stdout), &session); err != nil {
					return fmt.Errorf("failed to parse session JSON: %w", err)
				}

				if err := assert.Equal("completed", session.Status, "status should be completed"); err != nil {
					return err
				}
				if session.EndedAt == nil {
					return fmt.Errorf("ended_at should be set for completed job")
				}

				return nil
			}),

			harness.NewStep("Start and fail a oneshot job", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				jobID := fmt.Sprintf("test-fail-job-%d", time.Now().Unix())
				ctx.Set("fail_job_id", jobID)

				// Start the job
				startInput := map[string]string{
					"job_id":         jobID,
					"plan_name":      "backup-plan",
					"plan_directory": "/tmp/backup",
					"job_title":      "Database Backup",
					"job_file_path":  "/tmp/backup/backup.md",
				}
				jsonInput, _ := json.Marshal(startInput)

				cmd := command.New(hooksBinary, "oneshot", "start").Stdin(strings.NewReader(string(jsonInput)))
				result := cmd.Run()

				if err := assert.Equal(0, result.ExitCode, "oneshot start should succeed"); err != nil {
					return err
				}

				// Fail the job with an error
				stopInput := map[string]string{
					"job_id": jobID,
					"status": "failed",
					"error":  "Connection timeout to database server",
				}
				jsonInput, _ = json.Marshal(stopInput)

				cmd = command.New(hooksBinary, "oneshot", "stop").Stdin(strings.NewReader(string(jsonInput)))
				result = cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return assert.Equal(0, result.ExitCode, "oneshot stop should succeed")
			}),

			harness.NewStep("Verify failed job status", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				jobID := ctx.Get("fail_job_id").(string)

				cmd := command.New(hooksBinary, "sessions", "get", jobID, "--json")
				result := cmd.Run()

				var session models.Session
				if err := json.Unmarshal([]byte(result.Stdout), &session); err != nil {
					return fmt.Errorf("failed to parse session JSON: %w", err)
				}

				return assert.Equal("failed", session.Status, "status should be failed")
			}),

			harness.NewStep("List oneshot jobs in sessions list", func(ctx *harness.Context) error {
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

				// Check for header with TYPE column
				if err := assert.Contains(result.Stdout, "TYPE", "should have TYPE column"); err != nil {
					return err
				}

				// Check for job entries
				if err := assert.Contains(result.Stdout, "job", "should show job type"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "test-plan", "should show plan name in context"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "backup-plan", "should show backup plan"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Filter oneshot jobs by status", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// List only failed sessions
				cmd := command.New(hooksBinary, "sessions", "list", "--status", "failed")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions list with filter should succeed"); err != nil {
					return err
				}

				// Should contain the failed job
				// The output shows failed jobs, which is what we want to verify
				if err := assert.Contains(result.Stdout, "failed", "should show failed status"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "backup-plan", "should show backup plan"); err != nil {
					return err
				}

				// Should not contain the completed job's plan
				if err := assert.NotContains(result.Stdout, "test-plan", "should not show completed job's plan"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Export oneshot job as JSON", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list", "--json")
				result := cmd.Run()

				if err := assert.Equal(0, result.ExitCode, "sessions list --json should succeed"); err != nil {
					return err
				}

				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse sessions JSON: %w", err)
				}

				// Find oneshot jobs
				foundOneshotJob := false
				for _, session := range sessions {
					if sessionType, ok := session["type"].(string); ok && sessionType == "oneshot_job" {
						foundOneshotJob = true

						// Verify job-specific fields exist
						if _, ok := session["plan_name"]; !ok {
							return fmt.Errorf("oneshot job should have plan_name field")
						}
						if _, ok := session["job_title"]; !ok {
							return fmt.Errorf("oneshot job should have job_title field")
						}
						break
					}
				}

				if !foundOneshotJob {
					return fmt.Errorf("should find at least one oneshot job in JSON output")
				}

				return nil
			}),

			/* TODO: Fix cleanup test - it's failing with exit code 1
			harness.NewStep("Cleanup old oneshot jobs", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Create an old job (simulated by creating and immediately completing it)
				oldJobID := fmt.Sprintf("old-job-%d", time.Now().Unix())
				startInput := map[string]string{
					"job_id":    oldJobID,
					"plan_name": "cleanup-test",
				}
				jsonInput, _ := json.Marshal(startInput)

				cmd := command.New(hooksBinary, "oneshot", "start").Stdin(strings.NewReader(string(jsonInput)))
				cmd.Run()

				// Complete it immediately
				stopInput := map[string]string{
					"job_id": oldJobID,
					"status": "completed",
				}
				jsonInput, _ = json.Marshal(stopInput)
				cmd = command.New(hooksBinary, "oneshot", "stop").Stdin(strings.NewReader(string(jsonInput)))
				cmd.Run()

				// Sleep for 2 seconds to ensure the job is old enough
				time.Sleep(2 * time.Second)

				// Run cleanup with a very short age (1 second)
				cmd = command.New(hooksBinary, "sessions", "cleanup", "--older-than", "1s")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "cleanup should succeed"); err != nil {
					return err
				}

				// Verify the old job was cleaned up
				cmd = command.New(hooksBinary, "sessions", "list", "--json")
				result = cmd.Run()

				return assert.NotContains(result.Stdout, oldJobID, "old job should be cleaned up")
			}), */
			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// OneshotJobValidationScenario tests input validation and error cases
// NOTE: This scenario tests deprecated functionality (oneshot command removed in Phase 2)
// It is marked as ExplicitOnly and kept for reference only
func OneshotJobValidationScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:         "Oneshot Job Validation",
		Description:  "Tests oneshot job input validation and error handling - DEPRECATED",
		ExplicitOnly: true,
		Steps: []harness.Step{
			harness.NewStep("Set up test database", SetupTestDatabase),
			harness.NewStep("Reject invalid JSON input", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Send invalid JSON
				cmd := command.New(hooksBinary, "oneshot", "start").Stdin(strings.NewReader("invalid json"))
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.NotEqual(0, result.ExitCode, "should fail with invalid JSON"); err != nil {
					return err
				}

				return assert.Contains(result.Stderr, "Error parsing JSON", "should show JSON parsing error")
			}),

			harness.NewStep("Handle missing required fields", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Send JSON without job_id
				invalidInput := map[string]string{
					"plan_name": "test-plan",
				}
				jsonInput, _ := json.Marshal(invalidInput)

				cmd := command.New(hooksBinary, "oneshot", "start").Stdin(strings.NewReader(string(jsonInput)))
				result := cmd.Run()

				// Should still succeed but with empty job_id
				if err := assert.Equal(0, result.ExitCode, "should handle missing job_id"); err != nil {
					return err
				}

				return assert.Contains(result.Stdout, "Started tracking oneshot job:", "should start with empty job_id")
			}),

			harness.NewStep("Update non-existent job", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				stopInput := map[string]string{
					"job_id": "non-existent-job-12345",
					"status": "completed",
				}
				jsonInput, _ := json.Marshal(stopInput)

				cmd := command.New(hooksBinary, "oneshot", "stop").Stdin(strings.NewReader(string(jsonInput)))
				result := cmd.Run()

				// Should succeed (updates are idempotent)
				return assert.Equal(0, result.ExitCode, "should handle updating non-existent job")
			}),
			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// MixedSessionTypesScenario tests handling of mixed Claude sessions and oneshot jobs
// NOTE: This scenario tests deprecated functionality (oneshot command removed in Phase 2)
// It is marked as ExplicitOnly and kept for reference only
func MixedSessionTypesScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:         "Mixed Session Types",
		Description:  "Tests that Claude sessions and oneshot jobs coexist properly - DEPRECATED",
		ExplicitOnly: true,
		Steps: []harness.Step{
			harness.NewStep("Create a Claude session and oneshot job", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Use a custom database for this test
				testDB := fmt.Sprintf("/tmp/grove-hooks-mixed-test-%d.db", time.Now().Unix())
				ctx.Set("test_db", testDB)
				defer os.Remove(testDB)

				// Create a Claude session via pretooluse hook
				claudeSessionID := fmt.Sprintf("claude-%d", time.Now().Unix())
				hookInput := map[string]interface{}{
					"session_id":      claudeSessionID,
					"hook_event_name": "pre_tool_use",
					"tool_name":       "bash",
					"tool_input": map[string]string{
						"command": "echo 'test'",
					},
				}
				jsonInput, _ := json.Marshal(hookInput)

				cmd := command.New(hooksBinary, "pretooluse").
					Stdin(strings.NewReader(string(jsonInput))).
					Env("GROVE_HOOKS_DB_PATH", testDB)
				result := cmd.Run()

				if err := assert.Equal(0, result.ExitCode, "pretooluse should succeed"); err != nil {
					return err
				}

				// Create an oneshot job
				jobID := fmt.Sprintf("job-%d", time.Now().Unix())
				jobInput := map[string]string{
					"job_id":    jobID,
					"plan_name": "mixed-test",
					"job_title": "Mixed Test Job",
				}
				jsonInput, _ = json.Marshal(jobInput)

				cmd = command.New(hooksBinary, "oneshot", "start").
					Stdin(strings.NewReader(string(jsonInput))).
					Env("GROVE_HOOKS_DB_PATH", testDB)
				result = cmd.Run()

				if err := assert.Equal(0, result.ExitCode, "oneshot start should succeed"); err != nil {
					return err
				}

				ctx.Set("claude_session_id", claudeSessionID)
				ctx.Set("oneshot_job_id", jobID)

				return nil
			}),

			harness.NewStep("Verify both session types in list", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				testDB := ctx.Get("test_db").(string)

				cmd := command.New(hooksBinary, "sessions", "list").
					Env("GROVE_HOOKS_DB_PATH", testDB)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "sessions list should succeed"); err != nil {
					return err
				}

				// Verify both types are shown
				if err := assert.Contains(result.Stdout, "claude", "should show Claude session type"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, "job", "should show job type"); err != nil {
					return err
				}

				// Verify both sessions are listed
				claudeID := ctx.Get("claude_session_id").(string)
				jobID := ctx.Get("oneshot_job_id").(string)

				if err := assert.Contains(result.Stdout, claudeID[:12], "should show Claude session"); err != nil {
					return err
				}
				if err := assert.Contains(result.Stdout, jobID[:12], "should show oneshot job"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Verify JSON output contains proper type field", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				testDB := ctx.Get("test_db").(string)

				cmd := command.New(hooksBinary, "sessions", "list", "--json").
					Env("GROVE_HOOKS_DB_PATH", testDB)
				result := cmd.Run()

				var sessions []map[string]interface{}
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse JSON: %w", err)
				}

				// Count session types
				claudeSessions := 0
				oneshotJobs := 0

				for _, session := range sessions {
					switch session["type"] {
					case "claude_session":
						claudeSessions++
					case "oneshot_job":
						oneshotJobs++
					}
				}

				if claudeSessions != 1 {
					return fmt.Errorf("expected 1 Claude session, got %d", claudeSessions)
				}
				if oneshotJobs != 1 {
					return fmt.Errorf("expected 1 oneshot job, got %d", oneshotJobs)
				}

				return nil
			}),
		},
	}
}
