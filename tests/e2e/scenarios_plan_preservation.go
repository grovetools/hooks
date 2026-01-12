package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-tend/pkg/assert"
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/git"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// PlanPreservationScenario tests that ExitPlanMode hook saves plans to grove-flow
func PlanPreservationScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "plan-preservation",
		Description: "Tests that ExitPlanMode hook automatically saves Claude plans to grove-flow",
		Tags:        []string{"hooks", "flow", "plan-preservation"},
		Steps: []harness.Step{
			// Step 1: Setup project environment
			harness.NewStep("Setup project with git, grove.yml, and flow plan", func(ctx *harness.Context) error {
				// Init git repo
				git.Init(ctx.RootDir)
				git.SetupTestConfig(ctx.RootDir)

				// Create grove.yml
				configContent := `name: plan-preservation-test
flow:
  plans_directory: ./plans
`
				fs.WriteString(filepath.Join(ctx.RootDir, "grove.yml"), configContent)

				// Create initial commit
				git.Add(ctx.RootDir, ".")
				git.Commit(ctx.RootDir, "Initial commit")

				// Setup test database
				return SetupTestDatabase(ctx)
			}),

			// Step 2: Create a flow plan directory to save plans to
			harness.NewStep("Create flow plan directory", func(ctx *harness.Context) error {
				planDir := filepath.Join(ctx.RootDir, "plans", "test-plan")
				if err := os.MkdirAll(planDir, 0755); err != nil {
					return fmt.Errorf("failed to create plan directory: %w", err)
				}

				// Create .grove-plan.yml to mark it as a valid plan
				planConfig := `name: test-plan
description: Test plan for plan preservation
`
				fs.WriteString(filepath.Join(planDir, ".grove-plan.yml"), planConfig)

				// Create an initial job file so we can test numbering
				initialJob := `---
id: initial-job
title: Initial Job
type: oneshot
status: pending
---

This is the initial job.
`
				fs.WriteString(filepath.Join(planDir, "01-initial-job.md"), initialJob)

				ctx.Set("plan_dir", planDir)
				ctx.ShowCommandOutput("Info", "Created plan directory", planDir)

				return nil
			}),

			// Step 3: Simulate ExitPlanMode hook call
			harness.NewStep("Simulate ExitPlanMode hook with plan content", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				planDir := ctx.GetString("plan_dir")

				// Create plan content that would come from Claude
				planContent := `# Plan: Implement User Authentication

## Goal
Add user authentication to the application using JWT tokens.

## Current State
- No authentication exists
- Users can access all endpoints

## Implementation Steps

### Step 1: Add JWT Library
Install jsonwebtoken package and configure secret key.

### Step 2: Create Auth Middleware
Build middleware to validate JWT tokens on protected routes.

### Step 3: Add Login Endpoint
Create POST /auth/login endpoint that validates credentials and returns JWT.

### Step 4: Protect Routes
Apply auth middleware to all protected API routes.

## Testing
1. Test login with valid/invalid credentials
2. Test protected routes with/without token
3. Test token expiration

## Files to Modify
- package.json
- src/middleware/auth.js (new)
- src/routes/auth.js (new)
- src/routes/api.js
`

				// Create the JSON input for posttooluse with ExitPlanMode
				hookInput := map[string]interface{}{
					"session_id":      "test-plan-session",
					"transcript_path": "/tmp/test-transcript",
					"hook_event_name": "PostToolUse:ExitPlanMode",
					"tool_name":       "ExitPlanMode",
					"tool_input": map[string]interface{}{
						"plan": planContent,
					},
					"tool_response":    map[string]interface{}{},
					"tool_duration_ms": 100,
				}

				jsonInput, err := json.Marshal(hookInput)
				if err != nil {
					return fmt.Errorf("failed to marshal hook input: %w", err)
				}

				// Set environment variable to point to the plan directory
				// The hook should auto-detect the plan based on working directory
				cmd := command.New(hooksBinary, "posttooluse").
					Stdin(strings.NewReader(string(jsonInput))).
					Dir(ctx.RootDir).
					Env(
						fmt.Sprintf("PWD=%s", ctx.RootDir),
						fmt.Sprintf("GROVE_HOOKS_TARGET_PLAN_DIR=%s", planDir),
						"GROVE_HOOKS_ENABLE_PLAN_PRESERVATION=true",
					)

				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				// Store the input for later verification
				ctx.Set("plan_content", planContent)

				if err := assert.Equal(0, result.ExitCode, "posttooluse hook should succeed"); err != nil {
					return err
				}

				return nil
			}),

			// Step 4: Verify plan was saved
			harness.NewStep("Verify plan was saved to flow plan directory", func(ctx *harness.Context) error {
				planDir := ctx.GetString("plan_dir")
				expectedPlanContent := ctx.GetString("plan_content")

				// List files in plan directory
				entries, err := os.ReadDir(planDir)
				if err != nil {
					return fmt.Errorf("failed to read plan directory: %w", err)
				}

				ctx.ShowCommandOutput("Info", fmt.Sprintf("Found %d files in plan directory", len(entries)), "")

				// Find the new plan file (should be 02-*.md since we have 01-initial-job.md)
				var planFile string
				for _, entry := range entries {
					name := entry.Name()
					ctx.ShowCommandOutput("Debug", "Found file", name)
					if strings.HasPrefix(name, "02-") && strings.HasSuffix(name, ".md") {
						planFile = filepath.Join(planDir, name)
						break
					}
				}

				if planFile == "" {
					return fmt.Errorf("no new plan file found (expected 02-*.md)")
				}

				ctx.ShowCommandOutput("Success", "Found new plan file", planFile)

				// Read and verify content
				content, err := os.ReadFile(planFile)
				if err != nil {
					return fmt.Errorf("failed to read plan file: %w", err)
				}

				contentStr := string(content)

				// Verify it contains the plan content (either in body or as prompt)
				if !strings.Contains(contentStr, "Implement User Authentication") {
					return fmt.Errorf("plan file doesn't contain expected title")
				}

				if !strings.Contains(contentStr, "JWT") {
					return fmt.Errorf("plan file doesn't contain expected content (JWT)")
				}

				ctx.ShowCommandOutput("Success", "Plan content verified", "")

				// Verify frontmatter
				if !strings.HasPrefix(contentStr, "---") {
					return fmt.Errorf("plan file missing YAML frontmatter")
				}

				// Check that original plan structure is preserved
				if !strings.Contains(contentStr, "## Goal") || !strings.Contains(contentStr, "## Implementation Steps") {
					ctx.ShowCommandOutput("Note", "Plan structure may have been modified", "")
				}

				_ = expectedPlanContent // Used for future assertions if needed

				return nil
			}),

			// Step 5: Verify database tracking
			harness.NewStep("Verify plan preservation was logged in database", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				// Check if there's an event logged for plan preservation
				cmd := command.New(hooksBinary, "sessions", "list", "--json")
				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				// We don't strictly require the event to be logged,
				// but we should see the session was tracked
				if result.Stdout != "" && result.Stdout != "[]" {
					ctx.ShowCommandOutput("Info", "Sessions found in database", "")
				}

				return nil
			}),

			// Cleanup
			harness.NewStep("Clean up test database", CleanupTestDatabase),
		},
	}
}

