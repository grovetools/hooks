package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-tend/pkg/harness"
	"github.com/mattsolo1/grove-tend/pkg/project"
)

// FindProjectBinary finds the project's main binary path by reading grove.yml.
// This provides a single source of truth for locating the binary under test.
func FindProjectBinary() (string, error) {
	// The test runner is executed from the project root, so we start the search here.
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not get working directory: %w", err)
	}

	binaryPath, err := project.GetBinaryPath(wd)
	if err != nil {
		return "", fmt.Errorf("failed to find project binary via grove.yml: %w", err)
	}

	return binaryPath, nil
}

// SetupTestDatabase creates a temporary test database and returns a cleanup function
func SetupTestDatabase(ctx *harness.Context) error {
	// Create a temporary test database
	tempDir, err := os.MkdirTemp("", "grove-hooks-test-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	ctx.Set("test_temp_dir", tempDir)
	
	// Set environment variable for test database
	testDbPath := filepath.Join(tempDir, "test.db")
	os.Setenv("GROVE_HOOKS_DB_PATH", testDbPath)
	ctx.Set("test_db_path", testDbPath)
	
	return nil
}

// CleanupTestDatabase removes the test database
func CleanupTestDatabase(ctx *harness.Context) error {
	// Clean up test database
	tempDir := ctx.GetString("test_temp_dir")
	if tempDir != "" {
		os.RemoveAll(tempDir)
	}
	// Unset the environment variable
	os.Unsetenv("GROVE_HOOKS_DB_PATH")
	return nil
}