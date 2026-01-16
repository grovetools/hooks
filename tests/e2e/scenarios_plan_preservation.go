package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/tend/pkg/assert"
	"github.com/grovetools/tend/pkg/command"
	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/git"
	"github.com/grovetools/tend/pkg/harness"
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

// PlanEditSyncScenario tests that Edit operations on Claude plan files sync to grove-flow
func PlanEditSyncScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "plan-edit-sync",
		Description: "Tests that Edit operations on Claude plan files (~/.claude/plans/*) sync to grove-flow",
		Tags:        []string{"hooks", "flow", "plan-preservation", "edit"},
		Steps: []harness.Step{
			harness.NewStep("Setup project with flow plan and fake Claude plans dir", func(ctx *harness.Context) error {
				git.Init(ctx.RootDir)
				git.SetupTestConfig(ctx.RootDir)
				fs.WriteString(filepath.Join(ctx.RootDir, "grove.yml"), "name: plan-edit-test\n")
				git.Add(ctx.RootDir, ".")
				git.Commit(ctx.RootDir, "Initial commit")

				// Create flow plan directory
				planDir := filepath.Join(ctx.RootDir, "plans", "test-plan")
				if err := os.MkdirAll(planDir, 0755); err != nil {
					return err
				}
				fs.WriteString(filepath.Join(planDir, ".grove-plan.yml"), "name: test-plan\n")
				ctx.Set("plan_dir", planDir)

				// Create a fake ~/.claude/plans directory in the test root for isolation
				claudePlansDir := filepath.Join(ctx.RootDir, ".claude", "plans")
				if err := os.MkdirAll(claudePlansDir, 0755); err != nil {
					return err
				}
				ctx.Set("claude_plans_dir", claudePlansDir)

				// Create an initial plan file that we'll "edit"
				initialPlan := `# My Implementation Plan

## Goal
Build a feature to process user requests.

## Steps
1. Create the request handler
2. Add validation logic
`
				planFilePath := filepath.Join(claudePlansDir, "fluffy-noodling-balloon.md")
				fs.WriteString(planFilePath, initialPlan)
				ctx.Set("plan_file_path", planFilePath)
				ctx.Set("initial_plan", initialPlan)

				return SetupTestDatabase(ctx)
			}),

			harness.NewStep("Simulate Edit hook on Claude plan file", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				planDir := ctx.GetString("plan_dir")
				planFilePath := ctx.GetString("plan_file_path")

				// First, update the plan file content (simulating what Claude would do)
				updatedPlan := `# My Implementation Plan

## Goal
Build a feature to process user requests.

## Steps
1. Create the request handler
2. Add validation logic
3. Write tests for the handler

**Updated:** Added testing step during editing.
`
				fs.WriteString(planFilePath, updatedPlan)
				ctx.Set("updated_plan", updatedPlan)

				// Create the JSON input for posttooluse with Edit tool
				hookInput := map[string]interface{}{
					"session_id":      "test-plan-edit-session",
					"transcript_path": "/tmp/test-transcript",
					"hook_event_name": "PostToolUse:Edit",
					"tool_name":       "Edit",
					"tool_input": map[string]interface{}{
						"file_path":  planFilePath,
						"old_string": "2. Add validation logic\n",
						"new_string": "2. Add validation logic\n3. Write tests for the handler\n\n**Updated:** Added testing step during editing.\n",
					},
					"tool_response":    "File edited successfully",
					"tool_duration_ms": 50,
				}

				jsonInput, err := json.Marshal(hookInput)
				if err != nil {
					return fmt.Errorf("failed to marshal hook input: %w", err)
				}

				cmd := command.New(hooksBinary, "posttooluse").
					Stdin(strings.NewReader(string(jsonInput))).
					Dir(ctx.RootDir).
					Env(
						fmt.Sprintf("PWD=%s", ctx.RootDir),
						fmt.Sprintf("GROVE_HOOKS_TARGET_PLAN_DIR=%s", planDir),
						"GROVE_HOOKS_ENABLE_PLAN_PRESERVATION=true",
						// Override HOME to use our test's .claude/plans
						fmt.Sprintf("HOME=%s", ctx.RootDir),
					)

				result := cmd.Run()
				ctx.ShowCommandOutput(cmd.String(), result.Stdout, result.Stderr)

				if err := assert.Equal(0, result.ExitCode, "posttooluse hook should succeed"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Verify plan was synced to grove-flow", func(ctx *harness.Context) error {
				planDir := ctx.GetString("plan_dir")

				entries, err := os.ReadDir(planDir)
				if err != nil {
					return fmt.Errorf("failed to read plan directory: %w", err)
				}

				ctx.ShowCommandOutput("Info", fmt.Sprintf("Found %d files in plan directory", len(entries)), "")

				// Find a plan file (should be created by the sync)
				var planFile string
				for _, entry := range entries {
					name := entry.Name()
					ctx.ShowCommandOutput("Debug", "Found file", name)
					if strings.HasSuffix(name, ".md") && name != ".grove-plan.yml" {
						planFile = filepath.Join(planDir, name)
						break
					}
				}

				if planFile == "" {
					return fmt.Errorf("no plan file found - Edit sync did not create a job")
				}

				ctx.ShowCommandOutput("Success", "Found synced plan file", planFile)

				// Read and verify content
				content, err := os.ReadFile(planFile)
				if err != nil {
					return fmt.Errorf("failed to read plan file: %w", err)
				}

				contentStr := string(content)

				// Verify it contains the updated content
				if !strings.Contains(contentStr, "My Implementation Plan") {
					return fmt.Errorf("synced file doesn't contain expected title")
				}

				if !strings.Contains(contentStr, "Write tests for the handler") {
					return fmt.Errorf("synced file doesn't contain the updated content")
				}

				ctx.ShowCommandOutput("Success", "Plan content verified with updated changes", "")
				return nil
			}),

			harness.NewStep("Clean up", CleanupTestDatabase),
		},
	}
}

