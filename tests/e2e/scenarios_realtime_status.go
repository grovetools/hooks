package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/git"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// Helper function to find a job file by its title within a plan directory
func findJobFileByTitle(planDir, title string) (string, error) {
	files, err := filepath.Glob(filepath.Join(planDir, "*.md"))
	if err != nil {
		return "", err
	}

	searchPattern1 := fmt.Sprintf("title: %s", title)
	searchPattern2 := fmt.Sprintf("title: \"%s\"", title)

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		contentStr := string(content)
		// Search for title in frontmatter (with or without quotes)
		if strings.Contains(contentStr, searchPattern1) || strings.Contains(contentStr, searchPattern2) {
			return file, nil
		}
	}

	// Enhanced error message for debugging
	var filesChecked []string
	for _, file := range files {
		filesChecked = append(filesChecked, filepath.Base(file))
	}
	return "", fmt.Errorf("job file with title '%s' not found in %s (searched for '%s' or '%s', checked files: %v)",
		title, planDir, searchPattern1, searchPattern2, filesChecked)
}

// Helper function to update the status in a job's frontmatter
func updateJobStatusInFile(filePath, newStatus string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	newContent := strings.Replace(string(content), "status: pending", fmt.Sprintf("status: %s", newStatus), 1)
	return os.WriteFile(filePath, []byte(newContent), 0644)
}


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

				jobDefinitions := []struct {
					title     string
					jobType   string
					setStatus string
				}{
					{"Test Oneshot Job", "oneshot", "running"},
					{"Test Chat Job", "chat", "running"},
					{"Test Interactive Agent Job", "interactive_agent", "running"},
					{"Test Headless Agent Job", "headless_agent", "running"},
					{"Test Completed Job", "oneshot", "completed"},
				}

				for _, jobDef := range jobDefinitions {
					// Use flow plan add to create the job
					addCmd := command.New("flow", "plan", "add", "test-plan",
						"--title", jobDef.title,
						"--type", jobDef.jobType,
						"-p", fmt.Sprintf("Test content for %s", jobDef.title)).Dir(ctx.RootDir)

					addResult := addCmd.Run()
					ctx.ShowCommandOutput(addCmd.String(), addResult.Stdout, addResult.Stderr)
					if addResult.Error != nil {
						return fmt.Errorf("failed to add job '%s': %w", jobDef.title, addResult.Error)
					}

					// Sleep briefly to ensure filesystem is synced
					time.Sleep(100 * time.Millisecond)

					// Debug: list all markdown files
					files, globErr := filepath.Glob(filepath.Join(plansDir, "*.md"))
					if globErr != nil {
						return fmt.Errorf("glob error: %w", globErr)
					}
					var fileNames []string
					for _, f := range files {
						fileNames = append(fileNames, filepath.Base(f))
					}
					fmt.Fprintf(os.Stderr, "=== DEBUG: After adding '%s', found %d MD files: %v ===\n", jobDef.title, len(files), fileNames)
					ctx.ShowCommandOutput("Debug", fmt.Sprintf("MD files after adding '%s': %v", jobDef.title, fileNames), "")

					// Find the generated file
					jobFile, err := findJobFileByTitle(plansDir, jobDef.title)
					if err != nil {
						// Debug: show content of files to understand the issue
						for _, f := range files {
							content, _ := os.ReadFile(f)
							lines := strings.Split(string(content), "\n")
							if len(lines) > 10 {
								lines = lines[:10]
							}
							ctx.ShowCommandOutput("Debug file", f, strings.Join(lines, "\n"))
						}
						return fmt.Errorf("failed to find job file: %w", err)
					}

					// Manually update the status from 'pending' to the desired test state
					if err := updateJobStatusInFile(jobFile, jobDef.setStatus); err != nil {
						return fmt.Errorf("failed to update status for '%s': %w", jobDef.title, err)
					}
				}

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

				cmd := command.New(hooksBinary, "sessions", "list", "--json", "--type", "job").
					Dir(ctx.RootDir).
					Env("GROVE_HOOKS_DISCOVERY_MODE=local").
					Env("GROVE_DEBUG=1")
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
				fmt.Fprintf(os.Stderr, "=== DEBUG: Found %d sessions ===\n", len(sessions))
				for _, s := range sessions {
					fmt.Fprintf(os.Stderr, "=== DEBUG: Session - ID: %s, JobTitle: '%s', Status: %s ===\n", s.ID, s.JobTitle, s.Status)
					statusMap[s.JobTitle] = s.Status // Use JobTitle for mapping as ID is dynamic
				}

				// Verify expected statuses
				expectedStatuses := map[string]string{
					"Test Oneshot Job":           "interrupted", // oneshot without lock file
					"Test Chat Job":              "running",     // chat jobs don't need lock files
					"Test Interactive Agent Job": "running",     // interactive_agent jobs don't need lock files
					"Test Headless Agent Job":    "interrupted", // headless_agent without lock file
					"Test Completed Job":         "completed",   // terminal states are respected
				}

				for jobTitle, expectedStatus := range expectedStatuses {
					actualStatus, found := statusMap[jobTitle]
					if !found {
						return fmt.Errorf("job '%s' not found in sessions list", jobTitle)
					}
					if actualStatus != expectedStatus {
						return fmt.Errorf("job '%s': expected status=%s, got status=%s", jobTitle, expectedStatus, actualStatus)
					}
					ctx.ShowCommandOutput("✓", fmt.Sprintf("Job '%s' correctly shows status: %s", jobTitle, actualStatus), "")
				}

				return nil
			}),

			// Step 4: Create lock files with live PIDs for oneshot and headless jobs
			harness.NewStep("Add lock files with live PIDs", func(ctx *harness.Context) error {
				plansDir := filepath.Join(ctx.RootDir, "plans", "test-plan")

				// Get current shell PID (guaranteed to be alive)
				currentPID := os.Getpid()
				pidStr := fmt.Sprintf("%d", currentPID)

				// Find the job files to create locks for
				oneshotFile, err := findJobFileByTitle(plansDir, "Test Oneshot Job")
				if err != nil { return err }
				headlessFile, err := findJobFileByTitle(plansDir, "Test Headless Agent Job")
				if err != nil { return err }

				// Create lock files for oneshot and headless jobs
				fs.WriteString(oneshotFile+".lock", pidStr)
				fs.WriteString(headlessFile+".lock", pidStr)

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

				cmd := command.New(hooksBinary, "sessions", "list", "--json", "--type", "job").
					Dir(ctx.RootDir).
					Env("GROVE_HOOKS_DISCOVERY_MODE=local")
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
					statusMap[s.JobTitle] = s.Status
				}

				// Verify statuses updated to "running" now that lock files exist
				expectedStatuses := map[string]string{
					"Test Oneshot Job":           "running",   // now has lock file with live PID
					"Test Chat Job":              "running",   // still running (no lock file needed)
					"Test Interactive Agent Job": "running",   // still running (no lock file needed)
					"Test Headless Agent Job":    "running",   // now has lock file with live PID
					"Test Completed Job":         "completed", // still completed (terminal state)
				}

				for jobTitle, expectedStatus := range expectedStatuses {
					actualStatus, found := statusMap[jobTitle]
					if !found {
						return fmt.Errorf("job '%s' not found in sessions list", jobTitle)
					}
					if actualStatus != expectedStatus {
						return fmt.Errorf("job '%s': expected status=%s, got status=%s (real-time update failed)", jobTitle, expectedStatus, actualStatus)
					}
					ctx.ShowCommandOutput("✓", fmt.Sprintf("Job '%s' status updated in real-time to: %s", jobTitle, actualStatus), "")
				}

				return nil
			}),

			// Step 6: Test with dead PID - should revert to interrupted
			harness.NewStep("Verify dead PID detection", func(ctx *harness.Context) error {
				plansDir := filepath.Join(ctx.RootDir, "plans", "test-plan")

				// Use a PID that's very unlikely to exist
				deadPID := "99999"
				oneshotFile, err := findJobFileByTitle(plansDir, "Test Oneshot Job")
				if err != nil { return err }
				headlessFile, err := findJobFileByTitle(plansDir, "Test Headless Agent Job")
				if err != nil { return err }

				fs.WriteString(oneshotFile+".lock", deadPID)
				fs.WriteString(headlessFile+".lock", deadPID)

				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				cmd := command.New(hooksBinary, "sessions", "list", "--json", "--type", "job").
					Dir(ctx.RootDir).
					Env("GROVE_HOOKS_DISCOVERY_MODE=local")
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
					statusMap[s.JobTitle] = s.Status
				}

				// Jobs with dead PIDs should be interrupted
				if statusMap["Test Oneshot Job"] != "interrupted" {
					return fmt.Errorf("oneshot job with dead PID should be interrupted, got: %s", statusMap["Test Oneshot Job"])
				}
				if statusMap["Test Headless Agent Job"] != "interrupted" {
					return fmt.Errorf("headless job with dead PID should be interrupted, got: %s", statusMap["Test Headless Agent Job"])
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
					cmd := command.New(hooksBinary, "sessions", "list", "--type", "job").
						Dir(ctx.RootDir).
						Env("GROVE_HOOKS_DISCOVERY_MODE=local")
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
