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
	// Use the grove-tend RootDir instead of creating a new temp directory
	// This way all artifacts are in the directory that grove-tend preserves with -d flag
	tempDir := ctx.RootDir

	// Set environment variable for test database
	testDbPath := filepath.Join(tempDir, "test.db")
	os.Setenv("GROVE_HOOKS_DB_PATH", testDbPath)
	ctx.Set("test_db_path", testDbPath)

	// Set XDG_DATA_HOME to RootDir (NOT RootDir/.grove!)
	// When expandPath sees ~/.grove/hooks/sessions with XDG_DATA_HOME set,
	// it strips .grove/ and joins: XDG_DATA_HOME/hooks/sessions
	// So XDG_DATA_HOME should be the base directory, not include .grove
	xdgDataHome := tempDir
	oldXdgDataHome := os.Getenv("XDG_DATA_HOME")
	ctx.Set("old_xdg_data_home", oldXdgDataHome)
	os.Setenv("XDG_DATA_HOME", xdgDataHome)
	ctx.Set("xdg_data_home", xdgDataHome)

	return nil
}

// CleanupTestDatabase removes the test database
func CleanupTestDatabase(ctx *harness.Context) error {
	// Restore original XDG_DATA_HOME
	oldXdgDataHome := ctx.GetString("old_xdg_data_home")
	if oldXdgDataHome != "" {
		os.Setenv("XDG_DATA_HOME", oldXdgDataHome)
	} else {
		os.Unsetenv("XDG_DATA_HOME")
	}

	// Unset the environment variable
	os.Unsetenv("GROVE_HOOKS_DB_PATH")

	// Note: We don't remove ctx.RootDir - grove-tend manages that
	return nil
}
