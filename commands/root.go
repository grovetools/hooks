package commands

import (
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-hooks/internal/hooks"
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root command for grove-hooks.
func NewRootCmd() *cobra.Command {
	rootCmd := cli.NewStandardCommand(
		"hooks",
		"Claude hooks integration for Grove ecosystem",
	)

	// Add subcommands for manual execution
	rootCmd.AddCommand(newNotificationCmd())
	rootCmd.AddCommand(newPreToolUseCmd())
	rootCmd.AddCommand(newPostToolUseCmd())
	rootCmd.AddCommand(newStopCmd())
	rootCmd.AddCommand(newSubagentStopCmd())
	rootCmd.AddCommand(NewSessionsCmd())
	rootCmd.AddCommand(NewInstallCmd())
	rootCmd.AddCommand(NewVersionCmd())
	rootCmd.AddCommand(NewDebugWorkspacesCmd())
	rootCmd.AddCommand(NewOpencodeCmd())

	tuiCmd := NewBrowseCmd()
	tuiCmd.Use = "tui"
	tuiCmd.Aliases = []string{"browse", "b"}
	rootCmd.AddCommand(tuiCmd)

	return rootCmd
}

func newPreToolUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pretooluse",
		Short: "Run the pre-tool-use hook",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunPreToolUseHook()
		},
	}
}

func newPostToolUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "posttooluse",
		Short: "Run the post-tool-use hook",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunPostToolUseHook()
		},
	}
}

func newNotificationCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "notification",
		Short: "Run the notification hook",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunNotificationHook()
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Run the stop hook",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunStopHook()
		},
	}
}

func newSubagentStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subagent-stop",
		Short: "Run the subagent stop hook",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunSubagentStopHook()
		},
	}
}