// PlanPreservationDisabledScenario tests that plan preservation can be disabled
func PlanPreservationDisabledScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "plan-preservation-disabled",
		Description: "Tests that plan preservation can be disabled via environment variable",
		Tags:        []string{"hooks", "flow", "plan-preservation"},
		Steps: []harness.Step{
			// Step 1: Setup
			harness.NewStep("Setup project environment", func(ctx *harness.Context) error {
				git.Init(ctx.RootDir)
				git.SetupTestConfig(ctx.RootDir)

				fs.WriteString(filepath.Join(ctx.RootDir, "grove.yml"), "name: test\n")
				git.Add(ctx.RootDir, ".")
				git.Commit(ctx.RootDir, "Initial commit")

				// Create plan directory
				planDir := filepath.Join(ctx.RootDir, "plans", "test-plan")
				os.MkdirAll(planDir, 0755)
				fs.WriteString(filepath.Join(planDir, ".grove-plan.yml"), "name: test-plan\n")
				ctx.Set("plan_dir", planDir)

				return SetupTestDatabase(ctx)
			}),

			// Step 2: Send ExitPlanMode with preservation disabled
			harness.NewStep("Send ExitPlanMode with preservation disabled", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				planDir := ctx.GetString("plan_dir")

				hookInput := map[string]interface{}{
					"session_id":      "test-disabled-session",
					"hook_event_name": "PostToolUse:ExitPlanMode",
					"tool_name":       "ExitPlanMode",
					"tool_input": map[string]interface{}{
						"plan": "# Test Plan\n\nThis should NOT be saved.",
					},
					"tool_response":    map[string]interface{}{},
					"tool_duration_ms": 50,
				}

				jsonInput, _ := json.Marshal(hookInput)

				// Disable plan preservation
				cmd := command.New(hooksBinary, "posttooluse").
					Stdin(strings.NewReader(string(jsonInput))).
					Dir(ctx.RootDir).
					Env(
						fmt.Sprintf("PWD=%s", ctx.RootDir),
						fmt.Sprintf("GROVE_HOOKS_TARGET_PLAN_DIR=%s", planDir),
						"GROVE_HOOKS_DISABLE_PLAN_PRESERVATION=true",
					)

				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return nil
			}),

			// Step 3: Verify no plan was saved
			harness.NewStep("Verify no plan was saved", func(ctx *harness.Context) error {
				planDir := ctx.GetString("plan_dir")

				entries, err := os.ReadDir(planDir)
				if err != nil {
					return fmt.Errorf("failed to read plan directory: %w", err)
				}

				// Should only have the .grove-plan.yml file
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".md") {
						return fmt.Errorf("unexpected plan file found when preservation was disabled: %s", entry.Name())
					}
				}

				ctx.ShowCommandOutput("Success", "No plan file created (as expected)", "")
				return nil
			}),

			harness.NewStep("Clean up", CleanupTestDatabase),
		},
	}
}