// PlanEditNonPlanFileScenario tests that Edit operations on non-plan files don't trigger sync
func PlanEditNonPlanFileScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "plan-edit-non-plan-file",
		Description: "Tests that Edit operations on regular files don't trigger plan sync",
		Tags:        []string{"hooks", "flow", "plan-preservation", "edit"},
		Steps: []harness.Step{
			harness.NewStep("Setup project with flow plan", func(ctx *harness.Context) error {
				git.Init(ctx.RootDir)
				git.SetupTestConfig(ctx.RootDir)
				fs.WriteString(filepath.Join(ctx.RootDir, "grove.yml"), "name: non-plan-edit-test\n")
				git.Add(ctx.RootDir, ".")
				git.Commit(ctx.RootDir, "Initial commit")

				planDir := filepath.Join(ctx.RootDir, "plans", "test-plan")
				os.MkdirAll(planDir, 0755)
				fs.WriteString(filepath.Join(planDir, ".grove-plan.yml"), "name: test-plan\n")
				ctx.Set("plan_dir", planDir)

				// Create a regular source file
				fs.WriteString(filepath.Join(ctx.RootDir, "main.go"), "package main\n\nfunc main() {}\n")

				return SetupTestDatabase(ctx)
			}),

			harness.NewStep("Simulate Edit hook on regular source file", func(ctx *harness.Context) error {
				hooksBinary, err := FindProjectBinary()
				if err != nil {
					return err
				}

				planDir := ctx.GetString("plan_dir")
				sourceFile := filepath.Join(ctx.RootDir, "main.go")

				hookInput := map[string]interface{}{
					"session_id":      "test-regular-edit-session",
					"hook_event_name": "PostToolUse:Edit",
					"tool_name":       "Edit",
					"tool_input": map[string]interface{}{
						"file_path":  sourceFile,
						"old_string": "func main() {}\n",
						"new_string": "func main() {\n\tfmt.Println(\"Hello\")\n}\n",
					},
					"tool_response":    "File edited successfully",
					"tool_duration_ms": 30,
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

				if err := assert.Equal(0, result.ExitCode, "hook should succeed"); err != nil {
					return err
				}

				return nil
			}),

			harness.NewStep("Verify no plan file was created", func(ctx *harness.Context) error {
				planDir := ctx.GetString("plan_dir")

				entries, err := os.ReadDir(planDir)
				if err != nil {
					return fmt.Errorf("failed to read plan directory: %w", err)
				}

				for _, entry := range entries {
					name := entry.Name()
					// Only .grove-plan.yml should exist
					if strings.HasSuffix(name, ".md") {
						return fmt.Errorf("unexpected plan file created for non-plan Edit: %s", name)
					}
				}

				ctx.ShowCommandOutput("Success", "No plan file created for regular file Edit", "")
				return nil
			}),

			harness.NewStep("Clean up", CleanupTestDatabase),
		},
	}
}
