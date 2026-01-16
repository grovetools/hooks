package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/project"
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

	// Set HOME to a sandboxed directory within the test root.
	// This ensures that grove-core's FileSystemRegistry (which uses os.UserHomeDir())
	// writes session files to the test directory instead of the real home.
	// os.UserHomeDir() respects the HOME environment variable on Unix systems.
	sandboxedHome := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(sandboxedHome, 0755); err != nil {
		return fmt.Errorf("failed to create sandboxed home: %w", err)
	}

	oldHome := os.Getenv("HOME")
	ctx.Set("old_home", oldHome)
	os.Setenv("HOME", sandboxedHome)
	ctx.Set("sandboxed_home", sandboxedHome)

	// Also set XDG_DATA_HOME for any code that checks it
	oldXdgDataHome := os.Getenv("XDG_DATA_HOME")
	ctx.Set("old_xdg_data_home", oldXdgDataHome)
	os.Setenv("XDG_DATA_HOME", filepath.Join(sandboxedHome, ".local", "share"))
	ctx.Set("xdg_data_home", filepath.Join(sandboxedHome, ".local", "share"))

	return nil
}

// CleanupTestDatabase removes the test database
func CleanupTestDatabase(ctx *harness.Context) error {
	// Restore original HOME
	oldHome := ctx.GetString("old_home")
	if oldHome != "" {
		os.Setenv("HOME", oldHome)
	} else {
		os.Unsetenv("HOME")
	}

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
