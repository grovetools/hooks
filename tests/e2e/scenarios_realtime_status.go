package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/git"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// RealtimeStatusUpdateScenario tests that job statuses are updated in real-time
// based on filesystem checks (lock files, PIDs) without waiting for cache expiration
func RealtimeStatusUpdateScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:         "realtime-status-updates",
		Description:  "Tests that job statuses update in real-time based on lock files and PIDs, not cached data",
		Tags:         []string{"integration", "flow", "realtime", "explicit"},
		ExplicitOnly: true,
		Steps: []harness.Step{
			// Step 1: Setup test environment
			harness.NewStep("Setup test environment with grove-flow plan", func(ctx *harness.Context) error {
				// Init git repo
				git.Init(ctx.RootDir)
				git.SetupTestConfig(ctx.RootDir)
				fs.WriteString(filepath.Join(ctx.RootDir, "README.md"), "Test project")
				git.Add(ctx.RootDir, ".")
				git.Commit(ctx.RootDir, "Initial commit")

				// Create grove.yml
				configContent := `name: realtime-test
flow:
  plans_directory: ./plans
`
				fs.WriteString(filepath.Join(ctx.RootDir, "grove.yml"), configContent)

				// Setup database
				return SetupTestDatabase(ctx)
			}),

			// Step 2: Use flow to create a plan, then manually create test jobs
			harness.NewStep("Create test jobs with different types and states", func(ctx *harness.Context) error {
				// Initialize a flow plan first
				cmd := command.New("flow", "plan", "init", "test-plan").Dir(ctx.RootDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)
				if result.Error != nil {
					return fmt.Errorf("failed to init plan: %w", result.Error)
				}

				plansDir := filepath.Join(ctx.RootDir, "plans", "test-plan")
				if err := os.MkdirAll(plansDir, 0755); err != nil {
					return err
				}

				// Create a oneshot job with status=running (will be interrupted without lock file)
				oneshotJob := `---
id: test-oneshot
title: Test Oneshot Job
type: oneshot
status: running
updated_at: 2025-01-01T12:00:00-04:00
---

Test oneshot job content
`
				fs.WriteString(filepath.Join(plansDir, "01-oneshot.md"), oneshotJob)

				// Create a chat job with status=running (should stay running without lock file)
				chatJob := `---
id: test-chat
title: Test Chat Job
type: chat
status: running
updated_at: 2025-01-01T12:00:00-04:00
---

Test chat job content
`
				fs.WriteString(filepath.Join(plansDir, "02-chat.md"), chatJob)

				// Create an interactive_agent job (should stay running without lock file)
				interactiveJob := `---
id: test-interactive
title: Test Interactive Agent Job
type: interactive_agent
status: running
updated_at: 2025-01-01T12:00:00-04:00
---

Test interactive agent job content
`
				fs.WriteString(filepath.Join(plansDir, "03-interactive.md"), interactiveJob)

				// Create a headless_agent job (should be interrupted without lock file)
				headlessJob := `---
id: test-headless
title: Test Headless Agent Job
type: headless_agent
status: running
updated_at: 2025-01-01T12:00:00-04:00
---

Test headless agent job content
`
				fs.WriteString(filepath.Join(plansDir, "04-headless.md"), headlessJob)

				// Create a completed job (terminal state, should stay completed)
				completedJob := `---
id: test-completed
title: Test Completed Job
type: oneshot
status: completed
updated_at: 2025-01-01T12:00:00-04:00
completed_at: 2025-01-01T12:05:00-04:00
---

Test completed job content
`
				fs.WriteString(filepath.Join(plansDir, "05-completed.md"), completedJob)

				return nil
			}),

			// Step 3: Test status without lock files - oneshot and headless should be interrupted
			harness.NewStep("Verify statuses without lock files", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Clear any cached data
				cacheFile := filepath.Join(os.Getenv("HOME"), ".grove", "hooks", "flow_jobs_cache.json")
				os.Remove(cacheFile)

				cmd := command.New(hooksBinary, "sessions", "list", "--json", "--type", "job").Dir(ctx.RootDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.Error != nil {
					return fmt.Errorf("sessions list failed: %w", result.Error)
				}

				// Parse JSON output
				var sessions []TestExtendedSessionForIntegration
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse JSON: %w", err)
				}

				// Build status map
				statusMap := make(map[string]string)
				for _, s := range sessions {
					statusMap[s.ID] = s.Status
				}

				// Verify expected statuses
				expectedStatuses := map[string]string{
					"test-oneshot":     "interrupted", // oneshot without lock file
					"test-chat":        "running",     // chat jobs don't need lock files
					"test-interactive": "running",     // interactive_agent jobs don't need lock files
					"test-headless":    "interrupted", // headless_agent without lock file
					"test-completed":   "completed",   // terminal states are respected
				}

				for jobID, expectedStatus := range expectedStatuses {
					actualStatus, found := statusMap[jobID]
					if !found {
						return fmt.Errorf("job %s not found in sessions list", jobID)
					}
					if actualStatus != expectedStatus {
						return fmt.Errorf("job %s: expected status=%s, got status=%s", jobID, expectedStatus, actualStatus)
					}
					ctx.ShowCommandOutput("✓", fmt.Sprintf("Job %s correctly shows status: %s", jobID, actualStatus), "")
				}

				return nil
			}),

			// Step 4: Create lock files with live PIDs for oneshot and headless jobs
			harness.NewStep("Add lock files with live PIDs", func(ctx *harness.Context) error {
				plansDir := filepath.Join(ctx.RootDir, "plans", "test-plan")

				// Get current shell PID (guaranteed to be alive)
				currentPID := os.Getpid()
				pidStr := fmt.Sprintf("%d", currentPID)

				// Create lock files for oneshot and headless jobs
				fs.WriteString(filepath.Join(plansDir, "01-oneshot.md.lock"), pidStr)
				fs.WriteString(filepath.Join(plansDir, "04-headless.md.lock"), pidStr)

				ctx.ShowCommandOutput("Info", fmt.Sprintf("Created lock files with live PID: %d", currentPID), "")
				return nil
			}),

			// Step 5: Verify statuses update immediately with lock files (no cache wait)
			harness.NewStep("Verify real-time status updates with lock files", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// NOTE: We do NOT clear the cache here - testing that real-time updates work even with cache
				// The cache should still be valid from the previous step (< 1 minute old)

				cmd := command.New(hooksBinary, "sessions", "list", "--json", "--type", "job").Dir(ctx.RootDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.Error != nil {
					return fmt.Errorf("sessions list failed: %w", result.Error)
				}

				// Parse JSON output
				var sessions []TestExtendedSessionForIntegration
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse JSON: %w", err)
				}

				// Build status map
				statusMap := make(map[string]string)
				for _, s := range sessions {
					statusMap[s.ID] = s.Status
				}

				// Verify statuses updated to "running" now that lock files exist
				expectedStatuses := map[string]string{
					"test-oneshot":     "running",   // now has lock file with live PID
					"test-chat":        "running",   // still running (no lock file needed)
					"test-interactive": "running",   // still running (no lock file needed)
					"test-headless":    "running",   // now has lock file with live PID
					"test-completed":   "completed", // still completed (terminal state)
				}

				for jobID, expectedStatus := range expectedStatuses {
					actualStatus, found := statusMap[jobID]
					if !found {
						return fmt.Errorf("job %s not found in sessions list", jobID)
					}
					if actualStatus != expectedStatus {
						return fmt.Errorf("job %s: expected status=%s, got status=%s (real-time update failed)", jobID, expectedStatus, actualStatus)
					}
					ctx.ShowCommandOutput("✓", fmt.Sprintf("Job %s status updated in real-time to: %s", jobID, actualStatus), "")
				}

				return nil
			}),

			// Step 6: Test with dead PID - should revert to interrupted
			harness.NewStep("Verify dead PID detection", func(ctx *harness.Context) error {
				plansDir := filepath.Join(ctx.RootDir, "plans", "test-plan")

				// Use a PID that's very unlikely to exist
				deadPID := "99999"
				fs.WriteString(filepath.Join(plansDir, "01-oneshot.md.lock"), deadPID)
				fs.WriteString(filepath.Join(plansDir, "04-headless.md.lock"), deadPID)

				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list", "--json", "--type", "job").Dir(ctx.RootDir)
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if result.Error != nil {
					return fmt.Errorf("sessions list failed: %w", result.Error)
				}

				// Parse JSON output
				var sessions []TestExtendedSessionForIntegration
				if err := json.Unmarshal([]byte(result.Stdout), &sessions); err != nil {
					return fmt.Errorf("failed to parse JSON: %w", err)
				}

				// Build status map
				statusMap := make(map[string]string)
				for _, s := range sessions {
					statusMap[s.ID] = s.Status
				}

				// Jobs with dead PIDs should be interrupted
				if statusMap["test-oneshot"] != "interrupted" {
					return fmt.Errorf("oneshot job with dead PID should be interrupted, got: %s", statusMap["test-oneshot"])
				}
				if statusMap["test-headless"] != "interrupted" {
					return fmt.Errorf("headless job with dead PID should be interrupted, got: %s", statusMap["test-headless"])
				}

				ctx.ShowCommandOutput("✓", "Jobs with dead PIDs correctly marked as interrupted", "")
				return nil
			}),

			// Step 7: Verify cache performance - status checks should be fast
			harness.NewStep("Verify real-time checks are fast", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Run sessions list multiple times and ensure it's fast (< 1 second)
				for i := 0; i < 3; i++ {
					start := time.Now()
					cmd := command.New(hooksBinary, "sessions", "list", "--type", "job").Dir(ctx.RootDir)
					result := cmd.Run()
					elapsed := time.Since(start)

					if result.Error != nil {
						return fmt.Errorf("sessions list failed: %w", result.Error)
					}

					if elapsed > 2*time.Second {
						return fmt.Errorf("sessions list took %v (> 2s), real-time checks may not be working", elapsed)
					}

					ctx.ShowCommandOutput("✓", fmt.Sprintf("Run %d: sessions list completed in %v", i+1, elapsed), "")
				}

				return nil
			}),
		},
	}
}
