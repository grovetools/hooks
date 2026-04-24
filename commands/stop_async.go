package commands

import (
	"github.com/grovetools/hooks/internal/hooks"
	"github.com/spf13/cobra"
)

// NewStopAsyncCmd returns the `grove hooks stop-async` subcommand. It reads a
// Stop event payload on stdin, loads [[hooks.on_stop]] entries from grove.toml,
// and runs them in parallel with per-hook PID locking and session-scoped
// artifact directories. Exit 2 triggers Claude Code's asyncRewake.
func NewStopAsyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop-async",
		Short: "Run configured on_stop hooks asynchronously with rewake support",
		Run: func(cmd *cobra.Command, args []string) {
			hooks.RunStopAsyncHook()
		},
	}
}
