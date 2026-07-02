package commands

import (
	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"

	"github.com/grovetools/hooks/internal/hooks"
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
	rootCmd.AddCommand(NewStopAsyncCmd())
	rootCmd.AddCommand(newSessionStartCmd())
	rootCmd.AddCommand(newSessionStatusCmd())
	rootCmd.AddCommand(newSessionEndCmd())
	rootCmd.AddCommand(newSubagentStartCmd())
	rootCmd.AddCommand(newSubagentStopCmd())
	rootCmd.AddCommand(NewSessionsCmd())
	rootCmd.AddCommand(NewInstallCmd())
	rootCmd.AddCommand(NewVersionCmd())
	rootCmd.AddCommand(NewDebugWorkspacesCmd())
	rootCmd.AddCommand(NewOpencodeCmd())
	rootCmd.AddCommand(NewCodexCmd())
	rootCmd.AddCommand(NewPiCmd())
	rootCmd.AddCommand(newDisableHookCmd())
	rootCmd.AddCommand(newEnableHookCmd())
	rootCmd.AddCommand(newListHooksCmd())

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

func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Run the session start hook",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunSessionStartHook()
		},
	}
}

func newSessionStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-status",
		Short: "Run the session status hook (provider integrations)",
		Long: `Run the session-status hook.

Provider integrations (the opencode plugin) pipe a JSON payload
({"session_id": ..., "status": "busy|retry|running|idle|pending_user"}) to
report non-terminal status transitions into the grove session pipeline.`,
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunSessionStatusHook()
		},
	}
}

func newSessionEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-end",
		Short: "Run the session end hook (provider integrations)",
		Long: `Run the session-end hook.

Provider integrations (the opencode plugin) pipe a JSON payload
({"session_id": ..., "reason": "deleted"}) when the provider destroyed the
session. The session is marked completed and its registry entry removed.`,
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunSessionEndHook()
		},
	}
}

func newSubagentStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subagent-start",
		Short: "Run the subagent start hook",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunSubagentStartHook()
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
