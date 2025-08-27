package main

import (
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-hooks/internal/commands"
	"github.com/mattsolo1/grove-hooks/internal/hooks"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
)

func main() {
	rootCmd := cli.NewStandardCommand(
		"hooks",
		"Claude hooks integration for Grove ecosystem",
	)

	// Check if called via symlink to determine which hook to run
	execName := filepath.Base(os.Args[0])

	// If called directly as 'hooks' or 'grove-hooks', show help
	if execName == "hooks" || execName == "grove-hooks" {
		// Add subcommands for manual execution
		rootCmd.AddCommand(newNotificationCmd())
		rootCmd.AddCommand(newPreToolUseCmd())
		rootCmd.AddCommand(newPostToolUseCmd())
		rootCmd.AddCommand(newStopCmd())
		rootCmd.AddCommand(newSubagentStopCmd())
		rootCmd.AddCommand(newSessionsCmd())
		rootCmd.AddCommand(newOneshotCmd())
		rootCmd.AddCommand(newInstallCmd())
		rootCmd.AddCommand(commands.NewVersionCmd())
	} else {
		// Called via symlink, execute the corresponding hook
		switch execName {
		case "notification":
			runNotificationHook()
		case "pretooluse", "pre-tool-use":
			runPreToolUseHook()
		case "posttooluse", "post-tool-use":
			runPostToolUseHook()
		case "stop", "complete":
			runStopHook()
		case "subagent-stop":
			runSubagentStopHook()
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

func newNotificationCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "notification",
		Short: "Run the notification hook",
		Run: func(cmd *cobra.Command, args []string) {
			runNotificationHook()
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Run the stop hook",
		Run: func(cmd *cobra.Command, args []string) {
			runStopHook()
		},
	}
}

func newSubagentStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subagent-stop",
		Short: "Run the subagent stop hook",
		Run: func(cmd *cobra.Command, args []string) {
			runSubagentStopHook()
		},
	}
}

func runPreToolUseHook() {
	hooks.RunPreToolUseHook()
}

func runPostToolUseHook() {
	hooks.RunPostToolUseHook()
}

func runNotificationHook() {
	hooks.RunNotificationHook()
}

func runStopHook() {
	hooks.RunStopHook()
}

func runSubagentStopHook() {
	hooks.RunSubagentStopHook()
}

func newSessionsCmd() *cobra.Command {
	return commands.NewSessionsCmd()
}

func newOneshotCmd() *cobra.Command {
	return commands.NewOneshotCmd()
}

func newInstallCmd() *cobra.Command {
	return commands.NewInstallCmd()
}