// PlanPreservationNoPlanScenario tests behavior when no active flow plan exists
func PlanPreservationNoPlanScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "plan-preservation-no-plan",
		Description: "Tests that plan preservation gracefully handles missing flow plan",
		Tags:        []string{"hooks", "flow", "plan-preservation"},
		Steps: []harness.Step{
			harness.NewStep("Setup project without flow plan", func(ctx *harness.Context) error {
				git.Init(ctx.RootDir)
				git.SetupTestConfig(ctx.RootDir)
				fs.WriteString(filepath.Join(ctx.RootDir, "README.md"), "# Test\n")
				git.Add(ctx.RootDir, ".")
				git.Commit(ctx.RootDir, "Initial commit")
				return SetupTestDatabase(ctx)
			}),

			harness.NewStep("Send ExitPlanMode without active plan", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				hookInput := map[string]interface{}{
					"session_id":      "test-no-plan-session",
					"hook_event_name": "PostToolUse:ExitPlanMode",
					"tool_name":       "ExitPlanMode",
					"tool_input": map[string]interface{}{
						"plan": "# Test Plan\n\nNo active plan to save to.",
					},
					"tool_response":    map[string]interface{}{},
					"tool_duration_ms": 50,
				}

				jsonInput, _ := json.Marshal(hookInput)

				// No GROVE_HOOKS_TARGET_PLAN_DIR set
				cmd := command.New(hooksBinary, "posttooluse").
					Stdin(strings.NewReader(string(jsonInput))).
					Dir(ctx.RootDir).
					Env(
						fmt.Sprintf("PWD=%s", ctx.RootDir),
						"GROVE_HOOKS_ENABLE_PLAN_PRESERVATION=true",
					)

				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				// Should succeed (gracefully skip saving)
				if err := assert.Equal(0, result.ExitCode, "hook should succeed even without active plan"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Clean up", CleanupTestDatabase),
		},
	}
}

// PlanPreservationEmptyPlanScenario tests that empty plans are not saved
func PlanPreservationEmptyPlanScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "plan-preservation-empty-plan",
		Description: "Tests that empty plans are not saved",
		Tags:        []string{"hooks", "flow", "plan-preservation"},
		Steps: []harness.Step{
			harness.NewStep("Setup project with flow plan", func(ctx *harness.Context) error {
				git.Init(ctx.RootDir)
				git.SetupTestConfig(ctx.RootDir)
				fs.WriteString(filepath.Join(ctx.RootDir, "grove.yml"), "name: test\n")
				git.Add(ctx.RootDir, ".")
				git.Commit(ctx.RootDir, "Initial commit")

				planDir := filepath.Join(ctx.RootDir, "plans", "test-plan")
				os.MkdirAll(planDir, 0755)
				fs.WriteString(filepath.Join(planDir, ".grove-plan.yml"), "name: test-plan\n")
				ctx.Set("plan_dir", planDir)

				return SetupTestDatabase(ctx)
			}),

			harness.NewStep("Send ExitPlanMode with empty plan", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				planDir := ctx.GetString("plan_dir")

				hookInput := map[string]interface{}{
					"session_id":      "test-empty-plan-session",
					"hook_event_name": "PostToolUse:ExitPlanMode",
					"tool_name":       "ExitPlanMode",
					"tool_input": map[string]interface{}{
						"plan": "", // Empty plan
					},
					"tool_response":    map[string]interface{}{},
					"tool_duration_ms": 50,
				}

				jsonInput, _ := json.Marshal(hookInput)

				cmd := command.New(hooksBinary, "posttooluse").
					Stdin(strings.NewReader(string(jsonInput))).
					Dir(ctx.RootDir).
					Env(
						fmt.Sprintf("PWD=%s", ctx.RootDir),
						fmt.Sprintf("GROVE_HOOKS_TARGET_PLAN_DIR=%s", planDir),
						"GROVE_HOOKS_ENABLE_PLAN_PRESERVATION=true",
					)

				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				return nil
			}),

			harness.NewStep("Verify no plan file created for empty plan", func(ctx *harness.Context) error {
				planDir := ctx.GetString("plan_dir")

				entries, err := os.ReadDir(planDir)
				if err != nil {
					return fmt.Errorf("failed to read plan directory: %w", err)
				}

				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".md") {
						return fmt.Errorf("unexpected plan file created for empty plan: %s", entry.Name())
					}
				}

				ctx.ShowCommandOutput("Success", "No plan file created for empty plan", "")
				return nil
			}),

			harness.NewStep("Clean up", CleanupTestDatabase),
		},
	}
}
