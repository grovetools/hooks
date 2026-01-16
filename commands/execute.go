package commands

import (
	"os"
	"path/filepath"

	"github.com/grovetools/hooks/internal/hooks"
)

// Execute runs the appropriate command or hook based on how the binary was called.
func Execute() {
	execName := filepath.Base(os.Args[0])

	// Handle symlink-based hook execution
	switch execName {
	case "notification":
		hooks.RunNotificationHook()
		return
	case "pretooluse", "pre-tool-use":
		hooks.RunPreToolUseHook()
		return
	case "posttooluse", "post-tool-use":
		hooks.RunPostToolUseHook()
		return
	case "stop", "complete":
		hooks.RunStopHook()
		return
	case "subagent-stop":
		hooks.RunSubagentStopHook()
		return
	}

	// If not called via a hook symlink, run the full CLI
	rootCmd := NewRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
