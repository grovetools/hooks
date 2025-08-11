package main

import (
	"os"
	"path/filepath"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := cli.NewStandardCommand(
		"hooks",
		"Claude hooks integration for Grove ecosystem",
	)
	
	// Check if called via symlink to determine which hook to run
	execName := filepath.Base(os.Args[0])
	
	// If called directly as 'hooks', show help
	if execName == "hooks" {
		// Add subcommands for manual execution
		rootCmd.AddCommand(newPreToolUseCmd())
		rootCmd.AddCommand(newPostToolUseCmd())
		rootCmd.AddCommand(newInitCmd())
		rootCmd.AddCommand(newCompleteCmd())
	} else {
		// Called via symlink, execute the corresponding hook
		switch execName {
		case "pretooluse":
			runPreToolUseHook()
		case "posttooluse":
			runPostToolUseHook()
		case "init":
			runInitHook()
		case "complete":
			runCompleteHook()
		default:
			rootCmd.Printf("Unknown hook: %s\n", execName)
			os.Exit(1)
		}
		return
	}
	
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newPreToolUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pretooluse",
		Short: "Run the pre-tool-use hook",
		Run: func(cmd *cobra.Command, args []string) {
			runPreToolUseHook()
		},
	}
}

func newPostToolUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "posttooluse",
		Short: "Run the post-tool-use hook",
		Run: func(cmd *cobra.Command, args []string) {
			runPostToolUseHook()
		},
	}
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Run the initialization hook",
		Run: func(cmd *cobra.Command, args []string) {
			runInitHook()
		},
	}
}

func newCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "complete",
		Short: "Run the completion hook",
		Run: func(cmd *cobra.Command, args []string) {
			runCompleteHook()
		},
	}
}

func runPreToolUseHook() {
	// TODO: Implement pre-tool-use hook logic
	os.Stdout.WriteString("Running pre-tool-use hook...\n")
}

func runPostToolUseHook() {
	// TODO: Implement post-tool-use hook logic
	os.Stdout.WriteString("Running post-tool-use hook...\n")
}

func runInitHook() {
	// TODO: Implement initialization hook logic
	os.Stdout.WriteString("Running initialization hook...\n")
}

func runCompleteHook() {
	// TODO: Implement completion hook logic
	os.Stdout.WriteString("Running completion hook...\n")
}